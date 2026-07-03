package analysis

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/repoguide/repoguide-core/model"
)

// BuildRepoAnalysis builds a RepoAnalysisBundle from all stored session events for a repo.
func BuildRepoAnalysis(repoRoot string, stored []model.RepoSessionEvents) (RepoAnalysisBundle, error) {
	if len(stored) == 0 {
		return emptyBundle(repoRoot), nil
	}

	sessions := make([]backendSession, 0, len(stored))
	for _, s := range stored {
		m := analyzeSessionEvents(s.Events)
		// estimate cost using the agent as a proxy for model name
		m.EstimatedCostUSD = estimateCost(s.Agent, m.TokenUsage)
		sessions = append(sessions, backendSession{
			id:        s.ID,
			agent:     s.Agent,
			name:      s.Name,
			timestamp: sessionTimestamp(s.Events, s.UpdatedAt),
			metrics:   m,
			events:    s.Events,
		})
	}

	return buildBundle(repoRoot, sessions), nil
}

func emptyBundle(repoRoot string) RepoAnalysisBundle {
	return RepoAnalysisBundle{
		Version: bundleVersion,
		Repo: RepoAnalysisRepo{
			Name:        filepath.Base(strings.TrimRight(repoRoot, string(filepath.Separator))),
			Root:        repoRoot,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func buildBundle(repoRoot string, sessions []backendSession) RepoAnalysisBundle {
	generatedAt := time.Now().UTC()
	var earliest time.Time
	for _, s := range sessions {
		if earliest.IsZero() || s.timestamp.Before(earliest) {
			earliest = s.timestamp
		}
	}
	rangeDays := 0
	if !earliest.IsZero() {
		rangeDays = int(generatedAt.Sub(earliest).Hours() / 24)
		if rangeDays <= 0 {
			rangeDays = 1
		}
	}

	bundle := RepoAnalysisBundle{
		Version: bundleVersion,
		Repo: RepoAnalysisRepo{
			Name:        filepath.Base(strings.TrimRight(repoRoot, string(filepath.Separator))),
			Root:        repoRoot,
			RangeDays:   rangeDays,
			GeneratedAt: generatedAt.Format(time.RFC3339),
		},
		Sessions: buildSessionSummaries(sessions),
	}

	fileStats := map[string]*fileAccum{}
	subsystemStats := map[string]*subsystemAccum{}
	coRead := map[string]int{}
	coEdit := map[string]int{}
	readBeforeEdit := map[string]int{}
	samePromptBlock := map[string]int{}
	sameSession := map[string]int{}
	expensiveEdits := map[string]*expensiveEditAccum{}
	readBeforeEditTraces := map[string]*readBeforeEditAccum{}
	lifecyclePatterns := map[string]*lifecycleAccum{}
	testSignals := &testSignalsAccum{
		sourceEditSessions:    map[string]struct{}{},
		sessionsWithTestReads: map[string]struct{}{},
		sessionsWithTestEdits: map[string]struct{}{},
		testsAsSpec:           map[string]*testRelationAccum{},
		sourceAndTestCoEdit:   map[string]*testRelationAccum{},
		testFriction:          map[string]*testFileAccum{},
		testChurn:             map[string]*testFileAccum{},
	}
	searchStats := map[string]*searchAccum{}
	deadEndSearches := 0

	var summary RepoAnalysisSummary
	for _, session := range sessions {
		summary.Sessions++
		m := session.metrics
		summary.Prompts += m.UserPromptCount
		summary.ToolCalls += m.ToolCallCount
		effCtx := effectiveCtx(m.TokenUsage)
		summary.ContextTokens += effCtx
		summary.TotalTokens += totalTok(m.TokenUsage)
		summary.CostUSD += m.EstimatedCostUSD

		perFile := buildFileActivity(repoRoot, m)
		for _, a := range perFile {
			summary.FileReads += a.reads
			summary.FileEdits += a.edits
		}

		sessionFiles := make([]string, 0, len(perFile))
		readFiles := make([]string, 0)
		editFiles := make([]string, 0)
		totalWeight := 0
		for path, a := range perFile {
			sessionFiles = append(sessionFiles, path)
			totalWeight += a.weight
			if a.reads > 0 {
				readFiles = append(readFiles, path)
			}
			if a.edits > 0 {
				editFiles = append(editFiles, path)
			}
		}
		sort.Strings(sessionFiles)
		sort.Strings(readFiles)
		sort.Strings(editFiles)

		for path, a := range perFile {
			stat := fileStats[path]
			if stat == nil {
				stat = &fileAccum{
					path:                   path,
					kind:                   fileKind(path),
					sessionSet:             map[string]struct{}{},
					editSessionSet:         map[string]struct{}{},
					relatedTests:           map[string]*relatedTestAccum{},
					readBeforeEditTargets:  map[string]int{},
					commonlySeenWith:       map[string]int{},
					foundViaSearchSessions: map[string]struct{}{},
					searchQueries:          map[string]int{},
				}
				fileStats[path] = stat
			}
			stat.sessionSet[session.id] = struct{}{}
			if a.edits > 0 {
				stat.editSessionSet[session.id] = struct{}{}
			}
			stat.reads += a.reads
			stat.edits += a.edits
			if totalWeight > 0 {
				stat.contextTokens += int64(float64(effCtx) * float64(a.weight) / float64(totalWeight))
			}
			if stat.firstSeen.IsZero() || session.timestamp.Before(stat.firstSeen) {
				stat.firstSeen = session.timestamp
			}
			if stat.lastSeen.IsZero() || session.timestamp.After(stat.lastSeen) {
				stat.lastSeen = session.timestamp
			}

			sub := subsystemName(path)
			ss := subsystemStats[sub]
			if ss == nil {
				ss = &subsystemAccum{
					name:                  sub,
					pathSet:               map[string]struct{}{},
					sessionSet:            map[string]struct{}{},
					fileScores:            map[string]float64{},
					sourceEditSessionSet:  map[string]struct{}{},
					testTouchedSessionSet: map[string]struct{}{},
				}
				subsystemStats[sub] = ss
			}
			ss.pathSet[path] = struct{}{}
			ss.sessionSet[session.id] = struct{}{}
			ss.reads += a.reads
			ss.edits += a.edits
			switch {
			case isSnapshotPath(path):
				// skip
			case isTestPath(path):
				ss.testReads += a.reads
				ss.testEdits += a.edits
				if a.reads > 0 || a.edits > 0 {
					ss.testTouchedSessionSet[session.id] = struct{}{}
				}
			default:
				ss.sourceReads += a.reads
				ss.sourceEdits += a.edits
				if a.edits > 0 {
					ss.sourceEditSessionSet[session.id] = struct{}{}
				}
			}
			if totalWeight > 0 {
				share := float64(a.weight) / float64(totalWeight)
				ss.contextTokens += int64(float64(effCtx) * share)
				ss.costUSD += m.EstimatedCostUSD * share
				ss.fileScores[path] += float64(a.reads) + float64(a.edits*2)
			}
		}

		countPairs(coRead, readFiles)
		countPairs(coEdit, editFiles)
		countPairs(sameSession, sessionFiles)
		addTracePatterns(expensiveEdits, readBeforeEditTraces, repoRoot, session)
		addLifecyclePatterns(lifecyclePatterns, repoRoot, session)
		addPromptRelationships(fileStats, subsystemStats, readBeforeEdit, samePromptBlock, repoRoot, session)
		addSearchDiscoverability(fileStats, searchStats, &deadEndSearches, repoRoot, session)
		addTestSignals(fileStats, subsystemStats, testSignals, repoRoot, session, perFile)
	}

	summary.AvgPromptsPerSession = safeDivide(float64(summary.Prompts), float64(summary.Sessions))
	summary.AvgReadsPerSession = safeDivide(float64(summary.FileReads), float64(summary.Sessions))
	summary.AvgEditsPerSession = safeDivide(float64(summary.FileEdits), float64(summary.Sessions))
	summary.CostUSD = round2(summary.CostUSD)
	summary.AvgCostPerSession = round2(safeDivide(summary.CostUSD, float64(summary.Sessions)))
	bundle.Summary = summary
	bundle.Files = finalizeFiles(fileStats)
	bundle.Subsystems = finalizeSubsystems(subsystemStats)
	bundle.SeenWithGroups = finalizeSeenWithGroups(fileStats)
	edges := finalizeRelationshipEdges(fileStats, coRead, coEdit, readBeforeEdit, samePromptBlock, sameSession)
	bundle.Relationships = finalizeRelationshipGroups(edges)
	bundle.TracePatterns = finalizeTraces(expensiveEdits, readBeforeEditTraces, lifecyclePatterns, bundle.Files)
	bundle.TestSignals = finalizeTestSignals(testSignals)
	bundle.Discoverability = finalizeDiscoverability(fileStats, searchStats, deadEndSearches)
	bundle.Docs = finalizeDocs(bundle.Files)
	attachRelatedSessions(&bundle, repoRoot, sessions)
	return bundle
}

// ── file activity ─────────────────────────────────────────────────────────────

type fileActivity struct {
	reads  int
	edits  int
	weight int
}

func buildFileActivity(repoRoot string, m sessionMetrics) map[string]fileActivity {
	out := map[string]fileActivity{}
	for path, reads := range m.ReadFileCounts {
		rel := repoRelPath(repoRoot, path)
		if rel == "" {
			continue
		}
		e := out[rel]
		e.reads += reads
		e.weight += reads
		out[rel] = e
	}
	for path, edits := range m.EditFileCounts {
		rel := repoRelPath(repoRoot, path)
		if rel == "" {
			continue
		}
		e := out[rel]
		e.edits += edits
		e.weight += edits * 2
		if e.weight == 0 {
			e.weight = edits
		}
		out[rel] = e
	}
	return out
}

func repoRelPath(repoRoot, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		rel, err := filepath.Rel(repoRoot, clean)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		return filepath.ToSlash(rel)
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(clean)
}

func normalizeRepoPaths(repoRoot string, paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		rel := repoRelPath(repoRoot, path)
		if rel == "" {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// ── prompt relationships ──────────────────────────────────────────────────────

func addPromptRelationships(
	fileStats map[string]*fileAccum,
	subsystemStats map[string]*subsystemAccum,
	readBeforeEdit map[string]int,
	samePromptBlock map[string]int,
	repoRoot string,
	session backendSession,
) {
	for idx, block := range session.metrics.PromptBlocks {
		reads := normalizeRepoPaths(repoRoot, block.ReadFiles)
		edits := normalizeRepoPaths(repoRoot, block.EditedFiles)
		all := append(append([]string{}, reads...), edits...)
		countPairs(samePromptBlock, uniqueStrings(all))

		contextBefore := promptContextBefore(session.metrics.PromptBlocks, idx)
		for _, target := range edits {
			if stat := fileStats[target]; stat != nil {
				stat.readsBeforeFirstEditSum += float64(block.ReadsBeforeFirstEdit)
				stat.readsBeforeFirstEditN++
				stat.promptsBeforeFirstEditSum += float64(idx + 1)
				stat.promptsBeforeFirstEditN++
				stat.contextBeforeFirstEditSum += contextBefore
				stat.contextBeforeFirstEditN++
			}
			if ss := subsystemStats[subsystemName(target)]; ss != nil {
				ss.readsBeforeFirstEditSum += float64(block.ReadsBeforeFirstEdit)
				ss.readsBeforeFirstEditN++
			}
			for _, source := range reads {
				if source == target {
					continue
				}
				readBeforeEdit[pairKey(source, target)]++
				if stat := fileStats[source]; stat != nil {
					stat.readBeforeEditTargets[target]++
				}
			}
		}
	}

	for i, a := range session.metrics.PromptBlocks {
		_ = i
		_ = a
	}

	sessionFiles := make([]string, 0)
	for path := range buildFileActivity(repoRoot, session.metrics) {
		sessionFiles = append(sessionFiles, path)
	}
	sort.Strings(sessionFiles)
	for i := range sessionFiles {
		for j := range sessionFiles {
			if i == j {
				continue
			}
			if stat := fileStats[sessionFiles[i]]; stat != nil {
				stat.commonlySeenWith[sessionFiles[j]]++
			}
		}
	}
}

func promptContextBefore(blocks []promptBlock, upto int) float64 {
	var total int64
	for i := 0; i <= upto && i < len(blocks); i++ {
		total += effectiveCtx(blocks[i].TokenUsage)
	}
	return float64(total)
}

// ── search discoverability ────────────────────────────────────────────────────

func addSearchDiscoverability(
	fileStats map[string]*fileAccum,
	searchStats map[string]*searchAccum,
	deadEnd *int,
	repoRoot string,
	session backendSession,
) {
	for _, block := range session.metrics.PromptBlocks {
		*deadEnd += block.DeadEndSearches
		searchesByTarget := map[string]int{}
		readsByTarget := map[string]int{}
		queriesByTarget := map[string]map[string]int{}
		for _, tr := range block.Searches {
			query := normalizeQuery(tr.Query)
			if query == "" {
				query = "(unknown)"
			}
			qs := searchStats[query]
			if qs == nil {
				qs = &searchAccum{query: query, readTargets: map[string]struct{}{}, editTargets: map[string]struct{}{}}
				searchStats[query] = qs
			}
			qs.searches++
			for _, raw := range tr.ReadFiles {
				if path := repoRelPath(repoRoot, raw); path != "" {
					qs.readTargets[path] = struct{}{}
				}
			}
			target := repoRelPath(repoRoot, tr.EditTarget)
			if target != "" {
				qs.editTargets[target] = struct{}{}
			}
			if !tr.FoundViaSearch || target == "" {
				continue
			}
			searchesByTarget[target]++
			readsByTarget[target] += tr.ReadsBeforeEdit
			if queriesByTarget[target] == nil {
				queriesByTarget[target] = map[string]int{}
			}
			queriesByTarget[target][query]++
		}
		for target, searches := range searchesByTarget {
			stat := fileStats[target]
			if stat == nil {
				continue
			}
			stat.foundViaSearchSessions[session.id] = struct{}{}
			stat.searchesBeforeEditSum += searches
			stat.readsAfterSearchSum += readsByTarget[target]
			for q, cnt := range queriesByTarget[target] {
				stat.searchQueries[q] += cnt
			}
		}
	}
}

func normalizeQuery(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(q))), " ")
}

// ── trace patterns ────────────────────────────────────────────────────────────

func addTracePatterns(
	expensiveEdits map[string]*expensiveEditAccum,
	readBeforeEditTraces map[string]*readBeforeEditAccum,
	repoRoot string,
	session backendSession,
) {
	effCtx := effectiveCtx(session.metrics.TokenUsage)
	costUSD := session.metrics.EstimatedCostUSD
	type seenEdit struct {
		readsBeforeFirstEdit   int
		promptsBeforeFirstEdit int
		precedingReads         []string
		relatedSubsystems      []string
	}
	perTarget := map[string]seenEdit{}

	for idx, block := range session.metrics.PromptBlocks {
		reads := normalizeRepoPaths(repoRoot, block.ReadFiles)
		edits := normalizeRepoPaths(repoRoot, block.EditedFiles)
		for _, target := range edits {
			if !traceEligible(target) {
				continue
			}
			if _, seen := perTarget[target]; seen {
				continue
			}
			precedingReads := []string{}
			subsystems := []string{subsystemName(target)}
			for _, source := range reads {
				if source == target || !traceEligible(source) {
					continue
				}
				precedingReads = append(precedingReads, source)
				subsystems = append(subsystems, subsystemName(source))

				key := pairKey(source, target)
				acc := readBeforeEditTraces[key]
				if acc == nil {
					acc = &readBeforeEditAccum{source: source, target: target, sessionSet: map[string]struct{}{}}
					readBeforeEditTraces[key] = acc
				}
				if _, ok := acc.sessionSet[session.id]; !ok {
					acc.sessionSet[session.id] = struct{}{}
					acc.costSum += costUSD
					acc.contextSum += effCtx
				}
			}
			perTarget[target] = seenEdit{
				readsBeforeFirstEdit:   block.ReadsBeforeFirstEdit,
				promptsBeforeFirstEdit: idx + 1,
				precedingReads:         uniqueStrings(precedingReads),
				relatedSubsystems:      uniqueStrings(subsystems),
			}
		}
	}

	for target, stats := range perTarget {
		acc := expensiveEdits[target]
		if acc == nil {
			acc = &expensiveEditAccum{
				target:                target,
				sessionSet:            map[string]struct{}{},
				precedingReadSessions: map[string]int{},
				relatedSubsystems:     map[string]int{},
			}
			expensiveEdits[target] = acc
		}
		if _, ok := acc.sessionSet[session.id]; ok {
			continue
		}
		acc.sessionSet[session.id] = struct{}{}
		acc.costSum += costUSD
		acc.contextSum += effCtx
		acc.readsBeforeFirstEditSum += float64(stats.readsBeforeFirstEdit)
		acc.promptsBeforeFirstEditSum += float64(stats.promptsBeforeFirstEdit)
		for _, path := range stats.precedingReads {
			acc.precedingReadSessions[path]++
		}
		for _, name := range stats.relatedSubsystems {
			acc.relatedSubsystems[name]++
		}
	}
}

func addLifecyclePatterns(dst map[string]*lifecycleAccum, repoRoot string, session backendSession) {
	effCtx := effectiveCtx(session.metrics.TokenUsage)
	costUSD := session.metrics.EstimatedCostUSD
	perFile := map[string][]string{}
	for _, ev := range session.events {
		for _, raw := range ev.ReadPaths {
			if path := repoRelPath(repoRoot, raw); path != "" {
				perFile[path] = append(perFile[path], "read")
			}
		}
		for _, raw := range ev.WritePaths {
			if path := repoRelPath(repoRoot, raw); path != "" {
				perFile[path] = append(perFile[path], "edit")
			}
		}
	}
	for file, ops := range perFile {
		if !traceEligible(file) || !containsStr(ops, "edit") || len(ops) < 2 {
			continue
		}
		chain := lifecycleChain(ops)
		key := file + "|" + chain
		acc := dst[key]
		if acc == nil {
			acc = &lifecycleAccum{file: file, chain: chain}
			dst[key] = acc
		}
		acc.sessions++
		acc.costSum += costUSD
		acc.contextSum += effCtx
	}
}

// ── test signals (simplified: no filesystem scan) ─────────────────────────────

func addTestSignals(
	fileStats map[string]*fileAccum,
	subsystemStats map[string]*subsystemAccum,
	ts *testSignalsAccum,
	repoRoot string,
	session backendSession,
	perFile map[string]fileActivity,
) {
	sessionID := session.id
	effCtx := effectiveCtx(session.metrics.TokenUsage)
	sourceEditsByFile := map[string]int{}
	testReadsByFile := map[string]int{}
	testEditsByFile := map[string]int{}

	for path, a := range perFile {
		switch {
		case isSnapshotPath(path):
			continue
		case isTestPath(path):
			if a.reads > 0 {
				testReadsByFile[path] = a.reads
				ts.sessionsWithTestReads[sessionID] = struct{}{}
			}
			if a.edits > 0 {
				testEditsByFile[path] = a.edits
				ts.sessionsWithTestEdits[sessionID] = struct{}{}
				acc := ts.testChurn[path]
				if acc == nil {
					acc = &testFileAccum{test: path, sessionSet: map[string]struct{}{}}
					ts.testChurn[path] = acc
				}
				acc.sessionSet[sessionID] = struct{}{}
				acc.edits += a.edits
			}
		default:
			if a.edits > 0 {
				sourceEditsByFile[path] = a.edits
				ts.sourceEditSessions[sessionID] = struct{}{}
			}
		}
	}

	// pair detection without filesystem scan: match test files seen in the same session
	for source := range sourceEditsByFile {
		for test, reads := range testReadsByFile {
			if !strings.Contains(strings.ToLower(test), stripExt(filepath.Base(source))) {
				continue
			}
			if stat := fileStats[source]; stat != nil {
				stat.testReads += reads
				link := stat.relatedTests[test]
				if link == nil {
					link = &relatedTestAccum{path: test}
					stat.relatedTests[test] = link
				}
				link.reads += reads
			}
			key := source + "\x00" + test
			acc := ts.testsAsSpec[key]
			if acc == nil {
				acc = &testRelationAccum{source: source, test: test, sessionSet: map[string]struct{}{}}
				ts.testsAsSpec[key] = acc
			}
			acc.sessionSet[sessionID] = struct{}{}
		}
		for test, edits := range testEditsByFile {
			if !strings.Contains(strings.ToLower(test), stripExt(filepath.Base(source))) {
				continue
			}
			if stat := fileStats[source]; stat != nil {
				stat.testEdits += edits
				link := stat.relatedTests[test]
				if link == nil {
					link = &relatedTestAccum{path: test}
					stat.relatedTests[test] = link
				}
				link.edits += edits
			}
			if ss := subsystemStats[subsystemName(source)]; ss != nil {
				ss.testTouchedSessionSet[sessionID] = struct{}{}
			}
			key := source + "\x00" + test
			acc := ts.sourceAndTestCoEdit[key]
			if acc == nil {
				acc = &testRelationAccum{source: source, test: test, sessionSet: map[string]struct{}{}}
				ts.sourceAndTestCoEdit[key] = acc
			}
			acc.sessionSet[sessionID] = struct{}{}
		}
		if stat := fileStats[source]; stat != nil {
			if len(testReadsByFile) == 0 {
				stat.sourceEditWithoutTestRead++
			}
			if len(testEditsByFile) == 0 {
				stat.sourceEditWithoutTestEdit++
			}
		}
	}

	for test := range testReadsByFile {
		acc := ts.testFriction[test]
		if acc == nil {
			acc = &testFileAccum{test: test, sessionSet: map[string]struct{}{}}
			ts.testFriction[test] = acc
		}
		acc.sessionSet[sessionID] = struct{}{}
		acc.reads += testReadsByFile[test]
		acc.contextTokens += effCtx
		for _, edits := range sourceEditsByFile {
			acc.sourceEdits += edits
		}
	}
}

func stripExt(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return strings.ToLower(name)
	}
	return strings.ToLower(strings.TrimSuffix(name, ext))
}

// ── finalization ──────────────────────────────────────────────────────────────

func finalizeFiles(stats map[string]*fileAccum) []RepoAnalysisFile {
	out := make([]RepoAnalysisFile, 0, len(stats))
	for _, stat := range stats {
		sessions := len(stat.sessionSet)
		editSessions := len(stat.editSessionSet)
		item := RepoAnalysisFile{
			Path:            stat.path,
			Kind:            stat.kind,
			Sessions:        sessions,
			Reads:           stat.reads,
			ReadsPerSession: safeDivide(float64(stat.reads), float64(sessions)),
			Edits:           stat.edits,
			EditSessions:    editSessions,
			EditRate:        safeDivide(float64(editSessions), float64(sessions)),
			ContextTokens:   stat.contextTokens,
			LastSeen:        formatDay(stat.lastSeen),
			FirstSeen:       formatDay(stat.firstSeen),
			Classification:  classifyFile(stat),
		}
		if stat.edits > 0 {
			v := safeDivide(float64(stat.contextTokens), float64(stat.edits))
			item.TokensPerEdit = &v
		}
		if stat.readsBeforeFirstEditN > 0 {
			v := round2(safeDivide(stat.readsBeforeFirstEditSum, float64(stat.readsBeforeFirstEditN)))
			item.AvgReadsBeforeFirstEdit = &v
		}
		if stat.promptsBeforeFirstEditN > 0 {
			v := round2(safeDivide(stat.promptsBeforeFirstEditSum, float64(stat.promptsBeforeFirstEditN)))
			item.AvgPromptsBeforeFirstEdit = &v
		}
		if stat.contextBeforeFirstEditN > 0 {
			v := round2(safeDivide(stat.contextBeforeFirstEditSum, float64(stat.contextBeforeFirstEditN)))
			item.AvgContextBeforeFirstEdit = &v
		}
		item.TestReads = stat.testReads
		item.TestEdits = stat.testEdits
		item.TestReadBeforeEdit = stat.testReadBeforeEdit
		item.SourceEditWithoutTestRead = stat.sourceEditWithoutTestRead
		item.SourceEditWithoutTestEdit = stat.sourceEditWithoutTestEdit
		item.RelatedTests = finalizeRelatedTests(stat.relatedTests)
		item.ReadBeforeEditOf = topStringKeys(stat.readBeforeEditTargets, 5, 1)
		item.FoundViaSearchSessions = len(stat.foundViaSearchSessions)
		if item.FoundViaSearchSessions > 0 {
			item.AvgSearchesBeforeEdit = round2(safeDivide(float64(stat.searchesBeforeEditSum), float64(item.FoundViaSearchSessions)))
			item.AvgReadsAfterSearchBeforeEdit = round2(safeDivide(float64(stat.readsAfterSearchSum), float64(maxInt(stat.searchesBeforeEditSum, 1))))
			item.TopQueries = topQueryCounts(stat.searchQueries, 5)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		si := float64(out[i].Sessions*3 + out[i].Reads + out[i].EditSessions*4)
		sj := float64(out[j].Sessions*3 + out[j].Reads + out[j].EditSessions*4)
		if si != sj {
			return si > sj
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func finalizeRelatedTests(stats map[string]*relatedTestAccum) []RepoAnalysisFileLink {
	out := make([]RepoAnalysisFileLink, 0, len(stats))
	for _, stat := range stats {
		if stat.reads == 0 && stat.edits == 0 {
			continue
		}
		out = append(out, RepoAnalysisFileLink{Path: stat.path, Reads: stat.reads, Edits: stat.edits})
	}
	sort.Slice(out, func(i, j int) bool {
		si := out[i].Reads + out[i].Edits*2
		sj := out[j].Reads + out[j].Edits*2
		if si != sj {
			return si > sj
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func finalizeSubsystems(stats map[string]*subsystemAccum) []RepoAnalysisSubsystem {
	out := make([]RepoAnalysisSubsystem, 0, len(stats))
	for _, stat := range stats {
		paths := sortedKeys(stat.pathSet)
		topFiles := topScoredKeys(stat.fileScores, 5)
		item := RepoAnalysisSubsystem{
			Name:                stat.name,
			Paths:               paths,
			Sessions:            len(stat.sessionSet),
			Reads:               stat.reads,
			Edits:               stat.edits,
			SourceReads:         stat.sourceReads,
			SourceEdits:         stat.sourceEdits,
			TestReads:           stat.testReads,
			TestEdits:           stat.testEdits,
			SourceEditSessions:  len(stat.sourceEditSessionSet),
			TestTouchedSessions: len(stat.testTouchedSessionSet),
			ContextTokens:       stat.contextTokens,
			CostUSD:             round2(stat.costUSD),
			TopFiles:            topFiles,
			Classification:      classifySubsystem(stat),
		}
		item.TestTouchRate = round2(safeDivide(float64(item.TestTouchedSessions), float64(maxInt(item.SourceEditSessions, 1))))
		if stat.readsBeforeFirstEditN > 0 {
			v := round2(safeDivide(stat.readsBeforeFirstEditSum, float64(stat.readsBeforeFirstEditN)))
			item.AvgReadsBeforeFirstEdit = &v
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if out[i].ContextTokens != out[j].ContextTokens {
			return out[i].ContextTokens > out[j].ContextTokens
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

func finalizeSeenWithGroups(stats map[string]*fileAccum) []RepoAnalysisRelation {
	groups := map[string]int{}
	for _, stat := range stats {
		top := topStringKeys(stat.commonlySeenWith, 4, 2)
		if len(top) == 0 {
			continue
		}
		files := pruneAncestorPaths(uniqueStrings(append([]string{stat.path}, top...)))
		if len(files) < 2 {
			continue
		}
		key := strings.Join(files, "\x00")
		score := 0
		for _, other := range top {
			score += stat.commonlySeenWith[other]
		}
		if score > groups[key] {
			groups[key] = score
		}
	}
	out := make([]RepoAnalysisRelation, 0, len(groups))
	for key, score := range groups {
		files := strings.Split(key, "\x00")
		out = append(out, RepoAnalysisRelation{Type: "seen_with_group", Files: files, Sessions: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return strings.Join(out[i].Files, "|") < strings.Join(out[j].Files, "|")
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func pruneAncestorPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for i, c := range paths {
		if c == "" {
			continue
		}
		isAnc := false
		for j, o := range paths {
			if i == j || o == "" || c == o {
				continue
			}
			if strings.HasPrefix(o, c+"/") {
				isAnc = true
				break
			}
		}
		if !isAnc {
			out = append(out, c)
		}
	}
	return out
}

func finalizeTraces(
	expensiveEdits map[string]*expensiveEditAccum,
	readBeforeEditTraces map[string]*readBeforeEditAccum,
	lifecycles map[string]*lifecycleAccum,
	files []RepoAnalysisFile,
) []RepoAnalysisTrace {
	out := make([]RepoAnalysisTrace, 0)
	for _, acc := range expensiveEdits {
		sessions := len(acc.sessionSet)
		if sessions < 3 {
			continue
		}
		avgCost := safeDivide(acc.costSum, float64(maxInt(sessions, 1)))
		avgCtx := safeDivide(float64(acc.contextSum), float64(maxInt(sessions, 1)))
		if !shouldIncludeTrace(sessions, avgCost, avgCtx) {
			continue
		}
		out = append(out, RepoAnalysisTrace{
			Type:                 "expensive_edit_target",
			PatternType:          "expensive_edit_targets",
			Target:               acc.target,
			Sessions:             sessions,
			AvgCostUSD:           round2(avgCost),
			AvgContextTokens:     round2(avgCtx),
			AvgReadsBeforeEdit:   round2(safeDivide(acc.readsBeforeFirstEditSum, float64(maxInt(sessions, 1)))),
			AvgPromptsBeforeEdit: round2(safeDivide(acc.promptsBeforeFirstEditSum, float64(maxInt(sessions, 1)))),
			TopPrecedingReads:    topPathCounts(acc.precedingReadSessions, 5, 1),
			TopRelatedSubsystems: topNameCounts(acc.relatedSubsystems, 5, 1),
		})
	}
	for _, acc := range readBeforeEditTraces {
		sessions := len(acc.sessionSet)
		avgCost := safeDivide(acc.costSum, float64(maxInt(sessions, 1)))
		avgCtx := safeDivide(float64(acc.contextSum), float64(maxInt(sessions, 1)))
		if !shouldIncludeTrace(sessions, avgCost, avgCtx) {
			continue
		}
		out = append(out, RepoAnalysisTrace{
			Type:             "read_before_edit_pattern",
			PatternType:      "read_before_edit_patterns",
			Source:           acc.source,
			Target:           acc.target,
			Sessions:         sessions,
			AvgCostUSD:       round2(avgCost),
			AvgContextTokens: round2(avgCtx),
		})
	}
	for _, life := range lifecycles {
		avgCost := safeDivide(life.costSum, float64(maxInt(life.sessions, 1)))
		avgCtx := safeDivide(float64(life.contextSum), float64(maxInt(life.sessions, 1)))
		if !shouldIncludeTrace(life.sessions, avgCost, avgCtx) && !significantLifecycle(life.chain) {
			continue
		}
		out = append(out, RepoAnalysisTrace{
			Type:             "file_lifecycle_pattern",
			PatternType:      "file_lifecycle_patterns",
			Target:           life.file,
			File:             life.file,
			Chain:            life.chain,
			Sessions:         life.sessions,
			AvgCostUSD:       round2(avgCost),
			AvgContextTokens: round2(avgCtx),
		})
	}
	for _, file := range files {
		if !containsStr(file.Classification, "context_tax") {
			continue
		}
		avgCtx := safeDivide(float64(file.ContextTokens), float64(maxInt(file.Sessions, 1)))
		if file.Edits > 0 && file.Sessions < 2 && avgCtx < 3_000_000 {
			continue
		}
		if !shouldIncludeTrace(file.Sessions, 0, avgCtx) {
			continue
		}
		out = append(out, RepoAnalysisTrace{
			Type:             "context_tax_read",
			PatternType:      "context_tax_reads",
			Target:           file.Path,
			Sessions:         file.Sessions,
			Reads:            file.Reads,
			Edits:            file.Edits,
			AvgContextTokens: round2(avgCtx),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return traceLabel(out[i]) < traceLabel(out[j])
	})
	out = limitTraceByType(out, "expensive_edit_target", 20)
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func limitTraceByType(traces []RepoAnalysisTrace, typ string, limit int) []RepoAnalysisTrace {
	count := 0
	out := make([]RepoAnalysisTrace, 0, len(traces))
	for _, t := range traces {
		if t.Type == typ {
			if count >= limit {
				continue
			}
			count++
		}
		out = append(out, t)
	}
	return out
}

func finalizeTestSignals(acc *testSignalsAccum) RepoAnalysisTestSignals {
	if acc == nil {
		return RepoAnalysisTestSignals{}
	}
	out := RepoAnalysisTestSignals{
		Summary: RepoAnalysisTestSummary{
			SourceEditSessions:    len(acc.sourceEditSessions),
			SessionsWithTestReads: len(acc.sessionsWithTestReads),
			SessionsWithTestEdits: len(acc.sessionsWithTestEdits),
		},
	}
	out.Summary.TestTouchRate = round2(safeDivide(float64(out.Summary.SessionsWithTestReads), float64(maxInt(out.Summary.SourceEditSessions, 1))))
	out.TestsAsSpec = finalizeTestRelations(acc.testsAsSpec, "test_as_spec", 20)
	out.SourceAndTestCoEdit = finalizeTestRelations(acc.sourceAndTestCoEdit, "source_and_test_co_edit", 20)
	out.TestFriction = finalizeTestFiles(acc.testFriction, "test_friction", 15)
	out.TestChurn = finalizeTestFiles(acc.testChurn, "test_churn", 15)
	return out
}

func finalizeTestRelations(src map[string]*testRelationAccum, typ string, limit int) []RepoAnalysisTestRelation {
	out := make([]RepoAnalysisTestRelation, 0, len(src))
	for _, acc := range src {
		out = append(out, RepoAnalysisTestRelation{Type: typ, Source: acc.source, Test: acc.test, Sessions: len(acc.sessionSet)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Source < out[j].Source
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func finalizeTestFiles(src map[string]*testFileAccum, typ string, limit int) []RepoAnalysisTestFile {
	out := make([]RepoAnalysisTestFile, 0, len(src))
	for _, acc := range src {
		out = append(out, RepoAnalysisTestFile{
			Type: typ, Test: acc.test, Sessions: len(acc.sessionSet),
			Reads: acc.reads, Edits: acc.edits, SourceEdits: acc.sourceEdits, ContextTokens: acc.contextTokens,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Test < out[j].Test
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func finalizeDiscoverability(fileStats map[string]*fileAccum, searchStats map[string]*searchAccum, deadEnd int) RepoAnalysisDiscoverability {
	out := RepoAnalysisDiscoverability{DeadEndSearches: deadEnd}
	for _, stat := range fileStats {
		sessions := len(stat.foundViaSearchSessions)
		if sessions == 0 {
			continue
		}
		confidence := searchConfidence(stat.searchQueries, searchStats, sessions)
		out.SearchHeavyTargets = append(out.SearchHeavyTargets, RepoAnalysisSearchTarget{
			Path:                          stat.path,
			FoundViaSearchSessions:        sessions,
			AvgSearchesBeforeEdit:         round2(safeDivide(float64(stat.searchesBeforeEditSum), float64(sessions))),
			AvgReadsAfterSearchBeforeEdit: round2(safeDivide(float64(stat.readsAfterSearchSum), float64(maxInt(stat.searchesBeforeEditSum, 1)))),
			TopQueries:                    topQueryCounts(stat.searchQueries, 5),
			TargetConfidence:              confidence,
		})
	}
	sort.Slice(out.SearchHeavyTargets, func(i, j int) bool {
		if out.SearchHeavyTargets[i].FoundViaSearchSessions != out.SearchHeavyTargets[j].FoundViaSearchSessions {
			return out.SearchHeavyTargets[i].FoundViaSearchSessions > out.SearchHeavyTargets[j].FoundViaSearchSessions
		}
		return out.SearchHeavyTargets[i].Path < out.SearchHeavyTargets[j].Path
	})
	if len(out.SearchHeavyTargets) > 20 {
		out.SearchHeavyTargets = out.SearchHeavyTargets[:20]
	}
	for _, stat := range searchStats {
		if len(stat.editTargets) < 2 && len(stat.readTargets) < 5 {
			continue
		}
		out.AmbiguousSearches = append(out.AmbiguousSearches, RepoAnalysisAmbiguousSearch{
			Query:               stat.query,
			Searches:            stat.searches,
			DistinctReadTargets: len(stat.readTargets),
			DistinctEditTargets: len(stat.editTargets),
		})
	}
	sort.Slice(out.AmbiguousSearches, func(i, j int) bool {
		if out.AmbiguousSearches[i].Searches != out.AmbiguousSearches[j].Searches {
			return out.AmbiguousSearches[i].Searches > out.AmbiguousSearches[j].Searches
		}
		return out.AmbiguousSearches[i].Query < out.AmbiguousSearches[j].Query
	})
	if len(out.AmbiguousSearches) > 20 {
		out.AmbiguousSearches = out.AmbiguousSearches[:20]
	}
	return out
}

func searchConfidence(counts map[string]int, searchStats map[string]*searchAccum, sessions int) string {
	top := topQueryCounts(counts, 1)
	if len(top) == 0 {
		return "low"
	}
	stat := searchStats[top[0].Query]
	if stat == nil || len(stat.editTargets) > 2 {
		return "low"
	}
	if sessions >= 3 && len(stat.editTargets) == 1 {
		return "high"
	}
	return "medium"
}

func finalizeDocs(files []RepoAnalysisFile) []RepoAnalysisDoc {
	out := make([]RepoAnalysisDoc, 0)
	for _, file := range files {
		if file.Kind != "doc" {
			continue
		}
		doc := RepoAnalysisDoc{
			Path:             file.Path,
			Sessions:         file.Sessions,
			Reads:            file.Reads,
			EditSessions:     file.EditSessions,
			ReadBeforeEditOf: file.ReadBeforeEditOf,
		}
		if containsStr(file.Classification, "knowledge_hub") {
			doc.Classification = append(doc.Classification, "instruction_hub")
		}
		if containsStr(file.Classification, "context_tax") {
			doc.Classification = append(doc.Classification, "frequently_consulted")
		}
		out = append(out, doc)
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

const maxRelatedSessionsPerItem = 8

type sessionCoverage struct {
	ref       RepoAnalysisSessionRef
	timestamp time.Time
	paths     map[string]struct{}
}

func attachRelatedSessions(bundle *RepoAnalysisBundle, repoRoot string, sessions []backendSession) {
	if bundle == nil || len(sessions) == 0 {
		return
	}
	coverages := buildSessionCoverages(repoRoot, sessions)
	if len(coverages) == 0 {
		return
	}

	for i := range bundle.Subsystems {
		pathSet := toPathSet(bundle.Subsystems[i].Paths)
		bundle.Subsystems[i].RelatedSessions = collectRelatedSessions(coverages, func(c sessionCoverage) bool {
			return touchesAny(c.paths, pathSet)
		})
	}

	for i := range bundle.SeenWithGroups {
		files := bundle.SeenWithGroups[i].Files
		minMatches := 2
		if len(files) < minMatches {
			minMatches = len(files)
		}
		pathSet := toPathSet(files)
		bundle.SeenWithGroups[i].RelatedSessions = collectRelatedSessions(coverages, func(c sessionCoverage) bool {
			return touchCount(c.paths, pathSet) >= minMatches
		})
	}

	for i := range bundle.TestSignals.TestsAsSpec {
		rel := bundle.TestSignals.TestsAsSpec[i]
		bundle.TestSignals.TestsAsSpec[i].RelatedSessions = collectRelatedSessions(coverages, func(c sessionCoverage) bool {
			return hasPath(c.paths, rel.Source) && hasPath(c.paths, rel.Test)
		})
	}
	for i := range bundle.TestSignals.SourceAndTestCoEdit {
		rel := bundle.TestSignals.SourceAndTestCoEdit[i]
		bundle.TestSignals.SourceAndTestCoEdit[i].RelatedSessions = collectRelatedSessions(coverages, func(c sessionCoverage) bool {
			return hasPath(c.paths, rel.Source) && hasPath(c.paths, rel.Test)
		})
	}
	for i := range bundle.TestSignals.TestFriction {
		test := bundle.TestSignals.TestFriction[i].Test
		bundle.TestSignals.TestFriction[i].RelatedSessions = collectRelatedSessions(coverages, func(c sessionCoverage) bool {
			return hasPath(c.paths, test)
		})
	}
	for i := range bundle.TestSignals.TestChurn {
		test := bundle.TestSignals.TestChurn[i].Test
		bundle.TestSignals.TestChurn[i].RelatedSessions = collectRelatedSessions(coverages, func(c sessionCoverage) bool {
			return hasPath(c.paths, test)
		})
	}
}

func buildSessionCoverages(repoRoot string, sessions []backendSession) []sessionCoverage {
	out := make([]sessionCoverage, 0, len(sessions))
	for _, session := range sessions {
		perFile := buildFileActivity(repoRoot, session.metrics)
		if len(perFile) == 0 {
			continue
		}
		paths := make(map[string]struct{}, len(perFile))
		for path := range perFile {
			paths[path] = struct{}{}
		}
		out = append(out, sessionCoverage{
			ref: RepoAnalysisSessionRef{
				ID:        session.id,
				Name:      sessionDisplayName(session.name),
				Agent:     session.agent,
				Timestamp: session.timestamp.UTC().Format(time.RFC3339),
			},
			timestamp: session.timestamp,
			paths:     paths,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].timestamp.Equal(out[j].timestamp) {
			return out[i].timestamp.After(out[j].timestamp)
		}
		if out[i].ref.Name != out[j].ref.Name {
			return out[i].ref.Name < out[j].ref.Name
		}
		return out[i].ref.ID < out[j].ref.ID
	})
	return out
}

func sanitizeInteractionText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 300 {
		text = string(runes[:300])
	}
	return text
}

func buildSessionSummaries(sessions []backendSession) []RepoAnalysisSession {
	type sessionSummaryRow struct {
		session   RepoAnalysisSession
		timestamp time.Time
	}

	rows := make([]sessionSummaryRow, 0, len(sessions))
	for _, session := range sessions {
		prompts := make([]string, 0)
		interactions := make([]SessionInteraction, 0)
		for _, event := range session.events {
			switch event.Kind {
			case "prompt":
				if shouldIgnoreSessionPrompt(event.Text) {
					continue
				}
				if text := truncatePromptPreview(event.Text, 100); text != "" {
					prompts = append(prompts, text)
				}
				if text := sanitizeInteractionText(event.Text); text != "" {
					interactions = append(interactions, SessionInteraction{Role: "user", Text: text})
				}
			case "assistant_message":
				if text := sanitizeInteractionText(event.Text); text != "" {
					interactions = append(interactions, SessionInteraction{Role: "assistant", Text: text})
				}
			}
		}
		row := sessionSummaryRow{
			session: RepoAnalysisSession{
				ID:    session.id,
				Title: sessionDisplayName(session.name),
			},
			timestamp: session.timestamp,
		}
		if len(prompts) > 0 {
			row.session.Prompts = prompts
		}
		if len(interactions) > 0 {
			row.session.Interactions = interactions
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].timestamp.Equal(rows[j].timestamp) {
			return rows[i].timestamp.After(rows[j].timestamp)
		}
		if rows[i].session.Title != rows[j].session.Title {
			return rows[i].session.Title < rows[j].session.Title
		}
		return rows[i].session.ID < rows[j].session.ID
	})
	out := make([]RepoAnalysisSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.session)
	}
	return out
}

func collectRelatedSessions(coverages []sessionCoverage, keep func(sessionCoverage) bool) []RepoAnalysisSessionRef {
	out := make([]RepoAnalysisSessionRef, 0, maxRelatedSessionsPerItem)
	for _, coverage := range coverages {
		if !keep(coverage) {
			continue
		}
		out = append(out, coverage.ref)
		if len(out) >= maxRelatedSessionsPerItem {
			break
		}
	}
	return out
}

func toPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func touchesAny(sessionPaths, want map[string]struct{}) bool {
	for path := range want {
		if _, ok := sessionPaths[path]; ok {
			return true
		}
	}
	return false
}

func touchCount(sessionPaths, want map[string]struct{}) int {
	count := 0
	for path := range want {
		if _, ok := sessionPaths[path]; ok {
			count++
		}
	}
	return count
}

func hasPath(paths map[string]struct{}, path string) bool {
	_, ok := paths[path]
	return ok
}

func sessionDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "(untitled)"
	}
	return name
}

func truncatePromptPreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func shouldIgnoreSessionPrompt(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<local-command-caveat>")
}

// ── classification ────────────────────────────────────────────────────────────

func classifyFile(stat *fileAccum) []string {
	sessions := len(stat.sessionSet)
	editSessions := len(stat.editSessionSet)
	editRate := safeDivide(float64(editSessions), float64(maxInt(sessions, 1)))
	out := make([]string, 0, 6)
	if stat.kind == "source" && stat.sourceEditWithoutTestRead >= 2 {
		out = append(out, "untested_edit_signal")
	}
	if stat.kind == "test" && stat.reads >= maxInt(sessions*2, 3) && stat.edits <= 1 {
		out = append(out, "test_as_spec")
	}
	if stat.reads >= maxInt(sessions*2, 3) && editSessions <= 1 {
		out = append(out, "context_tax")
	}
	if stat.reads >= maxInt(sessions, 2) && editRate >= 0.4 {
		out = append(out, "hotspot")
	}
	if stat.readsBeforeFirstEditN > 0 && safeDivide(stat.readsBeforeFirstEditSum, float64(stat.readsBeforeFirstEditN)) >= 3 {
		out = append(out, "friction")
	}
	if len(stat.readBeforeEditTargets) >= 2 {
		out = append(out, "knowledge_hub")
	}
	if stat.edits >= 3 && editSessions >= 2 {
		out = append(out, "churn")
	}
	if stat.kind == "test" && stat.edits >= 3 && editSessions >= 2 {
		out = append(out, "test_churn")
	}
	if stat.edits >= 2 && stat.contextTokens > 0 {
		tpe := safeDivide(float64(stat.contextTokens), float64(stat.edits))
		if tpe <= 8000 && (stat.readsBeforeFirstEditN == 0 || safeDivide(stat.readsBeforeFirstEditSum, float64(stat.readsBeforeFirstEditN)) <= 1.5) {
			out = append(out, "easy_surface")
		}
	}
	return out
}

func classifySubsystem(stat *subsystemAccum) []string {
	out := make([]string, 0, 4)
	if len(stat.sessionSet) > 0 && safeDivide(float64(stat.contextTokens), float64(len(stat.sessionSet))) >= 20_000 {
		out = append(out, "high_context")
	}
	if stat.edits >= 3 {
		out = append(out, "active_development")
	}
	if stat.readsBeforeFirstEditN > 0 && safeDivide(stat.readsBeforeFirstEditSum, float64(stat.readsBeforeFirstEditN)) >= 4 {
		out = append(out, "friction")
	}
	if len(stat.sourceEditSessionSet) >= 2 && len(stat.testTouchedSessionSet) == 0 {
		out = append(out, "low_test_context")
	}
	return out
}

// ── path helpers ──────────────────────────────────────────────────────────────

func fileKind(path string) string {
	base := filepath.Base(path)
	switch {
	case isSnapshotPath(path):
		return "snapshot"
	case isTestPath(path):
		return "test"
	case strings.HasSuffix(base, ".md"), strings.HasSuffix(base, ".mdx"), strings.HasSuffix(base, ".txt"), strings.HasSuffix(base, ".rst"):
		return "doc"
	case strings.HasSuffix(base, ".json"), strings.HasSuffix(base, ".yaml"), strings.HasSuffix(base, ".yml"), strings.HasSuffix(base, ".toml"), strings.HasSuffix(base, ".lock"):
		return "config"
	case strings.HasSuffix(base, ".go"), strings.HasSuffix(base, ".ts"), strings.HasSuffix(base, ".tsx"), strings.HasSuffix(base, ".js"), strings.HasSuffix(base, ".jsx"), strings.HasSuffix(base, ".py"), strings.HasSuffix(base, ".rb"), strings.HasSuffix(base, ".rs"), strings.HasSuffix(base, ".c"), strings.HasSuffix(base, ".cpp"), strings.HasSuffix(base, ".h"), strings.HasSuffix(base, ".java"), strings.HasSuffix(base, ".kt"), strings.HasSuffix(base, ".css"), strings.HasSuffix(base, ".html"):
		return "source"
	default:
		return "other"
	}
}

func isTestPath(path string) bool {
	p := filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	base := filepath.Base(p)
	return strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") ||
		strings.HasSuffix(base, "_spec.rb") ||
		strings.HasSuffix(base, "_test.py") ||
		(strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) ||
		strings.Contains(p, "/__tests__/") ||
		strings.Contains(p, "/tests/") ||
		strings.Contains(p, "/test/")
}

func isSnapshotPath(path string) bool {
	p := filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	base := filepath.Base(p)
	return strings.Contains(p, "/__snapshots__/") || strings.HasSuffix(base, ".snap")
}

func subsystemName(path string) string {
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." || dir == "/" {
		return "."
	}
	return dir
}

func traceEligible(path string) bool {
	k := relationshipPathKind(path)
	return k != "directory" && k != "repo_root"
}

// ── lifecycle ─────────────────────────────────────────────────────────────────

func lifecycleChain(ops []string) string {
	type seg struct {
		op    string
		count int
	}
	segs := make([]seg, 0, len(ops))
	for _, op := range ops {
		n := len(segs)
		if n > 0 && segs[n-1].op == op {
			segs[n-1].count++
			continue
		}
		segs = append(segs, seg{op: op, count: 1})
	}
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if s.count > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", s.op, s.count))
		} else {
			parts = append(parts, s.op)
		}
	}
	return strings.Join(parts, " → ")
}

func significantLifecycle(chain string) bool {
	return strings.Contains(chain, "edit → read → edit") || strings.Contains(chain, "read×3")
}

func shouldIncludeTrace(sessions int, avgCost, avgCtx float64) bool {
	return sessions >= 2 || avgCost >= 2.0 || avgCtx >= 3_000_000
}

// ── generic helpers ───────────────────────────────────────────────────────────

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func countPairs(dst map[string]int, values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			dst[pairKey(values[i], values[j])]++
		}
	}
}

func pairKey(a, b string) string {
	if a <= b {
		return a + "\x00" + b
	}
	return b + "\x00" + a
}

func splitPairKey(key string) (string, string) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}

func topStringKeys(counts map[string]int, limit, minVal int) []string {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= minVal {
			items = append(items, kv{k, v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.k
	}
	return out
}

func topScoredKeys(scores map[string]float64, limit int) []string {
	type kv struct {
		k string
		v float64
	}
	items := make([]kv, 0, len(scores))
	for k, v := range scores {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.k
	}
	return out
}

func topQueryCounts(counts map[string]int, limit int) []RepoAnalysisQueryCount {
	out := make([]RepoAnalysisQueryCount, 0, len(counts))
	for q, cnt := range counts {
		out = append(out, RepoAnalysisQueryCount{Query: q, Count: cnt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Query < out[j].Query
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func topPathCounts(counts map[string]int, limit, minVal int) []RepoAnalysisPathCount {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= minVal {
			items = append(items, kv{k, v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]RepoAnalysisPathCount, len(items))
	for i, item := range items {
		out[i] = RepoAnalysisPathCount{Path: item.k, Sessions: item.v}
	}
	return out
}

func topNameCounts(counts map[string]int, limit, minVal int) []RepoAnalysisNameCount {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= minVal {
			items = append(items, kv{k, v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]RepoAnalysisNameCount, len(items))
	for i, item := range items {
		out[i] = RepoAnalysisNameCount{Name: item.k, Sessions: item.v}
	}
	return out
}

func traceLabel(t RepoAnalysisTrace) string {
	return t.Type + ":" + t.Source + ":" + t.Target + ":" + t.Chain
}

func relationLabel(rel RepoAnalysisRelation) string {
	if len(rel.Files) == 2 {
		return rel.Type + ":" + rel.Files[0] + ":" + rel.Files[1]
	}
	return rel.Type + ":" + rel.Source + ":" + rel.Target
}

func relationFiles(rel RepoAnalysisRelation) []string {
	if len(rel.Files) == 2 {
		return rel.Files
	}
	if rel.Source != "" && rel.Target != "" {
		return []string{rel.Source, rel.Target}
	}
	return nil
}

func formatDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
