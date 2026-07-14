package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

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
	CandidateID string   `json:"candidate_id"`
	SourceIDs   []string `json:"source_ids"`
	Reason      string   `json:"reason"`
}

type candidateDiscoveryResponse struct {
	Candidates          []candidateGroup `json:"candidates"`
	UnassignedSourceIDs []string         `json:"unassigned_source_ids"`
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
		}
		if source.SourceType != "session" {
			input.ChangedFiles = firstStrings(source.ChangedFiles, 30)
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
	sourcesJSON, _ := json.Marshal(inputs)
	existingJSON, _ := json.Marshal(existingSummaries)
	raw, usage, err := callClaude(ctx, topicModel, prompts.BuildTopicCandidatePrompt(string(sourcesJSON), string(existingJSON)))
	if err != nil {
		return nil, usage, err
	}
	var response candidateDiscoveryResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stripFences(raw))), &response); err != nil {
		return nil, usage, fmt.Errorf("topic candidate discovery: parse response: %w", err)
	}
	groups := normalizeCandidateGroups(response, sources)
	candidates := make([]topicCandidate, 0, len(groups))
	for _, group := range groups {
		if candidate, ok := buildCandidateFromSources(group, sources, bundle); ok {
			candidates = append(candidates, candidate)
		}
	}
	return filterAndRank(candidates), usage, nil
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
		if len(ids) < 2 {
			loose = append(loose, ids...)
			continue
		}
		if strings.TrimSpace(group.CandidateID) == "" {
			group.CandidateID = fmt.Sprintf("candidate_%d", i+1)
		}
		group.SourceIDs = ids
		groups = append(groups, group)
	}
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
			if intersection >= 2 && union > 0 && float64(intersection)/float64(union) >= 0.65 {
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
	if len(group.SourceIDs) < 2 {
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
