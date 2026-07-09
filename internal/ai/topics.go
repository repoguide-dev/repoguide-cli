package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

// maxSessionPrompts caps user prompts extracted from a single session.
const maxSessionPrompts = 20

// promptsPerSession caps how many of a session's prompts reach the topic prompt.
// The first few state the task; later ones are usually corrections.
const promptsPerSession = 3

const (
	topicModel         = "claude-sonnet-4-6"
	minSessions        = 1
	minTopicConfidence = 0.30
)

type seenWithEntry struct {
	File     string `json:"file"`
	Sessions int    `json:"sessions"`
}

type commandEntry struct {
	Text     string `json:"text"`
	Runs     int    `json:"runs"`
	Failures int    `json:"failures,omitempty"`
}

type topicCandidate struct {
	subsystem           contracts.RepoAnalysisSubsystem
	prompts             []string
	commands            []commandEntry
	lastActive          string
	tests               []string
	topReadFiles        []string
	seenWith            []seenWithEntry
	readFiles           int
	fileClassifications map[string][]string
	readBeforeEditHints []string
	testTouchSignal     string
}

// DeriveTopicsAndGenerateContext derives named TopicContext entries from a RepoAnalysisBundle.
func DeriveTopicsAndGenerateContext(ctx context.Context, bundle contracts.RepoAnalysisBundle) ([]model.TopicContext, Usage, error) {
	candidates := buildCandidates(bundle)
	candidates = filterAndRank(candidates)
	repoCtx := buildRepoFileContext(bundle.Repo.Root, recentPrompts(bundle.Sessions, 50))
	if len(candidates) == 0 {
		candidates = buildFileStructureCandidates(bundle.Repo.Root)
		if len(candidates) == 0 {
			return nil, Usage{}, nil
		}
	}
	return nameTopics(ctx, candidates, repoCtx)
}

// GenerateTopicContextFromFeedback generates a TopicContext for a single topic, using agent
// feedback and the full event log from one session as primary evidence, and the repo analysis
// bundle for file classification context. Returns (nil, reason, ...) if the AI decides to skip.
func GenerateTopicContextFromFeedback(
	ctx context.Context,
	suggestedName, feedback string,
	bundle contracts.RepoAnalysisBundle,
	events []model.SessionEvent,
	existingTopics []model.TopicContext,
) (*model.TopicContext, string, Usage, error) {
	summary := buildFeedbackSessionSummary(events, bundle)
	sessionJSON, _ := json.Marshal(summary)
	existing := make([]prompts.ExistingTopicSummary, len(existingTopics))
	for i, t := range existingTopics {
		existing[i] = prompts.ExistingTopicSummary{Name: t.Name, Summary: t.Summary}
	}
	existingJSON, _ := json.Marshal(existing)
	prompt := prompts.BuildFeedbackTopicPrompt(suggestedName, feedback, string(sessionJSON), string(existingJSON))

	raw, usage, err := callClaude(ctx, topicModel, prompt)
	if err != nil {
		return nil, "", usage, err
	}
	raw = strings.TrimSpace(stripFences(raw))
	if raw == "null" || raw == "" {
		return nil, "invalid", usage, nil
	}
	var skip struct {
		Skip string `json:"skip"`
	}
	if err := json.Unmarshal([]byte(raw), &skip); err == nil && skip.Skip != "" {
		return nil, skip.Skip, usage, nil
	}

	result, parseErr := parseSingleTopicResult(raw)
	if parseErr != nil {
		return nil, "", usage, parseErr
	}
	if result.Confidence < minTopicConfidence {
		result.Confidence = minTopicConfidence
	}

	topic := model.TopicContext{
		ID:             toID(result.Name, 0),
		Name:           result.Name,
		Summary:        result.Summary,
		Confidence:     result.Confidence,
		WhenToUse:      result.WhenToUse,
		PromptKeywords: result.PromptKeywords,
		StartHere:      mapStartHere(result.StartHere),
		ImportantFiles: model.TopicImportantFiles{
			EditTargets:       result.ImportantFiles.EditTargets,
			ReferenceFiles:    result.ImportantFiles.ReferenceFiles,
			TestFiles:         result.ImportantFiles.TestFiles,
			CrossCuttingFiles: result.ImportantFiles.CrossCuttingFiles,
		},
		Tests: model.TopicTests{
			StartWith: result.Tests.StartWith,
			Signal:    result.Tests.Signal,
			Notes:     result.Tests.Notes,
			Commands:  result.Tests.Commands,
		},
		KnownWorkflows:   result.KnownWorkflows,
		AvoidWastingTime: result.AvoidWastingTime,
		RiskFlags:        result.RiskFlags,
		Evidence: model.TopicEvidence{
			Sessions:    1,
			EditedFiles: len(summary.EditedFiles),
			ReadFiles:   len(summary.ReadFiles),
			LastActive:  lastEventDate(events),
		},
	}
	return &topic, "", usage, nil
}

func buildFeedbackSessionSummary(events []model.SessionEvent, bundle contracts.RepoAnalysisBundle) prompts.FeedbackTopicSessionData {
	fileLabelsIndex := make(map[string][]string, len(bundle.Files))
	for _, f := range bundle.Files {
		if len(f.Classification) > 0 {
			fileLabelsIndex[f.Path] = f.Classification
		}
	}

	var userPrompts []string
	toolSet := map[string]struct{}{}
	var toolCalls []string
	readSet := map[string]struct{}{}
	var readFiles []string
	editSet := map[string]struct{}{}
	var editedFiles []string
	commandSet := map[string]struct{}{}
	var commands []string
	pendingCommands := map[string]string{}
	failedSet := map[string]struct{}{}
	var failedCommands []string

	for _, ev := range events {
		if ev.Kind == "prompt" {
			if t := strings.TrimSpace(ev.Text); t != "" && len(userPrompts) < maxSessionPrompts {
				userPrompts = append(userPrompts, t)
			}
		}
		if ev.Kind == "tool_call" && ev.ToolName != "" {
			if _, ok := toolSet[ev.ToolName]; !ok {
				toolSet[ev.ToolName] = struct{}{}
				toolCalls = append(toolCalls, ev.ToolName)
			}
		}
		if ev.Kind == "tool_call" && ev.CommandText != "" {
			if text := strings.Join(strings.Fields(ev.CommandText), " "); text != "" && len([]rune(text)) <= 200 {
				if _, ok := commandSet[text]; !ok && len(commands) < 15 {
					commandSet[text] = struct{}{}
					commands = append(commands, text)
				}
				if ev.ToolCallID != "" {
					pendingCommands[ev.ToolCallID] = text
				}
			}
		}
		if ev.Kind == "tool_result" && ev.IsError && ev.ToolCallID != "" {
			if text, ok := pendingCommands[ev.ToolCallID]; ok {
				if _, seen := failedSet[text]; !seen {
					failedSet[text] = struct{}{}
					failedCommands = append(failedCommands, text)
				}
			}
		}
		for _, p := range ev.ReadPaths {
			p = toRepoRel(bundle.Repo.Root, p)
			if p == "" {
				continue
			}
			if _, ok := readSet[p]; !ok {
				readSet[p] = struct{}{}
				readFiles = append(readFiles, p)
			}
		}
		for _, p := range ev.WritePaths {
			p = toRepoRel(bundle.Repo.Root, p)
			if p == "" {
				continue
			}
			if _, ok := editSet[p]; !ok {
				editSet[p] = struct{}{}
				editedFiles = append(editedFiles, p)
			}
		}
	}

	fileLabels := make(map[string][]string)
	for _, p := range append(readFiles, editedFiles...) {
		if labels, ok := fileLabelsIndex[p]; ok {
			fileLabels[p] = labels
		}
	}
	if len(fileLabels) == 0 {
		fileLabels = nil
	}

	return prompts.FeedbackTopicSessionData{
		Prompts:        userPrompts,
		ToolCalls:      toolCalls,
		Commands:       commands,
		FailedCommands: failedCommands,
		EditedFiles:    editedFiles,
		ReadFiles:      readFiles,
		FileLabels:     fileLabels,
	}
}

func parseSingleTopicResult(raw string) (llmTopicResult, error) {
	if strings.TrimSpace(raw) == "" {
		return llmTopicResult{}, ioEOF("empty model response")
	}
	// Try single object first
	var result llmTopicResult
	if err := json.Unmarshal([]byte(raw), &result); err == nil {
		return result, nil
	}
	// Fall back to array-of-one (model may wrap in array despite instructions)
	var results []llmTopicResult
	if err := json.Unmarshal([]byte(raw), &results); err == nil && len(results) > 0 {
		return results[0], nil
	}
	return llmTopicResult{}, fmt.Errorf("could not parse topic result from: %.80s", raw)
}

type bundleFileInfo struct {
	kind  string
	reads int
	path  string
}

func buildCandidates(bundle contracts.RepoAnalysisBundle) []topicCandidate {
	sessionPrompts := make(map[string][]string, len(bundle.Sessions))
	sessionCommands := make(map[string][]contracts.RepoAnalysisCommand, len(bundle.Sessions))
	for _, s := range bundle.Sessions {
		sessionPrompts[s.ID] = s.Prompts
		sessionCommands[s.ID] = s.Commands
	}

	fileIndex := make(map[string]bundleFileInfo, len(bundle.Files))
	for _, f := range bundle.Files {
		fileIndex[f.Path] = bundleFileInfo{kind: f.Kind, reads: f.Reads, path: f.Path}
	}

	// seenWithIndex[file] -> partner -> total sessions
	seenWithIndex := make(map[string]map[string]int)
	for _, rel := range bundle.SeenWithGroups {
		files := relationFiles(rel)
		for i, f := range files {
			for j, partner := range files {
				if i == j {
					continue
				}
				if seenWithIndex[f] == nil {
					seenWithIndex[f] = map[string]int{}
				}
				seenWithIndex[f][partner] += rel.Sessions
			}
		}
	}

	// Build read-before-edit hints index by directory
	type rbePair struct{ source, target string }
	rbeByDir := make(map[string][]rbePair)
	for _, trace := range bundle.TracePatterns {
		if trace.Type != "read_before_edit_pattern" || trace.Source == "" || trace.Target == "" {
			continue
		}
		srcDir := filepath.ToSlash(filepath.Dir(trace.Source))
		tgtDir := filepath.ToSlash(filepath.Dir(trace.Target))
		rbeByDir[srcDir] = append(rbeByDir[srcDir], rbePair{trace.Source, trace.Target})
		if srcDir != tgtDir {
			rbeByDir[tgtDir] = append(rbeByDir[tgtDir], rbePair{trace.Source, trace.Target})
		}
	}

	candidates := make([]topicCandidate, 0, len(bundle.Subsystems))
	for _, sub := range bundle.Subsystems {
		if sub.Sessions < minSessions {
			continue
		}

		dir := filepath.ToSlash(sub.Name)
		dirPrefix := dir + "/"

		// First promptsPerSession prompts of every related session (newest first).
		// Breadth over depth: one session's follow-ups must not crowd out the
		// task-defining prompts of the other sessions in this directory.
		seenP := map[string]struct{}{}
		var prompts []string
		for _, ref := range sub.RelatedSessions {
			for i, p := range sessionPrompts[ref.ID] {
				if i >= promptsPerSession {
					break
				}
				if _, ok := seenP[p]; !ok {
					seenP[p] = struct{}{}
					prompts = append(prompts, p)
				}
			}
		}

		// commands actually run in related sessions, aggregated across sessions
		commands := aggregateCommands(sub.RelatedSessions, sessionCommands)

		// RelatedSessions are sorted newest first
		lastActive := ""
		if len(sub.RelatedSessions) > 0 && len(sub.RelatedSessions[0].Timestamp) >= 10 {
			lastActive = sub.RelatedSessions[0].Timestamp[:10]
		}

		// test files, top read files, and file classifications in one pass
		var tests []string
		type readEntry struct {
			path  string
			reads int
		}
		var readEntries []readEntry
		fileClassifications := map[string][]string{}

		for _, f := range bundle.Files {
			fp := filepath.ToSlash(f.Path)
			if fp != dir && !strings.HasPrefix(fp, dirPrefix) {
				continue
			}
			if f.Kind == "test" {
				tests = append(tests, f.Path)
			} else if f.Reads > 0 {
				readEntries = append(readEntries, readEntry{f.Path, f.Reads})
			}
			if len(f.Classification) > 0 {
				fileClassifications[f.Path] = f.Classification
			}
		}

		sort.Slice(readEntries, func(i, j int) bool { return readEntries[i].reads > readEntries[j].reads })
		topReadFiles := make([]string, 0, 5)
		for _, e := range readEntries {
			if len(topReadFiles) >= 5 {
				break
			}
			topReadFiles = append(topReadFiles, e.path)
		}

		// outside-dir files seen together with subsystem files
		partnerSessions := map[string]int{}
		for _, f := range sub.Paths {
			for partner, n := range seenWithIndex[f] {
				fp := filepath.ToSlash(partner)
				if fp != dir && !strings.HasPrefix(fp, dirPrefix) {
					partnerSessions[partner] += n
				}
			}
		}
		type partnerEntry struct {
			file     string
			sessions int
		}
		partners := make([]partnerEntry, 0, len(partnerSessions))
		for file, n := range partnerSessions {
			partners = append(partners, partnerEntry{file, n})
		}
		sort.Slice(partners, func(i, j int) bool { return partners[i].sessions > partners[j].sessions })
		seenWith := make([]seenWithEntry, 0, 8)
		for _, p := range partners {
			if len(seenWith) >= 8 {
				break
			}
			seenWith = append(seenWith, seenWithEntry{File: p.file, Sessions: p.sessions})
		}

		// read-before-edit hints for this directory (deduplicated, capped at 5)
		var rbeHints []string
		seenH := map[string]struct{}{}
		for _, pair := range rbeByDir[dir] {
			hint := fmt.Sprintf("Read %s before editing %s", pair.source, pair.target)
			if _, ok := seenH[hint]; !ok {
				seenH[hint] = struct{}{}
				rbeHints = append(rbeHints, hint)
				if len(rbeHints) >= 5 {
					break
				}
			}
		}

		candidates = append(candidates, topicCandidate{
			subsystem:           sub,
			prompts:             prompts,
			commands:            commands,
			lastActive:          lastActive,
			tests:               dedupeStrings(tests),
			topReadFiles:        topReadFiles,
			seenWith:            seenWith,
			readFiles:           sub.Reads,
			fileClassifications: fileClassifications,
			readBeforeEditHints: rbeHints,
			testTouchSignal:     deriveTestSignal(sub),
		})
	}
	return candidates
}

// aggregateCommands merges observed commands across a subsystem's related
// sessions, summing run/failure counts, ordered by runs, capped at 8.
func aggregateCommands(refs []contracts.RepoAnalysisSessionRef, sessionCommands map[string][]contracts.RepoAnalysisCommand) []commandEntry {
	stats := map[string]*commandEntry{}
	var order []string
	for _, ref := range refs {
		for _, cmd := range sessionCommands[ref.ID] {
			entry := stats[cmd.Text]
			if entry == nil {
				entry = &commandEntry{Text: cmd.Text}
				stats[cmd.Text] = entry
				order = append(order, cmd.Text)
			}
			entry.Runs += cmd.Runs
			entry.Failures += cmd.Failures
		}
	}
	out := make([]commandEntry, 0, len(order))
	for _, text := range order {
		out = append(out, *stats[text])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Runs > out[j].Runs })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func deriveTestSignal(sub contracts.RepoAnalysisSubsystem) string {
	for _, label := range sub.Classification {
		if label == "low_test_context" {
			return "source often changed without tests"
		}
	}
	if sub.TestTouchedSessions == 0 {
		return "no clear test signal"
	}
	if sub.TestTouchRate >= 0.5 && sub.TestEdits > 0 {
		return "tests usually edited with source"
	}
	if sub.TestTouchRate >= 0.3 && sub.TestReads > sub.TestEdits {
		return "tests used as spec"
	}
	return "no clear test signal"
}

func filterAndRank(candidates []topicCandidate) []topicCandidate {
	sort.Slice(candidates, func(i, j int) bool {
		return topicScore(candidates[i]) > topicScore(candidates[j])
	})
	return candidates
}

func topicScore(c topicCandidate) float64 {
	return float64(c.subsystem.Sessions)*3.0 + float64(c.subsystem.Edits)
}

type llmTopicGroup struct {
	GroupID             string              `json:"group_id"`
	DirectoryHint       string              `json:"directory_hint"`
	SessionCount        int                 `json:"session_count"`
	LastActive          string              `json:"last_active,omitempty"`
	Prompts             []string            `json:"prompts"`
	Commands            []commandEntry      `json:"commands,omitempty"`
	TopEditedFiles      []string            `json:"top_edited_files"`
	TopReadFiles        []string            `json:"top_read_files,omitempty"`
	TestFiles           []string            `json:"test_files,omitempty"`
	SeenWith            []seenWithEntry     `json:"seen_with,omitempty"`
	SubsystemLabels     []string            `json:"subsystem_labels,omitempty"`
	FileLabels          map[string][]string `json:"file_labels,omitempty"`
	TestTouchSignal     string              `json:"test_touch_signal,omitempty"`
	ReadBeforeEditHints []string            `json:"read_before_edit_hints,omitempty"`
}

type llmStartFile struct {
	Path string `json:"path"`
	Role string `json:"role"`
	Why  string `json:"why"`
}

type llmImportantFiles struct {
	EditTargets       []string `json:"edit_targets,omitempty"`
	ReferenceFiles    []string `json:"reference_files,omitempty"`
	TestFiles         []string `json:"test_files,omitempty"`
	CrossCuttingFiles []string `json:"cross_cutting_files,omitempty"`
}

type llmTests struct {
	StartWith []string `json:"start_with,omitempty"`
	Signal    string   `json:"signal"`
	Notes     []string `json:"notes,omitempty"`
	Commands  []string `json:"commands,omitempty"`
}

type llmTopicResult struct {
	GroupIDs         []string          `json:"group_ids,omitempty"`
	Name             string            `json:"name"`
	Summary          string            `json:"summary"`
	WhenToUse        []string          `json:"when_to_use"`
	PromptKeywords   []string          `json:"prompt_keywords"`
	Confidence       float64           `json:"confidence"`
	StartHere        []llmStartFile    `json:"start_here"`
	ImportantFiles   llmImportantFiles `json:"important_files"`
	Tests            llmTests          `json:"tests"`
	KnownWorkflows   []string          `json:"known_workflows"`
	AvoidWastingTime []string          `json:"avoid_wasting_time"`
	RiskFlags        []string          `json:"risk_flags"`
	Evidence         struct {
		Reason                string   `json:"reason"`
		RepresentativePrompts []string `json:"representative_prompts"`
	} `json:"evidence"`
}

func nameTopics(ctx context.Context, candidates []topicCandidate, repoCtx string) ([]model.TopicContext, Usage, error) {
	groups := make([]llmTopicGroup, 0, len(candidates))
	groupByID := make(map[string]topicCandidate, len(candidates))
	groupOrder := make([]string, 0, len(candidates))
	for _, c := range candidates {
		groupID := toID(c.subsystem.Name, len(groupOrder))
		fileLabels := c.fileClassifications
		if len(fileLabels) == 0 {
			fileLabels = nil
		}
		groupByID[groupID] = c
		groupOrder = append(groupOrder, groupID)
		groups = append(groups, llmTopicGroup{
			GroupID:             groupID,
			DirectoryHint:       c.subsystem.Name,
			SessionCount:        c.subsystem.Sessions,
			LastActive:          c.lastActive,
			Prompts:             c.prompts,
			Commands:            c.commands,
			TopEditedFiles:      c.subsystem.TopFiles,
			TopReadFiles:        c.topReadFiles,
			TestFiles:           c.tests,
			SeenWith:            c.seenWith,
			SubsystemLabels:     c.subsystem.Classification,
			FileLabels:          fileLabels,
			TestTouchSignal:     c.testTouchSignal,
			ReadBeforeEditHints: c.readBeforeEditHints,
		})
	}

	groupsJSON, _ := json.Marshal(groups)
	prompt := prompts.BuildTopicPrompt(string(groupsJSON), repoCtx)

	raw, usage, err := callClaude(ctx, topicModel, prompt)
	if err != nil {
		return nil, usage, err
	}

	raw = stripFences(raw)

	named, parseErr := parseTopicResults(raw)
	topics := materializeTopicContexts(named, groupByID, groupOrder)
	if parseErr != nil {
		if len(named) == 0 {
			return fallbackTopicContexts(candidates), usage, nil
		}
		return topics, usage, nil
	}
	return topics, usage, nil
}

type aggregatedTopicEvidence struct {
	sessions    int
	editedFiles int
	readFiles   int
	lastActive  string
}

func materializeTopicContexts(results []llmTopicResult, groupByID map[string]topicCandidate, groupOrder []string) []model.TopicContext {
	topics := make([]model.TopicContext, 0, len(results))
	for i, result := range results {
		if result.Confidence < minTopicConfidence {
			continue
		}

		groupIDs := normalizeTopicGroupIDs(result.GroupIDs, groupByID, groupOrder, i, len(results))
		if len(groupIDs) == 0 {
			continue
		}

		evidence := aggregateTopicEvidence(groupIDs, groupByID)
		topics = append(topics, model.TopicContext{
			ID:             toID(result.Name, i),
			Name:           result.Name,
			Summary:        result.Summary,
			Confidence:     result.Confidence,
			WhenToUse:      result.WhenToUse,
			PromptKeywords: result.PromptKeywords,
			StartHere:      mapStartHere(result.StartHere),
			ImportantFiles: model.TopicImportantFiles{
				EditTargets:       result.ImportantFiles.EditTargets,
				ReferenceFiles:    result.ImportantFiles.ReferenceFiles,
				TestFiles:         result.ImportantFiles.TestFiles,
				CrossCuttingFiles: result.ImportantFiles.CrossCuttingFiles,
			},
			Tests: model.TopicTests{
				StartWith: result.Tests.StartWith,
				Signal:    result.Tests.Signal,
				Notes:     result.Tests.Notes,
				Commands:  result.Tests.Commands,
			},
			KnownWorkflows:   result.KnownWorkflows,
			AvoidWastingTime: result.AvoidWastingTime,
			RiskFlags:        result.RiskFlags,
			Evidence: model.TopicEvidence{
				Sessions:    evidence.sessions,
				EditedFiles: evidence.editedFiles,
				ReadFiles:   evidence.readFiles,
				LastActive:  evidence.lastActive,
			},
		})
	}
	return topics
}

func normalizeTopicGroupIDs(groupIDs []string, groupByID map[string]topicCandidate, groupOrder []string, index, totalResults int) []string {
	seen := make(map[string]struct{}, len(groupIDs))
	normalized := make([]string, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if _, ok := groupByID[groupID]; !ok {
			continue
		}
		if _, ok := seen[groupID]; ok {
			continue
		}
		seen[groupID] = struct{}{}
		normalized = append(normalized, groupID)
	}
	if len(normalized) > 0 {
		return normalized
	}
	// Positional fallback: use the candidate at this index when group_ids don't match.
	if index < len(groupOrder) {
		return []string{groupOrder[index]}
	}
	return nil
}

func aggregateTopicEvidence(groupIDs []string, groupByID map[string]topicCandidate) aggregatedTopicEvidence {
	var out aggregatedTopicEvidence
	for _, groupID := range groupIDs {
		candidate, ok := groupByID[groupID]
		if !ok {
			continue
		}
		out.sessions += candidate.subsystem.Sessions
		out.editedFiles += candidate.subsystem.SourceEdits + candidate.subsystem.TestEdits
		out.readFiles += candidate.readFiles
		if candidate.lastActive > out.lastActive {
			out.lastActive = candidate.lastActive
		}
	}
	return out
}

func fallbackTopicContexts(candidates []topicCandidate) []model.TopicContext {
	topics := make([]model.TopicContext, 0, len(candidates))
	for i, candidate := range candidates {
		result := fallbackTopicResult(candidate)
		topics = append(topics, model.TopicContext{
			ID:             toID(result.Name, i),
			Name:           result.Name,
			Summary:        result.Summary,
			Confidence:     result.Confidence,
			WhenToUse:      result.WhenToUse,
			PromptKeywords: result.PromptKeywords,
			StartHere:      mapStartHere(result.StartHere),
			ImportantFiles: model.TopicImportantFiles{
				EditTargets:       result.ImportantFiles.EditTargets,
				ReferenceFiles:    result.ImportantFiles.ReferenceFiles,
				TestFiles:         result.ImportantFiles.TestFiles,
				CrossCuttingFiles: result.ImportantFiles.CrossCuttingFiles,
			},
			Tests: model.TopicTests{
				StartWith: result.Tests.StartWith,
				Signal:    result.Tests.Signal,
				Notes:     result.Tests.Notes,
				Commands:  result.Tests.Commands,
			},
			KnownWorkflows:   result.KnownWorkflows,
			AvoidWastingTime: result.AvoidWastingTime,
			RiskFlags:        result.RiskFlags,
			Evidence: model.TopicEvidence{
				Sessions:    candidate.subsystem.Sessions,
				EditedFiles: candidate.subsystem.SourceEdits + candidate.subsystem.TestEdits,
				ReadFiles:   candidate.readFiles,
				LastActive:  candidate.lastActive,
			},
		})
	}
	return topics
}

// lastEventDate returns the date (YYYY-MM-DD) of the last timestamped event.
func lastEventDate(events []model.SessionEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if len(events[i].Timestamp) >= 10 {
			return events[i].Timestamp[:10]
		}
	}
	return ""
}

// toRepoRel converts an absolute path to repo-relative form. Returns "" for paths outside the repo.
// Falls through unchanged if root is empty or path is already relative.
func toRepoRel(root, path string) string {
	if root == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

func mapStartHere(files []llmStartFile) []model.TopicStartFile {
	out := make([]model.TopicStartFile, 0, len(files))
	for _, f := range files {
		out = append(out, model.TopicStartFile{Path: f.Path, Role: f.Role, Why: f.Why})
	}
	return out
}

func parseTopicResults(raw string) ([]llmTopicResult, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, ioEOF("empty model response")
	}

	var named []llmTopicResult
	if err := json.Unmarshal([]byte(raw), &named); err == nil {
		return named, nil
	}

	start := strings.IndexByte(raw, '[')
	if start == -1 {
		return nil, ioEOF("missing JSON array")
	}

	decoder := json.NewDecoder(strings.NewReader(raw[start:]))
	tok, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '[' {
		return nil, fmt.Errorf("expected JSON array")
	}

	recovered := make([]llmTopicResult, 0)
	for decoder.More() {
		var item llmTopicResult
		if err := decoder.Decode(&item); err != nil {
			if len(recovered) > 0 {
				return recovered, err
			}
			return nil, err
		}
		recovered = append(recovered, item)
	}
	if _, err := decoder.Token(); err != nil {
		if len(recovered) > 0 {
			return recovered, err
		}
		return nil, err
	}
	return recovered, nil
}

func fallbackTopicResult(c topicCandidate) llmTopicResult {
	name := fallbackTopicName(c)
	referenceFiles := make([]string, 0, len(c.topReadFiles))
	editTargets := dedupeStrings(append([]string(nil), c.subsystem.TopFiles...))
	editTargetSet := make(map[string]struct{}, len(editTargets))
	for _, path := range editTargets {
		editTargetSet[path] = struct{}{}
	}
	for _, path := range c.topReadFiles {
		if _, ok := editTargetSet[path]; !ok {
			referenceFiles = append(referenceFiles, path)
		}
	}

	crossCutting := make([]string, 0, len(c.seenWith))
	for _, item := range c.seenWith {
		crossCutting = append(crossCutting, item.File)
	}

	startHere := make([]llmStartFile, 0, 5)
	for _, path := range editTargets {
		startHere = append(startHere, llmStartFile{
			Path: path,
			Role: "frequent edit target",
			Why:  "This file is one of the main change points for the topic.",
		})
		if len(startHere) >= 5 {
			break
		}
	}
	if len(startHere) == 0 {
		for _, path := range referenceFiles {
			startHere = append(startHere, llmStartFile{
				Path: path,
				Role: "reference file",
				Why:  "This file is frequently read while working in the topic.",
			})
			if len(startHere) >= 5 {
				break
			}
		}
	}

	notes := make([]string, 0, 1)
	if c.testTouchSignal == "tests used as spec" {
		notes = append(notes, "Read the associated tests before changing behavior.")
	}

	avoid := make([]string, 0, 4)
	for path, labels := range c.fileClassifications {
		for _, label := range labels {
			if label == "context_tax" {
				avoid = append(avoid, "Do not start with "+path+" unless you need cross-cutting context.")
				break
			}
		}
		if len(avoid) >= 4 {
			break
		}
	}

	workflows := make([]string, 0, 5)
	for _, hint := range c.readBeforeEditHints {
		workflows = append(workflows, hint)
		if len(workflows) >= 5 {
			break
		}
	}

	promptKeywords := extractPromptKeywords(c.prompts, 8)
	whenToUse := make([]string, 0, 3)
	if len(promptKeywords) > 0 {
		whenToUse = append(whenToUse, "The request mentions "+promptKeywords[0]+".")
	}
	if c.subsystem.Name != "" {
		whenToUse = append(whenToUse, "Most edits are in "+c.subsystem.Name+".")
	}
	if c.testTouchSignal != "" && c.testTouchSignal != "no clear test signal" {
		whenToUse = append(whenToUse, "Testing behavior matches "+c.testTouchSignal+".")
	}

	return llmTopicResult{
		Name:           name,
		Summary:        fmt.Sprintf("Recurring work centered on %s and its related files.", c.subsystem.Name),
		WhenToUse:      whenToUse,
		PromptKeywords: promptKeywords,
		Confidence:     fallbackConfidence(c),
		StartHere:      startHere,
		ImportantFiles: llmImportantFiles{
			EditTargets:       editTargets,
			ReferenceFiles:    referenceFiles,
			TestFiles:         append([]string(nil), c.tests...),
			CrossCuttingFiles: crossCutting,
		},
		Tests: llmTests{
			StartWith: append([]string(nil), c.tests...),
			Signal:    c.testTouchSignal,
			Notes:     notes,
			Commands:  observedTestCommands(c.commands),
		},
		KnownWorkflows:   workflows,
		AvoidWastingTime: avoid,
		RiskFlags:        fallbackRiskFlags(c),
	}
}

// observedTestCommands returns test-like commands actually run in sessions -
// the only allowed source for tests.commands.
func observedTestCommands(commands []commandEntry) []string {
	out := []string{}
	for _, cmd := range commands {
		if strings.Contains(cmd.Text, "test") && len(out) < 3 {
			out = append(out, cmd.Text)
		}
	}
	return out
}

func fallbackTopicName(c topicCandidate) string {
	if len(c.prompts) > 0 {
		if name := titleCaseWords(extractPromptKeywords(c.prompts[:1], 4)); name != "" {
			return name
		}
	}
	parts := strings.FieldsFunc(filepath.Base(c.subsystem.Name), func(r rune) bool {
		return r == '_' || r == '-' || r == '/' || r == '.'
	})
	if name := titleCaseWords(parts); name != "" {
		return name + " Workflows"
	}
	return "Repository Topic"
}

func fallbackConfidence(c topicCandidate) float64 {
	confidence := 0.45
	if c.subsystem.Sessions >= 4 {
		confidence = 0.62
	}
	if len(c.prompts) >= 3 && len(c.subsystem.TopFiles) >= 2 {
		confidence += 0.08
	}
	if confidence > 0.78 {
		confidence = 0.78
	}
	return confidence
}

func fallbackRiskFlags(c topicCandidate) []string {
	seen := map[string]struct{}{}
	var flags []string
	for _, label := range c.subsystem.Classification {
		switch label {
		case "low_test_context":
			flags = appendUnique(flags, seen, "low_test_signal")
		case "high_context":
			flags = appendUnique(flags, seen, "high_context_cost")
		case "friction":
			flags = appendUnique(flags, seen, "search_friction")
		}
	}
	if len(c.seenWith) > 0 {
		flags = appendUnique(flags, seen, "cross_subsystem")
	}
	if c.testTouchSignal == "tests used as spec" {
		flags = appendUnique(flags, seen, "tests_as_spec")
	}
	for path, labels := range c.fileClassifications {
		_ = path
		for _, label := range labels {
			switch label {
			case "knowledge_hub":
				flags = appendUnique(flags, seen, "doc_driven")
			case "context_tax":
				flags = appendUnique(flags, seen, "high_context_cost")
			}
		}
	}
	return flags
}

func appendUnique(items []string, seen map[string]struct{}, item string) []string {
	if _, ok := seen[item]; ok {
		return items
	}
	seen[item] = struct{}{}
	return append(items, item)
}

func extractPromptKeywords(prompts []string, limit int) []string {
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "into": {}, "that": {}, "this": {}, "your": {}, "about": {},
		"please": {}, "need": {}, "want": {}, "make": {}, "fix": {},
		"update": {}, "changes": {}, "change": {}, "issue": {}, "error": {}, "agent": {}, "work": {}, "topic": {},
	}
	counts := map[string]int{}
	order := make([]string, 0)
	for _, prompt := range prompts {
		words := strings.Fields(strings.ToLower(strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
				return r
			}
			return ' '
		}, prompt)))
		for _, word := range words {
			if len(word) < 3 {
				continue
			}
			if _, ok := stop[word]; ok {
				continue
			}
			if _, ok := counts[word]; !ok {
				order = append(order, word)
			}
			counts[word]++
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return counts[order[i]] > counts[order[j]]
	})
	if len(order) > limit {
		order = order[:limit]
	}
	return order
}

func titleCaseWords(words []string) string {
	if len(words) == 0 {
		return ""
	}
	if len(words) > 4 {
		words = words[:4]
	}
	titled := make([]string, 0, len(words))
	for _, word := range words {
		if word == "" {
			continue
		}
		titled = append(titled, strings.ToUpper(word[:1])+word[1:])
	}
	return strings.Join(titled, " ")
}

func ioEOF(reason string) error {
	return fmt.Errorf("%s: %w", reason, errors.New("unexpected end of JSON input"))
}

func toID(name string, fallback int) string {
	if name == "" {
		return fmt.Sprintf("topic_%d", fallback+1)
	}
	id := strings.ToLower(name)
	id = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, id)
	for strings.Contains(id, "__") {
		id = strings.ReplaceAll(id, "__", "_")
	}
	return strings.Trim(id, "_")
}

func relationFiles(rel contracts.RepoAnalysisRelation) []string {
	if len(rel.Files) > 0 {
		return rel.Files
	}
	if rel.Source != "" && rel.Target != "" {
		return []string{rel.Source, rel.Target}
	}
	return nil
}

func dedupeStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := ss[:0]
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// ── File-structure helpers ────────────────────────────────────────────────────

var skipDirs = map[string]bool{
	".git": true, ".github": true, "node_modules": true, "vendor": true,
	".next": true, "dist": true, "build": true, "__pycache__": true,
	".cache": true, ".idea": true, ".vscode": true, "target": true,
}

// recentPrompts collects the last n unique user prompts across all sessions.
func recentPrompts(sessions []contracts.RepoAnalysisSession, n int) []string {
	var all []string
	for i := len(sessions) - 1; i >= 0 && len(all) < n; i-- {
		for j := len(sessions[i].Prompts) - 1; j >= 0 && len(all) < n; j-- {
			p := strings.TrimSpace(sessions[i].Prompts[j])
			if p != "" {
				all = append(all, p)
			}
		}
	}
	// reverse so oldest-first
	for l, r := 0, len(all)-1; l < r; l, r = l+1, r-1 {
		all[l], all[r] = all[r], all[l]
	}
	return all
}

// buildRepoFileContext returns a compact string of .md docs + directory tree
// (depth ≤ 3, ≤ 20 files per dir) for inclusion in the topic prompt.
func buildRepoFileContext(repoRoot string, sessionPrompts []string) string {
	if repoRoot == "" {
		return ""
	}
	var sb strings.Builder

	// Recent session prompts
	if len(sessionPrompts) > 0 {
		sb.WriteString("=== Recent Session Prompts ===\n")
		for _, p := range sessionPrompts {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}

	// .md docs from repo root
	for _, name := range []string{"README.md", "CLAUDE.md", "AGENTS.md", "STRUCTURE.md", "CONTRIBUTING.md"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if len(text) > 2000 {
			text = text[:2000] + "…"
		}
		fmt.Fprintf(&sb, "=== %s ===\n%s\n\n", name, text)
	}

	// Directory tree
	tree := buildCompactFileTree(repoRoot)
	if tree != "" {
		sb.WriteString("=== File Structure ===\n")
		sb.WriteString(tree)
	}
	return strings.TrimSpace(sb.String())
}

// buildCompactFileTree walks up to 3 directory levels and lists ≤ 20 files per dir.
func buildCompactFileTree(repoRoot string) string {
	var sb strings.Builder
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			// top-level files
			sb.WriteString(e.Name() + "\n")
			continue
		}
		if skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sb.WriteString(e.Name() + "/\n")
		writeTreeLevel(&sb, filepath.Join(repoRoot, e.Name()), "  ", 1)
	}
	return sb.String()
}

func writeTreeLevel(sb *strings.Builder, dir, indent string, depth int) {
	if depth > 2 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	written := 0
	for _, e := range entries {
		if skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if written >= 20 {
			fmt.Fprintf(sb, "%s…\n", indent)
			break
		}
		if e.IsDir() {
			fmt.Fprintf(sb, "%s%s/\n", indent, e.Name())
			writeTreeLevel(sb, filepath.Join(dir, e.Name()), indent+"  ", depth+1)
		} else {
			fmt.Fprintf(sb, "%s%s\n", indent, e.Name())
		}
		written++
	}
}

// buildFileStructureCandidates creates synthetic topic candidates from the repo
// directory tree when no session-based candidates exist.
func buildFileStructureCandidates(repoRoot string) []topicCandidate {
	if repoRoot == "" {
		return nil
	}
	type dirFiles struct {
		dir   string
		files []string
	}
	var groups []dirFiles

	// Walk up to depth 2 from repo root.
	top, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil
	}
	for _, e := range top {
		if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var files []string
		_ = filepath.WalkDir(filepath.Join(repoRoot, e.Name()), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, path)
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if len(files) > 0 {
			const maxFiles = 30
			if len(files) > maxFiles {
				files = files[:maxFiles]
			}
			groups = append(groups, dirFiles{dir: e.Name(), files: files})
		}
	}

	candidates := make([]topicCandidate, 0, len(groups))
	for _, g := range groups {
		candidates = append(candidates, topicCandidate{
			subsystem: contracts.RepoAnalysisSubsystem{
				Name:     g.dir,
				TopFiles: g.files,
			},
		})
	}
	return candidates
}
