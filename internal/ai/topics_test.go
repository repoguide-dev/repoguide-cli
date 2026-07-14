package ai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/repoguide/repoguide-cli/internal/ai/prompts"
	"github.com/repoguide/repoguide-core/contracts/v1"
)

func TestBuildCandidatesSplitsBroadSubsystemIntoFeatureCandidates(t *testing.T) {
	bundle := contracts.RepoAnalysisBundle{
		Subsystems: []contracts.RepoAnalysisSubsystem{{
			Name:        "web/src",
			Sessions:    9,
			Reads:       24,
			Edits:       18,
			SourceReads: 24,
			SourceEdits: 18,
			TopFiles: []string{
				"web/src/cart/CartPage.tsx",
				"web/src/checkout/CheckoutPage.tsx",
				"web/src/products/ProductList.tsx",
			},
			Paths: []string{
				"web/src/cart/CartPage.tsx",
				"web/src/cart/cartStore.ts",
				"web/src/cart/CartPage.test.tsx",
				"web/src/checkout/CheckoutPage.tsx",
				"web/src/checkout/discountCodes.ts",
				"web/src/products/ProductList.tsx",
				"web/src/products/ProductDetail.tsx",
			},
			RelatedSessions: []contracts.RepoAnalysisSessionRef{
				{ID: "s1", Timestamp: "2026-07-09T12:00:00Z"},
				{ID: "s2", Timestamp: "2026-07-09T11:00:00Z"},
				{ID: "s3", Timestamp: "2026-07-09T10:00:00Z"},
				{ID: "s4", Timestamp: "2026-07-09T09:00:00Z"},
				{ID: "s5", Timestamp: "2026-07-09T08:00:00Z"},
			},
		}},
		Sessions: []contracts.RepoAnalysisSession{
			{ID: "s1", Prompts: []string{"Add cart page with line items and subtotal"}},
			{ID: "s2", Prompts: []string{"Add quantity and remove controls for cart items"}},
			{ID: "s3", Prompts: []string{"Add checkout page with form validation"}},
			{ID: "s4", Prompts: []string{"Add discount code validation and application at checkout"}},
			{ID: "s5", Prompts: []string{"Add product detail page to storefront"}},
		},
		Files: []contracts.RepoAnalysisFile{
			{Path: "web/src/cart/CartPage.tsx", Kind: "source"},
			{Path: "web/src/cart/cartStore.ts", Kind: "source"},
			{Path: "web/src/cart/CartPage.test.tsx", Kind: "test"},
			{Path: "web/src/checkout/CheckoutPage.tsx", Kind: "source"},
			{Path: "web/src/checkout/discountCodes.ts", Kind: "source"},
			{Path: "web/src/products/ProductList.tsx", Kind: "source"},
			{Path: "web/src/products/ProductDetail.tsx", Kind: "source"},
		},
	}

	candidates := buildCandidates(bundle)
	if len(candidates) < 2 {
		t.Fatalf("candidate count = %d, want at least 2", len(candidates))
	}

	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.subsystem.Name)
	}
	joined := strings.Join(names, " | ")
	if !strings.Contains(joined, "[cart]") {
		t.Fatalf("expected cart split in %q", joined)
	}
	if !strings.Contains(joined, "[checkout]") {
		t.Fatalf("expected checkout split in %q", joined)
	}
}

func TestParseTopicResultsRecoversPartialArray(t *testing.T) {
	raw := `[{"name":"CLI Auth Flow","summary":"s","confidence":0.9,"when_to_use":["a"],"prompt_keywords":["auth"]},`

	got, err := parseTopicResults(raw)
	if err == nil {
		t.Fatalf("expected partial parse error")
	}
	if len(got) != 1 {
		t.Fatalf("recovered %d items, want 1", len(got))
	}
	if got[0].Name != "CLI Auth Flow" {
		t.Fatalf("first name = %q, want %q", got[0].Name, "CLI Auth Flow")
	}
}

func TestParseTopicResultsEmptyResponse(t *testing.T) {
	_, err := parseTopicResults("")
	if err == nil {
		t.Fatalf("expected error for empty response")
	}
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("error = %q, want unexpected end of JSON input", err)
	}
}

func TestFallbackTopicResultBuildsUsableContext(t *testing.T) {
	candidate := topicCandidate{
		subsystem: contracts.RepoAnalysisSubsystem{
			Name:           "backend/server",
			Sessions:       4,
			TopFiles:       []string{"backend/server/repo_handlers.go", "backend/server/server.go"},
			Classification: []string{"high_context", "low_test_context"},
		},
		prompts:      []string{"fix repo analyze topic parsing for backend server routes"},
		tests:        []string{"backend/server/repos_test.go"},
		topReadFiles: []string{"backend/model/repo.go", "backend/server/repo_handlers.go"},
		seenWith:     []seenWithEntry{{File: "backend/db/db.go", Sessions: 2}},
		fileClassifications: map[string][]string{
			"backend/model/repo.go": {"context_tax"},
		},
		readBeforeEditHints: []string{"Read backend/model/repo.go before editing backend/server/repo_handlers.go"},
		testTouchSignal:     "tests used as spec",
	}

	got := fallbackTopicResult(candidate)
	if got.Name == "" {
		t.Fatalf("expected fallback name")
	}
	if len(got.ImportantFiles.EditTargets) == 0 {
		t.Fatalf("expected edit targets")
	}
	if got.Tests.Signal != "tests used as spec" {
		t.Fatalf("test signal = %q", got.Tests.Signal)
	}
	if len(got.StartHere) == 0 {
		t.Fatalf("expected start_here entries")
	}
	if len(got.RiskFlags) == 0 {
		t.Fatalf("expected risk flags")
	}
}

func TestMaterializeTopicContextsAllowsSubsetOfGroups(t *testing.T) {
	groupByID := map[string]topicCandidate{
		"backend_server": {
			subsystem: contracts.RepoAnalysisSubsystem{
				Sessions:    4,
				SourceEdits: 3,
				TestEdits:   1,
			},
			readFiles: 7,
		},
		"cli_cmd": {
			subsystem: contracts.RepoAnalysisSubsystem{
				Sessions:    2,
				SourceEdits: 1,
				TestEdits:   0,
			},
			readFiles: 2,
		},
	}

	results := []llmTopicResult{{
		GroupIDs:       []string{"backend_server"},
		Name:           "Repo Analysis Handlers",
		Summary:        "Covers repo-analysis API handler work.",
		Confidence:     0.82,
		WhenToUse:      []string{"Editing repo analysis endpoints"},
		PromptKeywords: []string{"repo analysis", "handlers"},
	}}

	topics := materializeTopicContexts(results, groupByID, []string{"backend_server", "cli_cmd"})
	if len(topics) != 1 {
		t.Fatalf("topic count = %d, want 1", len(topics))
	}
	if topics[0].Evidence.Sessions != 4 {
		t.Fatalf("sessions = %d, want 4", topics[0].Evidence.Sessions)
	}
	if topics[0].Evidence.EditedFiles != 4 {
		t.Fatalf("edited files = %d, want 4", topics[0].Evidence.EditedFiles)
	}
	if topics[0].Evidence.ReadFiles != 7 {
		t.Fatalf("read files = %d, want 7", topics[0].Evidence.ReadFiles)
	}
}

func TestMaterializeTopicContextsPreservesStructuredGuidance(t *testing.T) {
	groups := map[string]topicCandidate{"ui": {subsystem: contracts.RepoAnalysisSubsystem{Sessions: 3}}}
	topics := materializeTopicContexts([]llmTopicResult{{
		GroupIDs:   []string{"ui"},
		Name:       "Shared UI Models",
		Summary:    "Covers shared UI model changes.",
		Confidence: 0.9,
		KnownWorkflows: []llmGuidanceItem{{
			Text: "Propagate canonical model changes", Steps: []string{"Edit model.go", "Update api.ts"}, Files: []string{"model.go", "api.ts"},
		}},
		AvoidWastingTime: []llmGuidanceItem{{Text: "Do not edit generated types", Severity: "warning", Files: []string{"generated.ts"}}},
	}}, groups, []string{"ui"})
	if len(topics) != 1 || len(topics[0].KnownWorkflows) != 1 || len(topics[0].AvoidWastingTime) != 1 {
		t.Fatalf("structured guidance missing: %#v", topics)
	}
	workflow := topics[0].KnownWorkflows[0]
	if workflow.ID == "" || len(workflow.Steps) != 2 || len(workflow.Files) != 2 {
		t.Fatalf("workflow = %#v", workflow)
	}
	warning := topics[0].AvoidWastingTime[0]
	if warning.ID == "" || warning.Severity != "warning" || len(warning.Files) != 1 {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestMaterializeTopicContextsSkipsLowConfidenceTopics(t *testing.T) {
	groupByID := map[string]topicCandidate{
		"backend_server": {
			subsystem: contracts.RepoAnalysisSubsystem{Sessions: 3},
			readFiles: 4,
		},
	}

	topics := materializeTopicContexts([]llmTopicResult{{
		GroupIDs:       []string{"backend_server"},
		Name:           "Noisy Topic Guess",
		Summary:        "Weak evidence topic.",
		Confidence:     0.29,
		WhenToUse:      []string{"Maybe relevant"},
		PromptKeywords: []string{"noise"},
	}}, groupByID, []string{"backend_server"})

	if len(topics) != 0 {
		t.Fatalf("topic count = %d, want 0", len(topics))
	}
}

func TestBuildTopicPromptPreservesCandidateBoundaries(t *testing.T) {
	prompt := prompts.BuildTopicPrompt("[]", "")
	checks := []string{
		"Return exactly one topic object per input group",
		"Judge recurrence across the group's whole prompt list, never from one prompt in isolation",
		"Every topic object must include group_ids with exactly that input group_id.",
		"If you are unsure, omit the topic instead of inventing a generic one.",
		"Create topics per domain area",
		"Use the whole group/bundle context when selecting files for a topic.",
		"Do not merge groups or split a group",
		"Prefer area-focused topics over layer buckets",
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestBuildCandidateFromSourcesRequiresMultipleIndependentSources(t *testing.T) {
	sources := map[string]contracts.RepoAnalysisSource{
		"s1": {ID: "s1", SourceType: "session", Prompts: []string{"create profile page"}, ChangedFiles: []string{"web/ProfilePage.jsx"}},
	}
	if _, ok := buildCandidateFromSources(candidateGroup{CandidateID: "profile", SourceIDs: []string{"s1"}}, sources, contracts.RepoAnalysisBundle{}); ok {
		t.Fatal("isolated source must not create a topic candidate")
	}
}

func TestCandidateSourcesUsesOnlySessions(t *testing.T) {
	sources := candidateSources(contracts.RepoAnalysisBundle{Sources: []contracts.RepoAnalysisSource{
		{ID: "session", SourceType: "session"},
		{ID: "commit", SourceType: "commit"},
	}})
	if len(sources) != 1 || sources["session"].ID != "session" {
		t.Fatalf("candidate sources = %#v, want session source only", sources)
	}
}

func TestBuildCandidateFromSourcesScoresStructuralSupport(t *testing.T) {
	sources := map[string]contracts.RepoAnalysisSource{}
	for i, id := range []string{"s1", "s2", "s3", "s4"} {
		sources[id] = contracts.RepoAnalysisSource{
			ID: id, SourceType: "session", AuthorID: id,
			Prompts: []string{"create profile page"}, ChangedFiles: []string{"web/ProfilePage.jsx", "web/profile.css"},
			Timestamp: "2026-07-0" + string(rune('1'+i)) + "T00:00:00Z",
		}
	}
	candidate, ok := buildCandidateFromSources(candidateGroup{CandidateID: "profile", SourceIDs: []string{"s1", "s2", "s3", "s4"}}, sources, contracts.RepoAnalysisBundle{
		Files: []contracts.RepoAnalysisFile{{Path: "web/ProfilePage.jsx", Kind: "source"}, {Path: "web/profile.css", Kind: "source"}},
	})
	if !ok || candidate.supportLevel != "strong" {
		t.Fatalf("candidate = %#v, want strong", candidate)
	}
	if len(candidate.repeatedEditedFiles) != 2 || candidate.independentAuthors != 4 {
		t.Fatalf("structural evidence = %#v authors=%d", candidate.repeatedEditedFiles, candidate.independentAuthors)
	}
}

func TestBuildCandidateFromSourcesKeepsWeakMultiSourceGroup(t *testing.T) {
	sources := map[string]contracts.RepoAnalysisSource{
		"s1": {ID: "s1", SourceType: "session", Prompts: []string{"profile avatar"}, ChangedFiles: []string{"web/avatar.jsx"}},
		"s2": {ID: "s2", SourceType: "commit", Prompts: []string{"profile biography"}, ChangedFiles: []string{"web/bio.jsx"}},
	}
	candidate, ok := buildCandidateFromSources(candidateGroup{CandidateID: "profile", SourceIDs: []string{"s1", "s2"}}, sources, contracts.RepoAnalysisBundle{})
	if !ok || candidate.supportLevel != "weak" {
		t.Fatalf("candidate = %#v, want retained weak group", candidate)
	}
}

func TestParseCandidateDiscoveryResponseRecoversCompleteGroupsFromTruncation(t *testing.T) {
	response, err := parseCandidateDiscoveryResponse(`{"groups":[["s1","s2"],["s3","s4"],["s5"`)
	if err != nil {
		t.Fatalf("parseCandidateDiscoveryResponse: %v", err)
	}
	if len(response.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want two complete recovered groups", response.Candidates)
	}
	if got := strings.Join(response.Candidates[1].SourceIDs, ","); got != "s3,s4" {
		t.Fatalf("second recovered group = %q", got)
	}
}

func TestParseCandidateDiscoveryResponseIgnoresTrailingFence(t *testing.T) {
	response, err := parseCandidateDiscoveryResponse("{\"new\":[[\"s1\",\"s2\"]],\"unassigned\":[]}\n```")
	if err != nil || len(response.Candidates) != 1 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestApplyCandidateTurnCarriesScopeIntoNextBatch(t *testing.T) {
	groups, loose := applyCandidateTurn(0, []candidateSourceInput{{ID: "s1"}, {ID: "s2"}}, candidateDiscoveryResponse{NewGroups: [][]string{{"s1", "s2"}}}, nil, nil, nil)
	if len(groups) != 1 || len(loose) != 0 {
		t.Fatalf("first turn groups=%#v loose=%#v", groups, loose)
	}
	groups, loose = applyCandidateTurn(1, []candidateSourceInput{{ID: "s3"}}, candidateDiscoveryResponse{Assignments: []candidateAssignment{{TopicID: groups[0].CandidateID, SourceIDs: []string{"s3"}}}}, groups, loose, nil)
	if len(groups) != 1 || len(groups[0].SourceIDs) != 3 || len(loose) != 0 {
		t.Fatalf("second turn groups=%#v loose=%#v", groups, loose)
	}
}

func TestParseCandidateDiscoveryResponseSupportsAssignNewAndSplit(t *testing.T) {
	response, err := parseCandidateDiscoveryResponse(`{"assign":[{"topic_id":"auth","source_ids":["s1"]}],"new":[["s2","s3"]],"split":[{"topic_id":"web","groups":[["s4","s5"],["s6","s7"]]}],"unassigned":["s8"]}`)
	if err != nil {
		t.Fatal(err)
	}
	foundExisting := false
	for _, candidate := range response.Candidates {
		foundExisting = foundExisting || candidate.ExistingTopicID == "auth"
	}
	if len(response.Candidates) != 4 || !foundExisting {
		t.Fatalf("candidates = %#v", response.Candidates)
	}
	if len(response.UnassignedSourceIDs) != 1 || response.UnassignedSourceIDs[0] != "s8" {
		t.Fatalf("unassigned = %#v", response.UnassignedSourceIDs)
	}
}

func TestCompactTopicRepoContextIncludesSummaryAndBoundsDocs(t *testing.T) {
	context := compactTopicRepoContext(contracts.RepoAnalysisBundle{Repo: contracts.RepoAnalysisRepo{Name: "repoguide"}}, map[string]string{"README.md": strings.Repeat("x", 4000)})
	if !strings.Contains(context, "Repository: repoguide") || !strings.Contains(context, "Telemetry summary:") {
		t.Fatalf("context missing repo summary: %q", context)
	}
	if len(context) > 3000 {
		t.Fatalf("context too large: %d", len(context))
	}
}

func TestTopicCandidatePromptUsesCompactOutput(t *testing.T) {
	prompt := prompts.BuildTopicCandidatePrompt(`[]`, `[]`)
	if !strings.Contains(prompt, `"assign":[{"topic_id"`) || !strings.Contains(prompt, `"new":[["source-id-2"`) || !strings.Contains(prompt, `"split":[{`) || !strings.Contains(prompt, "Do not include reasons") {
		t.Fatalf("candidate prompt is not compact: %s", prompt)
	}
}

func TestCandidateInputBatchesCoverEverySourceOnce(t *testing.T) {
	inputs := make([]candidateSourceInput, 205)
	for i := range inputs {
		inputs[i].ID = fmt.Sprintf("s-%d", i)
	}
	batches := candidateInputBatches(inputs, 50)
	if len(batches) != 5 {
		t.Fatalf("batch count = %d, want 5", len(batches))
	}
	seen := map[string]int{}
	for _, batch := range batches {
		if len(batch) > 50 {
			t.Fatalf("batch size = %d, want <= 50", len(batch))
		}
		for _, source := range batch {
			seen[source.ID]++
		}
	}
	if len(seen) != len(inputs) {
		t.Fatalf("covered %d sources, want %d", len(seen), len(inputs))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("source %s appeared %d times", id, count)
		}
	}
}
