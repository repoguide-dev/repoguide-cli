package mcp

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/repoguide/repoguide-core/contracts/v1"
)

// renderBootstrapContext produces the text shown to the agent after topic selection.
func renderBootstrapContext(ctx *MCPTopicContext, maxFiles int) string {
	if maxFiles <= 0 {
		maxFiles = 6
	}

	startHere := make([]string, 0, len(ctx.StartHere))
	for _, f := range ctx.StartHere {
		startHere = append(startHere, f.Path)
	}

	seen := make(map[string]struct{}, len(startHere))
	for _, p := range startHere {
		seen[p] = struct{}{}
	}
	alsoCheck := make([]string, 0)
	for _, p := range ctx.ImportantFiles.ReferenceFiles {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			alsoCheck = append(alsoCheck, p)
		}
	}
	for _, p := range ctx.Tests.StartWith {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			alsoCheck = append(alsoCheck, p)
		}
	}

	// cap total to maxFiles
	if remaining := maxFiles - len(startHere); remaining < len(alsoCheck) {
		if remaining < 0 {
			remaining = 0
			startHere = startHere[:maxFiles]
		}
		alsoCheck = alsoCheck[:remaining]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Selected RepoGuide topic: %s.\n", ctx.Name)

	if len(startHere) > 0 {
		sb.WriteString("\nStart here:\n")
		for _, p := range startHere {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
	}

	if len(alsoCheck) > 0 {
		sb.WriteString("\nAlso check:\n")
		for _, p := range alsoCheck {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
	}

	if len(ctx.KnownWorkflows) > 0 {
		sb.WriteString("\nPrior workflows:\n")
		for _, item := range ctx.KnownWorkflows {
			fmt.Fprintf(&sb, "- %s\n", item.Text)
		}
	}

	if len(ctx.AvoidWastingTime) > 0 {
		sb.WriteString("\nAvoid:\n")
		for _, item := range ctx.AvoidWastingTime {
			fmt.Fprintf(&sb, "- %s\n", item.Text)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// renderLocalBootstrap produces bootstrap text from the local filesystem fallback.
func renderLocalBootstrap(topic repoguideTopic, files []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Selected topic: %s.\n", topic.Title)
	if topic.Summary != "" {
		fmt.Fprintf(&sb, "%s\n", topic.Summary)
	}
	if len(files) > 0 {
		sb.WriteString("\nStart here:\n")
		for _, f := range files {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}
	for _, p := range buildPriorPatterns(topic) {
		fmt.Fprintf(&sb, "\n%s\n", p)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderTestContext produces test guidance text for a topic.
func renderTestContext(ctx *MCPTopicContext, files []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Test context for topic: %s.\n", ctx.Name)

	if ctx.Tests.Signal != "" {
		fmt.Fprintf(&sb, "Signal: %s.\n", ctx.Tests.Signal)
	}

	if len(ctx.Tests.StartWith) > 0 {
		sb.WriteString("\nStart with:\n")
		for _, t := range ctx.Tests.StartWith {
			fmt.Fprintf(&sb, "- %s\n", t)
		}
	}

	if len(ctx.Tests.Notes) > 0 {
		sb.WriteString("\nNotes:\n")
		for _, n := range ctx.Tests.Notes {
			fmt.Fprintf(&sb, "- %s\n", n.Text)
		}
	}
	if len(ctx.Tests.Commands) > 0 {
		sb.WriteString("\nObserved validation commands:\n")
		for _, command := range ctx.Tests.Commands {
			fmt.Fprintf(&sb, "- %s\n", command)
		}
	}

	// per-file test hints by name matching
	if len(files) > 0 && len(ctx.ImportantFiles.TestFiles) > 0 {
		perFile := map[string][]string{}
		for _, f := range files {
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
			for _, t := range ctx.ImportantFiles.TestFiles {
				if strings.Contains(strings.ToLower(t), base) {
					perFile[f] = append(perFile[f], t)
				}
			}
		}
		if len(perFile) > 0 {
			sb.WriteString("\nRelated tests by file:\n")
			for f, tests := range perFile {
				fmt.Fprintf(&sb, "- %s → %s\n", f, strings.Join(tests, ", "))
			}
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// renderSelectedAdvice folds advice items into plain prose instead of leaving
// them as a raw struct list for the caller to JSON-dump.
func renderSelectedAdvice(items []contracts.AdviceItem) string {
	if len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Advice:\n")
	for _, item := range items {
		fmt.Fprintf(&sb, "- %s", item.Text)
		if item.Severity != "" {
			fmt.Fprintf(&sb, " [%s]", item.Severity)
		}
		sb.WriteByte('\n')
		for _, step := range item.Steps {
			fmt.Fprintf(&sb, "  - %s\n", step)
		}
		if len(item.Files) > 0 {
			fmt.Fprintf(&sb, "  Files: %s\n", strings.Join(item.Files, ", "))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderTopicClarification(reason, question string, matches []contracts.TopicMatch) string {
	var sb strings.Builder
	sb.WriteString("Task-to-topic match\n")
	for _, match := range matches {
		name := match.Name
		if name == "" {
			name = match.TopicID
		}
		if match.Confidence > 0 {
			fmt.Fprintf(&sb, "- %.0f%% %s (`%s`)\n", match.Confidence*100, name, match.TopicID)
		} else {
			fmt.Fprintf(&sb, "- %s (`%s`)\n", name, match.TopicID)
		}
	}
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&sb, "\n%s\n", strings.TrimSpace(reason))
	}
	if strings.TrimSpace(question) == "" {
		question = "Which topic should RepoGuide use?"
	}
	fmt.Fprintf(&sb, "\n%s Call `repoguide_get_repo_experience` again with the chosen `topic_id`.", strings.TrimSpace(question))
	return strings.TrimSpace(sb.String())
}

// renderFullTopicContext is the explicit escape hatch for the complete stored
// topic, test, and search package. The default task route never calls it.
func renderFullTopicContext(ctx *MCPTopicContext, search *MCPSearchContext) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Full topic context: %s\nSummary: %s\n", ctx.Name, ctx.Summary)
	if len(ctx.WhenToUse) > 0 {
		sb.WriteString("\nWhen to use\n")
		for _, item := range ctx.WhenToUse {
			fmt.Fprintf(&sb, "- %s\n", item)
		}
	}
	if len(ctx.PromptKeywords) > 0 {
		fmt.Fprintf(&sb, "\nPrompt keywords\n- %s\n", strings.Join(ctx.PromptKeywords, ", "))
	}
	if len(ctx.StartHere) > 0 {
		sb.WriteString("\nStart here\n")
		for _, file := range ctx.StartHere {
			fmt.Fprintf(&sb, "- %s", file.Path)
			if file.Role != "" {
				fmt.Fprintf(&sb, " — %s", file.Role)
			}
			if file.Why != "" {
				fmt.Fprintf(&sb, ": %s", file.Why)
			}
			sb.WriteByte('\n')
		}
	}
	renderPaths := func(heading string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Fprintf(&sb, "\n%s\n", heading)
		for _, path := range paths {
			fmt.Fprintf(&sb, "- %s\n", path)
		}
	}
	renderPaths("Edit targets", ctx.ImportantFiles.EditTargets)
	renderPaths("Reference files", ctx.ImportantFiles.ReferenceFiles)
	renderPaths("Test files", ctx.ImportantFiles.TestFiles)
	renderPaths("Cross-cutting files", ctx.ImportantFiles.CrossCuttingFiles)
	renderGuidance := func(heading string, items []contracts.MCPTopicGuidanceItem) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&sb, "\n%s\n", heading)
		for _, item := range items {
			fmt.Fprintf(&sb, "- [%s] %s", item.ID, item.Text)
			if item.Severity != "" {
				fmt.Fprintf(&sb, " [%s]", item.Severity)
			}
			fmt.Fprintf(&sb, " (support %d, confidence %.0f%%)\n", item.SupportCount, item.Confidence*100)
			for _, step := range item.Steps {
				fmt.Fprintf(&sb, "  - %s\n", step)
			}
			if len(item.Files) > 0 {
				fmt.Fprintf(&sb, "  Files: %s\n", strings.Join(item.Files, ", "))
			}
		}
	}
	renderGuidance("Workflows", ctx.KnownWorkflows)
	renderGuidance("Avoid", ctx.AvoidWastingTime)
	renderGuidance("Scope boundaries", ctx.ScopeBoundaries)
	renderGuidance("Risks", ctx.RiskFlags)
	if tests := renderTestContext(ctx, nil); tests != "" {
		sb.WriteString("\n\n" + tests)
	}
	if search != nil {
		if rendered := renderSearchContext(ctx.ID, search); rendered != "" {
			sb.WriteString("\n\n" + rendered)
		}
	}
	return strings.TrimSpace(sb.String())
}

// renderSearchContext produces search guidance text.
func renderSearchContext(topicID string, sc *MCPSearchContext) string {
	var sb strings.Builder
	if topicID != "" {
		fmt.Fprintf(&sb, "Search context for topic: %s\n", topicID)
	} else {
		sb.WriteString("Search context.\n")
	}

	if len(sc.SearchHeavyTargets) > 0 {
		sb.WriteString("\nHigh-value search targets:\n")
		targets := sc.SearchHeavyTargets
		if len(targets) > 8 {
			targets = targets[:8]
		}
		for _, t := range targets {
			fmt.Fprintf(&sb, "- %s\n", t.Path)
			if t.UseFor != "" {
				fmt.Fprintf(&sb, "  Use for %s\n", t.UseFor)
			}
			if len(t.TopQueries) > 0 {
				sanitized := make([]string, 0, len(t.TopQueries))
				for _, q := range t.TopQueries {
					sanitized = append(sanitized, sanitizeSearchQuery(q))
				}
				fmt.Fprintf(&sb, "  Suggested queries: %s\n", strings.Join(sanitized, ", "))
			}
		}
	}

	if len(sc.AmbiguousSearches) > 0 {
		sb.WriteString("\nAvoid broad queries:\n")
		limit := len(sc.AmbiguousSearches)
		if limit > 5 {
			limit = 5
		}
		for _, a := range sc.AmbiguousSearches[:limit] {
			fmt.Fprintf(&sb, "- %s\n", a.Query)
		}
	}

	if len(sc.SearchHeavyTargets) == 0 && len(sc.AmbiguousSearches) == 0 {
		sb.WriteString("No search friction patterns recorded for this topic.\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// sanitizeSearchQuery removes regexp metacharacters that cause grep errors, preserving | for alternation.
var reQueryMeta = regexp.MustCompile(`[\\^$\[\]{}]`)

func sanitizeSearchQuery(q string) string {
	return reQueryMeta.ReplaceAllString(q, "")
}
