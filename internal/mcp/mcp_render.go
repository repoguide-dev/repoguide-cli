package mcp

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
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
		sb.WriteString("\nPrior workflow: ")
		sb.WriteString(strings.Join(ctx.KnownWorkflows, "; "))
		sb.WriteString(".\n")
	}

	if len(ctx.AvoidWastingTime) > 0 {
		sb.WriteString("\nAvoid: ")
		sb.WriteString(strings.Join(ctx.AvoidWastingTime, "; "))
		sb.WriteString(".\n")
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
			fmt.Fprintf(&sb, "- %s\n", n)
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
