package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

const maxCandidateSourcesPerCall = 50

type candidateSourceInput struct {
	ID           string   `json:"id"`
	SourceType   string   `json:"source_type"`
	AuthorID     string   `json:"author_id,omitempty"`
	Title        string   `json:"title,omitempty"`
	Prompts      []string `json:"prompts,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Timestamp    string   `json:"timestamp,omitempty"`
}

type candidateGroup struct {
	CandidateID     string   `json:"candidate_id"`
	ExistingTopicID string   `json:"existing_topic_id,omitempty"`
	SourceIDs       []string `json:"source_ids"`
	Reason          string   `json:"reason"`
}

type candidateAssignment struct {
	TopicID   string   `json:"topic_id"`
	SourceIDs []string `json:"source_ids"`
}

type candidateSplit struct {
	TopicID string     `json:"topic_id"`
	Groups  [][]string `json:"groups"`
}

type candidateDiscoveryResponse struct {
	Candidates          []candidateGroup      `json:"candidates"`
	UnassignedSourceIDs []string              `json:"unassigned_source_ids"`
	Groups              [][]string            `json:"groups"`
	Assignments         []candidateAssignment `json:"assign"`
	NewGroups           [][]string            `json:"new"`
	Splits              []candidateSplit      `json:"split"`
	Unassigned          []string              `json:"unassigned"`
}

type candidateConsolidationInput struct {
	CandidateID           string   `json:"candidate_id"`
	ExistingTopicID       string   `json:"existing_topic_id,omitempty"`
	SourceCount           int      `json:"source_count"`
	RepresentativePrompts []string `json:"representative_prompts,omitempty"`
	TopEditedFiles        []string `json:"top_edited_files,omitempty"`
}

func discoverTopicCandidates(ctx context.Context, bundle contracts.RepoAnalysisBundle, existing []model.TopicContext) ([]topicCandidate, Usage, error) {
	sources := candidateSources(bundle)
	if len(sources) < 2 {
		return nil, Usage{}, nil
	}
	inputs := make([]candidateSourceInput, 0, len(sources))
	sourceIDs := make([]string, 0, len(sources))
	for id := range sources {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Slice(sourceIDs, func(i, j int) bool {
		left, right := sources[sourceIDs[i]], sources[sourceIDs[j]]
		if left.Timestamp != right.Timestamp {
			return left.Timestamp > right.Timestamp
		}
		return left.ID < right.ID
	})
	for _, id := range sourceIDs {
		source := sources[id]
		input := candidateSourceInput{
			ID: source.ID, SourceType: source.SourceType, AuthorID: source.AuthorID,
			Title: source.Title, Prompts: firstStrings(source.Prompts, promptsPerSession), Timestamp: source.Timestamp,
			ChangedFiles: firstStrings(source.ChangedFiles, 20),
		}
		inputs = append(inputs, input)
	}
	existingSummaries := make([]map[string]any, 0, len(existing))
	for _, topic := range existing {
		existingSummaries = append(existingSummaries, map[string]any{
			"id": topic.ID, "name": topic.Name, "summary": topic.Summary,
			"edit_targets": topic.ImportantFiles.EditTargets,
		})
	}
	batches := candidateInputBatches(inputs, maxCandidateSourcesPerCall)
	var groups []candidateGroup
	var loose []string
	var usage Usage
	for index, batch := range batches {
		turnExisting := append([]map[string]any(nil), existingSummaries...)
		turnExisting = append(turnExisting, candidateScopeSummaries(groups, sources)...)
		existingJSON, _ := json.Marshal(turnExisting)
		sourcesJSON, _ := json.Marshal(batch)
		started := time.Now()
		slog.Info("topic candidate turn started", "batch", index+1, "batches", len(batches), "sources", len(batch), "existing_topics", len(turnExisting))
		raw, batchUsage, err := callClaudeMaxTokens(ctx, topicModel, prompts.BuildTopicCandidatePrompt(string(sourcesJSON), string(existingJSON)), 8192)
		usage = mergeUsage(usage, batchUsage)
		if err != nil {
			slog.Error("topic candidate turn failed", "batch", index+1, "batches", len(batches), "sources", len(batch), "duration_ms", time.Since(started).Milliseconds(), "err", err)
			return nil, usage, err
		}
		response, parseErr := parseCandidateDiscoveryResponse(raw)
		if parseErr != nil {
			return nil, usage, fmt.Errorf("topic candidate discovery batch %d/%d: parse response (%d chars, %d output tokens): %w", index+1, len(batches), len(raw), batchUsage.OutputTokens, parseErr)
		}
		groups, loose = applyCandidateTurn(index, batch, response, groups, loose, sources)
		slog.Info("topic candidate turn completed", "batch", index+1, "batches", len(batches), "sources", len(batch), "candidate_scopes", len(groups), "unassigned_total", len(loose), "input_tokens", batchUsage.InputTokens, "output_tokens", batchUsage.OutputTokens, "duration_ms", time.Since(started).Milliseconds())
	}
	groups = normalizeCandidateGroups(candidateDiscoveryResponse{Candidates: groups, UnassignedSourceIDs: loose}, sources)
	candidates := make([]topicCandidate, 0, len(groups))
	for _, group := range groups {
		if candidate, ok := buildCandidateFromSources(group, sources, bundle); ok {
			candidates = append(candidates, candidate)
		}
	}
	return filterAndRank(candidates), usage, nil
}

func consolidateCandidateGroups(ctx context.Context, groups []candidateGroup, sources map[string]contracts.RepoAnalysisSource) ([]candidateGroup, Usage, error) {
	if len(groups) < 2 {
		return groups, Usage{}, nil
	}
	inputs := make([]candidateConsolidationInput, 0, len(groups))
	for _, group := range groups {
		promptSet := map[string]struct{}{}
		fileCounts := map[string]int{}
		var representativePrompts []string
		for _, sourceID := range group.SourceIDs {
			source := sources[sourceID]
			for _, prompt := range firstStrings(source.Prompts, promptsPerSession) {
				prompt = strings.TrimSpace(prompt)
				if prompt == "" {
					continue
				}
				if _, ok := promptSet[prompt]; !ok && len(representativePrompts) < 8 {
					promptSet[prompt] = struct{}{}
					representativePrompts = append(representativePrompts, prompt)
				}
			}
			for _, path := range dedupeStrings(source.ChangedFiles) {
				fileCounts[path]++
			}
		}
		inputs = append(inputs, candidateConsolidationInput{CandidateID: group.CandidateID, ExistingTopicID: group.ExistingTopicID, SourceCount: len(group.SourceIDs), RepresentativePrompts: representativePrompts, TopEditedFiles: rankedPaths(fileCounts, 12)})
	}
	payload, _ := json.Marshal(inputs)
	started := time.Now()
	slog.Info("topic candidate consolidation turn started", "candidates", len(groups))
	raw, usage, err := callClaudeMaxTokens(ctx, topicModel, prompts.BuildTopicCandidateConsolidationPrompt(string(payload)), 4096)
	if err != nil {
		slog.Error("topic candidate consolidation turn failed", "candidates", len(groups), "duration_ms", time.Since(started).Milliseconds(), "err", err)
		return nil, usage, err
	}
	response, parseErr := parseCandidateDiscoveryResponse(raw)
	if parseErr != nil {
		return nil, usage, fmt.Errorf("topic candidate consolidation: parse response: %w", parseErr)
	}
	byID := make(map[string]candidateGroup, len(groups))
	for _, group := range groups {
		byID[group.CandidateID] = group
	}
	used := map[string]struct{}{}
	consolidated := make([]candidateGroup, 0, len(groups))
	for _, mergeIDs := range response.Groups {
		var merged candidateGroup
		for _, id := range mergeIDs {
			group, ok := byID[id]
			if !ok {
				continue
			}
			if _, ok := used[id]; ok {
				continue
			}
			used[id] = struct{}{}
			if merged.CandidateID == "" {
				merged = group
			} else {
				merged.SourceIDs = dedupeStrings(append(merged.SourceIDs, group.SourceIDs...))
				if merged.ExistingTopicID == "" {
					merged.ExistingTopicID = group.ExistingTopicID
				}
			}
		}
		if merged.CandidateID != "" {
			consolidated = append(consolidated, merged)
		}
	}
	for _, group := range groups {
		if _, ok := used[group.CandidateID]; !ok {
			consolidated = append(consolidated, group)
		}
	}
	slog.Info("topic candidate consolidation turn completed", "candidates_before", len(groups), "candidates_after", len(consolidated), "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "duration_ms", time.Since(started).Milliseconds())
	return consolidated, usage, nil
}

func candidateInputBatches(inputs []candidateSourceInput, limit int) [][]candidateSourceInput {
	if len(inputs) == 0 || limit <= 0 {
		return nil
	}
	batches := make([][]candidateSourceInput, 0, (len(inputs)+limit-1)/limit)
	for start := 0; start < len(inputs); start += limit {
		end := start + limit
		if end > len(inputs) {
			end = len(inputs)
		}
		batches = append(batches, inputs[start:end])
	}
	return batches
}

func candidateScopeSummaries(groups []candidateGroup, sources map[string]contracts.RepoAnalysisSource) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		fileCounts := map[string]int{}
		var representativePrompts []string
		for _, sourceID := range group.SourceIDs {
			source := sources[sourceID]
			if len(representativePrompts) < 6 {
				representativePrompts = append(representativePrompts, firstStrings(source.Prompts, min(promptsPerSession, 6-len(representativePrompts)))...)
			}
			for _, path := range dedupeStrings(source.ChangedFiles) {
				fileCounts[path]++
			}
		}
		out = append(out, map[string]any{"id": group.CandidateID, "candidate": true, "source_count": len(group.SourceIDs), "representative_prompts": representativePrompts, "edit_targets": rankedPaths(fileCounts, 12)})
	}
	return out
}

func applyCandidateTurn(batchIndex int, batch []candidateSourceInput, response candidateDiscoveryResponse, groups []candidateGroup, loose []string, sources map[string]contracts.RepoAnalysisSource) ([]candidateGroup, []string) {
	batchIDs := map[string]struct{}{}
	for _, source := range batch {
		batchIDs[source.ID] = struct{}{}
	}
	used := map[string]struct{}{}
	validIDs := func(ids []string) []string {
		var out []string
		for _, id := range ids {
			if _, ok := batchIDs[id]; !ok {
				continue
			}
			if _, ok := used[id]; ok {
				continue
			}
			used[id] = struct{}{}
			out = append(out, id)
		}
		return out
	}
	groupIndex := map[string]int{}
	for index, group := range groups {
		groupIndex[group.CandidateID] = index
		if group.ExistingTopicID != "" {
			groupIndex[group.ExistingTopicID] = index
		}
	}
	for _, assignment := range response.Assignments {
		ids := validIDs(assignment.SourceIDs)
		if len(ids) == 0 {
			continue
		}
		if index, ok := groupIndex[assignment.TopicID]; ok {
			groups[index].SourceIDs = dedupeStrings(append(groups[index].SourceIDs, ids...))
			continue
		}
		groups = append(groups, candidateGroup{CandidateID: "existing_" + assignment.TopicID, ExistingTopicID: assignment.TopicID, SourceIDs: ids})
		groupIndex[assignment.TopicID] = len(groups) - 1
	}
	newGroups := append([][]string(nil), response.NewGroups...)
	newGroups = append(newGroups, response.Groups...)
	for groupNumber, ids := range newGroups {
		ids = validIDs(ids)
		if len(ids) >= 2 {
			groups = append(groups, candidateGroup{CandidateID: fmt.Sprintf("candidate_%d_%d", batchIndex+1, groupNumber+1), SourceIDs: ids})
		} else {
			loose = append(loose, ids...)
		}
	}
	for splitNumber, split := range response.Splits {
		for groupNumber, ids := range split.Groups {
			ids = validIDs(ids)
			if len(ids) >= 2 {
				groups = append(groups, candidateGroup{CandidateID: fmt.Sprintf("split_%d_%d_%d", batchIndex+1, splitNumber+1, groupNumber+1), SourceIDs: ids})
			} else {
				loose = append(loose, ids...)
			}
		}
	}
	loose = append(loose, response.UnassignedSourceIDs...)
	for id := range batchIDs {
		if _, ok := used[id]; !ok {
			loose = append(loose, id)
		}
	}
	return groups, dedupeStrings(loose)
}

func parseCandidateDiscoveryResponse(raw string) (candidateDiscoveryResponse, error) {
	clean := strings.TrimSpace(extractJSON(raw))
	var response candidateDiscoveryResponse
	if err := json.Unmarshal([]byte(clean), &response); err != nil {
		response.Groups = recoverCompactCandidateGroups(clean)
		if len(response.Groups) == 0 {
			return candidateDiscoveryResponse{}, err
		}
	}
	for i, ids := range response.Groups {
		response.Candidates = append(response.Candidates, candidateGroup{CandidateID: fmt.Sprintf("candidate_%d", i+1), SourceIDs: ids})
	}
	for i, ids := range response.NewGroups {
		response.Candidates = append(response.Candidates, candidateGroup{CandidateID: fmt.Sprintf("new_%d", i+1), SourceIDs: ids})
	}
	for _, assignment := range response.Assignments {
		response.Candidates = append(response.Candidates, candidateGroup{CandidateID: "existing_" + assignment.TopicID, ExistingTopicID: assignment.TopicID, SourceIDs: assignment.SourceIDs})
	}
	for splitIndex, split := range response.Splits {
		for groupIndex, ids := range split.Groups {
			response.Candidates = append(response.Candidates, candidateGroup{CandidateID: fmt.Sprintf("split_%s_%d_%d", split.TopicID, splitIndex+1, groupIndex+1), SourceIDs: ids})
		}
	}
	response.UnassignedSourceIDs = append(response.UnassignedSourceIDs, response.Unassigned...)
	return response, nil
}

func recoverCompactCandidateGroups(raw string) [][]string {
	key := strings.Index(raw, `"groups"`)
	if key < 0 {
		return nil
	}
	outer := strings.Index(raw[key:], "[")
	if outer < 0 {
		return nil
	}
	start := key + outer
	depth, groupStart := 0, -1
	inString, escaped := false, false
	var groups [][]string
	for i := start; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		switch ch {
		case '[':
			depth++
			if depth == 2 {
				groupStart = i
			}
		case ']':
			if depth == 2 && groupStart >= 0 {
				var ids []string
				if json.Unmarshal([]byte(raw[groupStart:i+1]), &ids) == nil {
					groups = append(groups, ids)
				}
				groupStart = -1
			}
			depth--
			if depth <= 0 {
				return groups
			}
		}
	}
	return groups
}

func candidateSources(bundle contracts.RepoAnalysisBundle) map[string]contracts.RepoAnalysisSource {
	out := make(map[string]contracts.RepoAnalysisSource)
	for _, source := range bundle.Sources {
		if source.ID != "" {
			out[source.ID] = source
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, session := range bundle.Sessions {
		out[session.ID] = contracts.RepoAnalysisSource{
			ID: session.ID, SourceType: "session", AuthorID: session.AuthorID, Title: session.Title,
			Prompts: session.Prompts, ReadFiles: session.ReadFiles, ChangedFiles: session.EditedFiles,
			Timestamp: session.Timestamp,
		}
	}
	return out
}

func normalizeCandidateGroups(response candidateDiscoveryResponse, sources map[string]contracts.RepoAnalysisSource) []candidateGroup {
	used := map[string]struct{}{}
	groups := make([]candidateGroup, 0, len(response.Candidates))
	loose := append([]string(nil), response.UnassignedSourceIDs...)
	for i, group := range response.Candidates {
		seen := map[string]struct{}{}
		ids := make([]string, 0, len(group.SourceIDs))
		for _, id := range group.SourceIDs {
			if _, ok := sources[id]; !ok {
				continue
			}
			if _, ok := used[id]; ok {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			used[id] = struct{}{}
			ids = append(ids, id)
		}
		if len(ids) < 2 && group.ExistingTopicID == "" {
			loose = append(loose, ids...)
			continue
		}
		if strings.TrimSpace(group.CandidateID) == "" {
			group.CandidateID = fmt.Sprintf("candidate_%d", i+1)
		}
		group.SourceIDs = ids
		groups = append(groups, group)
	}
	groups = mergeExistingCandidateGroups(groups)
	for id := range sources {
		if _, ok := used[id]; !ok {
			loose = append(loose, id)
		}
	}
	loose = dedupeStrings(loose)
	for _, id := range loose {
		source, ok := sources[id]
		if !ok {
			continue
		}
		best, bestOverlap := -1, 0
		for i := range groups {
			overlap := sourceFileOverlap(source, groups[i], sources)
			if overlap > bestOverlap {
				best, bestOverlap = i, overlap
			}
		}
		if best >= 0 && bestOverlap > 0 {
			groups[best].SourceIDs = append(groups[best].SourceIDs, id)
		}
	}
	groups = mergeOverlappingCandidateGroups(groups, sources)
	seenCandidateIDs := map[string]int{}
	for i := range groups {
		base := toID(groups[i].CandidateID, i)
		seenCandidateIDs[base]++
		if seenCandidateIDs[base] > 1 {
			base = fmt.Sprintf("%s_%d", base, seenCandidateIDs[base])
		}
		groups[i].CandidateID = base
	}
	return groups
}

func mergeExistingCandidateGroups(groups []candidateGroup) []candidateGroup {
	indexes := map[string]int{}
	out := make([]candidateGroup, 0, len(groups))
	for _, group := range groups {
		if group.ExistingTopicID == "" {
			out = append(out, group)
			continue
		}
		if index, ok := indexes[group.ExistingTopicID]; ok {
			out[index].SourceIDs = dedupeStrings(append(out[index].SourceIDs, group.SourceIDs...))
			continue
		}
		indexes[group.ExistingTopicID] = len(out)
		out = append(out, group)
	}
	return out
}

func mergeOverlappingCandidateGroups(groups []candidateGroup, sources map[string]contracts.RepoAnalysisSource) []candidateGroup {
	for i := 0; i < len(groups); i++ {
		left := candidateChangedFileSet(groups[i], sources)
		for j := i + 1; j < len(groups); {
			right := candidateChangedFileSet(groups[j], sources)
			intersection := 0
			for path := range left {
				if _, ok := right[path]; ok {
					intersection++
				}
			}
			union := len(left) + len(right) - intersection
			minSize := min(len(left), len(right))
			jaccard := 0.0
			containment := 0.0
			if union > 0 {
				jaccard = float64(intersection) / float64(union)
			}
			if minSize > 0 {
				containment = float64(intersection) / float64(minSize)
			}
			if intersection >= 2 && (jaccard >= 0.35 || containment >= 0.5) {
				groups[i].SourceIDs = dedupeStrings(append(groups[i].SourceIDs, groups[j].SourceIDs...))
				groups = append(groups[:j], groups[j+1:]...)
				left = candidateChangedFileSet(groups[i], sources)
				continue
			}
			j++
		}
	}
	return groups
}

func candidateChangedFileSet(group candidateGroup, sources map[string]contracts.RepoAnalysisSource) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range group.SourceIDs {
		for _, path := range sources[id].ChangedFiles {
			if strings.TrimSpace(path) != "" {
				out[path] = struct{}{}
			}
		}
	}
	return out
}

func sourceFileOverlap(source contracts.RepoAnalysisSource, group candidateGroup, sources map[string]contracts.RepoAnalysisSource) int {
	files := make(map[string]struct{}, len(source.ChangedFiles))
	for _, path := range source.ChangedFiles {
		files[path] = struct{}{}
	}
	overlap := 0
	for _, id := range group.SourceIDs {
		for _, path := range sources[id].ChangedFiles {
			if _, ok := files[path]; ok {
				overlap++
			}
		}
	}
	return overlap
}

func buildCandidateFromSources(group candidateGroup, sources map[string]contracts.RepoAnalysisSource, bundle contracts.RepoAnalysisBundle) (topicCandidate, bool) {
	if len(group.SourceIDs) < 2 && group.ExistingTopicID == "" {
		return topicCandidate{}, false
	}
	fileInfo := make(map[string]contracts.RepoAnalysisFile, len(bundle.Files))
	for _, file := range bundle.Files {
		fileInfo[file.Path] = file
	}
	editCounts, readCounts := map[string]int{}, map[string]int{}
	authors := map[string]struct{}{}
	seenPrompts := map[string]struct{}{}
	var candidate topicCandidate
	candidate.candidateID = group.CandidateID
	candidate.existingTopicID = group.ExistingTopicID
	candidate.sourceIDs = append([]string(nil), group.SourceIDs...)
	candidate.fileClassifications = map[string][]string{}
	commandSessions := map[string][]contracts.RepoAnalysisCommand{}
	refs := make([]contracts.RepoAnalysisSessionRef, 0, len(group.SourceIDs))
	for _, session := range bundle.Sessions {
		commandSessions[session.ID] = session.Commands
	}
	for _, id := range group.SourceIDs {
		source := sources[id]
		if source.AuthorID != "" {
			authors[source.AuthorID] = struct{}{}
		}
		for _, prompt := range firstStrings(source.Prompts, promptsPerSession) {
			if _, ok := seenPrompts[prompt]; !ok && strings.TrimSpace(prompt) != "" {
				seenPrompts[prompt] = struct{}{}
				candidate.prompts = append(candidate.prompts, prompt)
			}
		}
		for _, path := range dedupeStrings(source.ChangedFiles) {
			editCounts[path]++
		}
		for _, path := range dedupeStrings(source.ReadFiles) {
			readCounts[path]++
		}
		if source.Timestamp > candidate.lastActive {
			candidate.lastActive = source.Timestamp
		}
		refs = append(refs, contracts.RepoAnalysisSessionRef{ID: id, Name: source.Title, Agent: source.SourceType, Timestamp: source.Timestamp})
	}
	if len(candidate.lastActive) >= 10 {
		candidate.lastActive = candidate.lastActive[:10]
	}
	candidate.independentAuthors = len(authors)
	for path, count := range editCounts {
		if count >= 2 {
			candidate.repeatedEditedFiles = append(candidate.repeatedEditedFiles, path)
		}
	}
	sort.Strings(candidate.repeatedEditedFiles)
	maxRepeated := 0
	for _, path := range candidate.repeatedEditedFiles {
		if editCounts[path] > maxRepeated {
			maxRepeated = editCounts[path]
		}
	}
	switch {
	case len(group.SourceIDs) >= 4 && maxRepeated >= 3:
		candidate.supportLevel = "strong"
	case len(candidate.repeatedEditedFiles) > 0:
		candidate.supportLevel = "supported"
	default:
		candidate.supportLevel = "weak"
	}

	edited := rankedPaths(editCounts, 12)
	read := rankedPaths(readCounts, 8)
	allPaths := dedupeStrings(append(append([]string(nil), edited...), read...))
	directory := dominantDirectory(edited)
	if directory == "" {
		directory = group.CandidateID
	}
	sub := contracts.RepoAnalysisSubsystem{Name: directory, Paths: allPaths, Sessions: len(group.SourceIDs), RelatedSessions: refs}
	for _, path := range edited {
		info := fileInfo[path]
		if info.Kind == "test" {
			sub.TestEdits += editCounts[path]
			candidate.tests = append(candidate.tests, path)
		} else {
			sub.SourceEdits += editCounts[path]
		}
		if len(info.Classification) > 0 {
			candidate.fileClassifications[path] = info.Classification
		}
	}
	for _, path := range read {
		info := fileInfo[path]
		if info.Kind == "test" {
			sub.TestReads += readCounts[path]
			candidate.tests = append(candidate.tests, path)
		} else {
			sub.SourceReads += readCounts[path]
			candidate.topReadFiles = append(candidate.topReadFiles, path)
		}
		if len(info.Classification) > 0 {
			candidate.fileClassifications[path] = info.Classification
		}
	}
	sub.Edits = sub.SourceEdits + sub.TestEdits
	sub.Reads = sub.SourceReads + sub.TestReads
	sub.TopFiles = edited
	candidate.subsystem = sub
	candidate.readFiles = len(readCounts)
	candidate.commands = aggregateCommands(refs, commandSessions)
	candidate.testTouchSignal = deriveTestSignal(sub)
	return candidate, true
}

func rankedPaths(counts map[string]int, limit int) []string {
	paths := make([]string, 0, len(counts))
	for path := range counts {
		if strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		if counts[paths[i]] != counts[paths[j]] {
			return counts[paths[i]] > counts[paths[j]]
		}
		return paths[i] < paths[j]
	})
	if len(paths) > limit {
		paths = paths[:limit]
	}
	return paths
}

func dominantDirectory(paths []string) string {
	counts := map[string]int{}
	best := ""
	for _, path := range paths {
		dir := filepath.ToSlash(filepath.Dir(path))
		if dir == "." {
			dir = ""
		}
		counts[dir]++
		if counts[dir] > counts[best] || counts[dir] == counts[best] && dir < best {
			best = dir
		}
	}
	return best
}

func firstStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func mergeUsage(a, b Usage) Usage {
	modelName := b.Model
	if modelName == "" {
		modelName = a.Model
	}
	return Usage{Model: modelName, InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens}
}
