package analysis

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
)

const (
	weightCoRead          = 2.0
	weightCoEdit          = 5.0
	weightReadBeforeEdit  = 4.0
	weightSamePromptBlock = 3.0
	weightSameSession     = 0.5

	normalizedThreshold = 1.0
	sameSessionFloor    = 4
	maxClusterSize      = 12
	groupLimit          = 10
)

type pairEvidence struct {
	A, B                                                   string
	CoReadSessions, CoEditSessions, ReadBeforeEditSessions int
	SamePromptBlockSessions, SameSessionSessions           int
	FileASessions, FileBSessions                           int
	WeightedShared, NormalizedStrength                     float64
}

func finalizeRelationshipEdges(
	fileStats map[string]*fileAccum,
	coRead, coEdit, readBeforeEdit, samePromptBlock, sameSession map[string]int,
) []RepoAnalysisRelation {
	evidence := buildPairEvidence(fileStats, coRead, coEdit, readBeforeEdit, samePromptBlock, sameSession)
	out := make([]RepoAnalysisRelation, 0, len(evidence)*2)
	for _, pair := range evidence {
		if !keepPair(pair) {
			continue
		}
		add := func(kind string, sessions, minSessions int, weight float64) {
			if sessions < minSessions {
				return
			}
			out = append(out, RepoAnalysisRelation{
				Type:     kind,
				Files:    []string{pair.A, pair.B},
				Sessions: sessions,
				Strength: round2(wstrength(float64(sessions)*weight, pair.FileASessions, pair.FileBSessions)),
			})
		}
		add("co_read", pair.CoReadSessions, 2, weightCoRead)
		add("co_edit", pair.CoEditSessions, 2, weightCoEdit)
		add("same_prompt_block", pair.SamePromptBlockSessions, 2, weightSamePromptBlock)
		add("same_session", pair.SameSessionSessions, sameSessionFloor, weightSameSession)
		if pair.ReadBeforeEditSessions >= 2 {
			out = append(out, RepoAnalysisRelation{
				Type:     "read_before_edit",
				Source:   pair.A,
				Target:   pair.B,
				Sessions: pair.ReadBeforeEditSessions,
				Strength: round2(wstrength(float64(pair.ReadBeforeEditSessions)*weightReadBeforeEdit, pair.FileASessions, pair.FileBSessions)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Strength != out[j].Strength {
			return out[i].Strength > out[j].Strength
		}
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return relationLabel(out[i]) < relationLabel(out[j])
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func finalizeRelationshipGroups(edges []RepoAnalysisRelation) []RepoAnalysisRelationshipGroup {
	if len(edges) == 0 {
		return nil
	}
	graph := map[string]map[string]struct{}{}
	for _, edge := range edges {
		files := relationFiles(edge)
		if len(files) != 2 {
			continue
		}
		a, b := files[0], files[1]
		if graph[a] == nil {
			graph[a] = map[string]struct{}{}
		}
		if graph[b] == nil {
			graph[b] = map[string]struct{}{}
		}
		graph[a][b] = struct{}{}
		graph[b][a] = struct{}{}
	}
	seen := map[string]struct{}{}
	groups := make([]RepoAnalysisRelationshipGroup, 0)
	for start := range graph {
		if _, ok := seen[start]; ok {
			continue
		}
		componentFiles := walkComponent(graph, start, seen)
		componentEdges := edgesForFiles(edges, componentFiles)
		groups = append(groups, splitComponent(componentFiles, componentEdges)...)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Strength != groups[j].Strength {
			return groups[i].Strength > groups[j].Strength
		}
		if len(groups[i].Files) != len(groups[j].Files) {
			return len(groups[i].Files) > len(groups[j].Files)
		}
		return groups[i].Name < groups[j].Name
	})
	return selectDiverseGroups(groups, groupLimit)
}

func buildPairEvidence(fileStats map[string]*fileAccum, coRead, coEdit, readBeforeEdit, samePromptBlock, sameSession map[string]int) []pairEvidence {
	keys := map[string]struct{}{}
	for k := range coRead {
		keys[k] = struct{}{}
	}
	for k := range coEdit {
		keys[k] = struct{}{}
	}
	for k := range readBeforeEdit {
		keys[k] = struct{}{}
	}
	for k := range samePromptBlock {
		keys[k] = struct{}{}
	}
	for k := range sameSession {
		keys[k] = struct{}{}
	}
	out := make([]pairEvidence, 0, len(keys))
	for key := range keys {
		a, b := splitPairKey(key)
		if !clusterEligible(a) || !clusterEligible(b) {
			continue
		}
		left, right := fileStats[a], fileStats[b]
		if left == nil || right == nil {
			continue
		}
		pair := pairEvidence{
			A:                       a,
			B:                       b,
			CoReadSessions:          coRead[key],
			CoEditSessions:          coEdit[key],
			ReadBeforeEditSessions:  readBeforeEdit[key],
			SamePromptBlockSessions: samePromptBlock[key],
			SameSessionSessions:     sameSession[key],
			FileASessions:           len(left.sessionSet),
			FileBSessions:           len(right.sessionSet),
		}
		pair.WeightedShared = float64(pair.CoReadSessions)*weightCoRead +
			float64(pair.CoEditSessions)*weightCoEdit +
			float64(pair.ReadBeforeEditSessions)*weightReadBeforeEdit +
			float64(pair.SamePromptBlockSessions)*weightSamePromptBlock +
			float64(pair.SameSessionSessions)*weightSameSession
		pair.NormalizedStrength = wstrength(pair.WeightedShared, pair.FileASessions, pair.FileBSessions)
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NormalizedStrength != out[j].NormalizedStrength {
			return out[i].NormalizedStrength > out[j].NormalizedStrength
		}
		return pairKey(out[i].A, out[i].B) < pairKey(out[j].A, out[j].B)
	})
	return out
}

func keepPair(pair pairEvidence) bool {
	if pair.CoEditSessions >= 2 || pair.ReadBeforeEditSessions >= 2 || pair.SamePromptBlockSessions >= 3 {
		return true
	}
	if pair.CoEditSessions == 0 && pair.ReadBeforeEditSessions == 0 && pair.SamePromptBlockSessions == 0 && pair.CoReadSessions == 0 {
		return pair.SameSessionSessions >= sameSessionFloor
	}
	return pair.NormalizedStrength >= normalizedThreshold
}

func wstrength(weighted float64, sessA, sessB int) float64 {
	return safeDivide(weighted, math.Sqrt(float64(maxInt(sessA, 1)*maxInt(sessB, 1))))
}

func clusterEligible(path string) bool {
	switch relationshipPathKind(path) {
	case "file", "package_manifest", "generated", "config", "unknown":
		return true
	default:
		return false
	}
}

func relationshipPathKind(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return "unknown"
	}
	if path == "." {
		return "repo_root"
	}
	base := filepath.Base(path)
	if isInfoLikeGenerated(path) {
		return "generated"
	}
	if isManifestFile(base) {
		return "package_manifest"
	}
	if looksLikeDir(path) {
		return "directory"
	}
	if fileKind(path) == "config" {
		return "config"
	}
	if fileKind(path) != "other" {
		return "file"
	}
	return "unknown"
}

func isInfoLikeGenerated(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "/dist/") || strings.Contains(lower, "/build/") ||
		strings.Contains(lower, "/coverage/") || strings.Contains(lower, "/gen/") ||
		strings.Contains(lower, "/generated/")
}

func isManifestFile(base string) bool {
	switch strings.ToLower(base) {
	case "package.json", "package-lock.json", "go.mod", "go.sum", "cargo.toml", "cargo.lock",
		"pyproject.toml", "pom.xml", "vite.config.js", "dockerfile", "makefile":
		return true
	default:
		return false
	}
}

func looksLikeDir(path string) bool {
	base := filepath.Base(path)
	if base == "." || base == "/" {
		return true
	}
	if strings.Contains(base, ".") {
		return false
	}
	if isManifestFile(base) {
		return false
	}
	switch base {
	case "Dockerfile", "Makefile":
		return false
	default:
		return true
	}
}

func walkComponent(graph map[string]map[string]struct{}, start string, seen map[string]struct{}) []string {
	queue := []string{start}
	seen[start] = struct{}{}
	files := make([]string, 0)
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		files = append(files, node)
		neighbors := make([]string, 0, len(graph[node]))
		for next := range graph[node] {
			neighbors = append(neighbors, next)
		}
		sort.Strings(neighbors)
		for _, next := range neighbors {
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	sort.Strings(files)
	return files
}

func edgesForFiles(edges []RepoAnalysisRelation, files []string) []RepoAnalysisRelation {
	fileSet := map[string]struct{}{}
	for _, f := range files {
		fileSet[f] = struct{}{}
	}
	out := make([]RepoAnalysisRelation, 0, len(edges))
	for _, edge := range edges {
		pair := relationFiles(edge)
		if len(pair) != 2 {
			continue
		}
		if _, ok := fileSet[pair[0]]; !ok {
			continue
		}
		if _, ok := fileSet[pair[1]]; !ok {
			continue
		}
		out = append(out, edge)
	}
	return out
}

func splitComponent(files []string, edges []RepoAnalysisRelation) []RepoAnalysisRelationshipGroup {
	if len(files) <= maxClusterSize {
		if g := buildGroup(files, edges); g != nil {
			return []RepoAnalysisRelationshipGroup{*g}
		}
		return nil
	}
	bySubsystem := map[string][]string{}
	for _, f := range files {
		sub := subsystemName(f)
		bySubsystem[sub] = append(bySubsystem[sub], f)
	}
	subsystems := make([]string, 0, len(bySubsystem))
	for sub := range bySubsystem {
		subsystems = append(subsystems, sub)
	}
	sort.Strings(subsystems)
	out := make([]RepoAnalysisRelationshipGroup, 0, len(subsystems))
	for _, sub := range subsystems {
		subFiles := bySubsystem[sub]
		if len(subFiles) < 2 {
			continue
		}
		subEdges := edgesForFiles(edges, subFiles)
		if g := buildGroup(subFiles, subEdges); g != nil {
			out = append(out, *g)
		}
	}
	if len(out) > 0 {
		return out
	}
	if g := buildGroup(files, edges); g != nil {
		return []RepoAnalysisRelationshipGroup{*g}
	}
	return nil
}

func buildGroup(files []string, edges []RepoAnalysisRelation) *RepoAnalysisRelationshipGroup {
	if len(files) < 2 || len(edges) == 0 {
		return nil
	}
	typeSet := map[string]struct{}{}
	for _, e := range edges {
		typeSet[e.Type] = struct{}{}
	}
	return &RepoAnalysisRelationshipGroup{
		Name:      groupName(files),
		Files:     append([]string{}, files...),
		Types:     sortedKeys(typeSet),
		Strength:  groupStrength(files, edges),
		EdgeCount: len(edges),
	}
}

func selectDiverseGroups(groups []RepoAnalysisRelationshipGroup, limit int) []RepoAnalysisRelationshipGroup {
	if len(groups) <= limit {
		return groups
	}
	selected := make([]RepoAnalysisRelationshipGroup, 0, limit)
	usedFiles := map[string]int{}
	remaining := append([]RepoAnalysisRelationshipGroup(nil), groups...)
	for len(selected) < limit && len(remaining) > 0 {
		bestIdx, bestScore := 0, -1.0
		for i, group := range remaining {
			novel := 0
			for _, f := range group.Files {
				if usedFiles[f] == 0 {
					novel++
				}
			}
			score := float64(novel)*1000 + group.Strength
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		selected = append(selected, remaining[bestIdx])
		for _, f := range remaining[bestIdx].Files {
			usedFiles[f]++
		}
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

func groupStrength(files []string, edges []RepoAnalysisRelation) float64 {
	fileSet := map[string]struct{}{}
	for _, f := range files {
		fileSet[f] = struct{}{}
	}
	strength := 0.0
	for _, edge := range edges {
		pair := relationFiles(edge)
		if len(pair) != 2 {
			continue
		}
		if _, ok := fileSet[pair[0]]; !ok {
			continue
		}
		if _, ok := fileSet[pair[1]]; !ok {
			continue
		}
		if edge.Strength > 0 {
			strength += edge.Strength
		} else {
			strength += float64(edge.Sessions)
		}
	}
	return round2(strength)
}

func groupName(files []string) string {
	if len(files) == 0 {
		return ""
	}
	if len(files) == 1 {
		return files[0]
	}
	if len(files) == 2 {
		return files[0] + " + " + files[1]
	}
	if prefix := dominantPrefix(files); prefix != "" && prefix != "." {
		return prefix + " cluster"
	}
	return files[0] + " cluster"
}

func dominantPrefix(files []string) string {
	counts := map[string]int{}
	for _, f := range files {
		sub := subsystemName(f)
		if sub != "." {
			counts[sub]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	type ps struct {
		prefix string
		count  int
		depth  int
	}
	scores := make([]ps, 0, len(counts))
	for prefix, count := range counts {
		scores = append(scores, ps{prefix, count, strings.Count(prefix, "/")})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].count != scores[j].count {
			return scores[i].count > scores[j].count
		}
		if scores[i].depth != scores[j].depth {
			return scores[i].depth > scores[j].depth
		}
		return scores[i].prefix < scores[j].prefix
	})
	return scores[0].prefix
}
