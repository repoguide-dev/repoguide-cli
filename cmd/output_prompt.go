package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/repoguide/repoguide-cli/internal"
)

// promptOneLiner returns a single-line version of the prompt text for table display.
func promptOneLiner(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}

func promptBlockTotalReads(b internal.PromptBlock) int {
	n := 0
	for _, c := range b.ReadFileCountsBefore {
		n += c
	}
	for _, c := range b.ReadFileCountsAfter {
		n += c
	}
	return n
}

func promptBlockUniqueFiles(b internal.PromptBlock) int {
	seen := make(map[string]struct{}, len(b.ReadFiles)+len(b.EditedFiles))
	for _, f := range b.ReadFiles {
		seen[f] = struct{}{}
	}
	for _, f := range b.EditedFiles {
		seen[f] = struct{}{}
	}
	return len(seen)
}

func buildPromptDetailContent(b internal.PromptBlock, num int, pricing *internal.ModelPricing, session internal.SessionSummary, expanded bool) string {
	shorten := func(p string) string {
		s := shortFilePath(p, session.RepoRoot)
		if s == p {
			s = shortFilePath(p, session.Cwd)
		}
		return s
	}

	reads := promptBlockTotalReads(b)
	edits := b.EditCount
	files := promptBlockUniqueFiles(b)

	fullText := b.FullText
	if fullText == "" {
		fullText = b.Text
	}
	displayText := fullText
	lines := strings.Split(fullText, "\n")
	if !expanded && len(lines) > 5 {
		displayText = strings.Join(lines[:5], "\n") + "\n" + muted.Render("-- expand (e)")
	}

	out := []string{
		titleStyle.Render(fmt.Sprintf("Prompt #%d", num)),
		"",
		headStyle.Render("User prompt"),
		promptTextStyle.Render(displayText),
		"",
		headStyle.Render("Activity"),
		fmt.Sprintf("  Searches   %d", len(b.Searches)),
		fmt.Sprintf("  Reads      %d", reads),
		fmt.Sprintf("  Edits      %d", edits),
		fmt.Sprintf("  Files      %d", files),
	}
	if len(b.Searches) > 0 {
		out = append(out, "", headStyle.Render("Search trace"))
		for _, search := range b.Searches {
			result := muted.Render("dead end")
			if search.EditTarget != "" {
				target := shorten(search.EditTarget)
				result = fmt.Sprintf("%d reads %s edit %s", search.ReadsBeforeEdit, muted.Render("→"), renderPathText(target))
				if !search.FoundViaSearch {
					result += " " + muted.Render("(target not read)")
				}
			}
			query := search.Query
			if strings.TrimSpace(query) == "" {
				query = "(unknown query)"
			}
			out = append(out, fmt.Sprintf(
				"  %s %s %s",
				searchQueryStyle.Render("“"+formatSearchQuery(query)+"”"),
				muted.Render("→"),
				result,
			))
		}
	}
	if b.TokenUsage != nil {
		out = append(out, "", headStyle.Render("Costs"))
		out = append(out,
			fmt.Sprintf("  Reads                  %s", formatTokensK(b.TokenUsage.InputTokens)),
			fmt.Sprintf("  Cached reads           %s", formatTokensK(b.TokenUsage.CacheReadTokens)),
			fmt.Sprintf("  Output                 %s", formatTokensK(b.TokenUsage.OutputTokens)),
			fmt.Sprintf("  Write                  %s", formatTokensK(b.TokenUsage.CacheWriteTokens)),
		)
		if pricing != nil {
			cost := internal.EstimateCostUSD(*pricing, b.TokenUsage)
			if cost > 0 {
				out = append(out, fmt.Sprintf("  Estimated dollar cost  $%.4f", cost))
			}
		}
		if b.DurationSec > 0 {
			secs := int(b.DurationSec)
			out = append(out, fmt.Sprintf("  Duration               %dm %ds", secs/60, secs%60))
		}
	}
	if b.TokenUsage == nil && b.DurationSec > 0 {
		secs := int(b.DurationSec)
		out = append(out, fmt.Sprintf("  Duration   %dm %ds", secs/60, secs%60))
	}

	type fileInfo struct {
		path   string
		reads  int
		edited bool
	}
	seen := map[string]*fileInfo{}
	for f, c := range b.ReadFileCountsBefore {
		if _, ok := seen[f]; !ok {
			seen[f] = &fileInfo{path: f}
		}
		seen[f].reads += c
	}
	for f, c := range b.ReadFileCountsAfter {
		if _, ok := seen[f]; !ok {
			seen[f] = &fileInfo{path: f}
		}
		seen[f].reads += c
	}
	for _, f := range b.EditedFiles {
		if _, ok := seen[f]; !ok {
			seen[f] = &fileInfo{path: f}
		}
		seen[f].edited = true
	}

	if len(seen) > 0 {
		out = append(out, "", headStyle.Render("Files touched"))
		type entry struct {
			path   string
			reads  int
			edited bool
		}
		entries := make([]entry, 0, len(seen))
		for _, fi := range seen {
			entries = append(entries, entry{fi.path, fi.reads, fi.edited})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].reads != entries[j].reads {
				return entries[i].reads > entries[j].reads
			}
			return entries[i].path < entries[j].path
		})
		paths := make([]string, 0, len(entries))
		for _, e := range entries {
			paths = append(paths, shorten(e.path))
		}
		pathWidth := fileListPathWidth(paths)
		for _, e := range entries {
			short := shorten(e.path)
			var ops []string
			if e.reads > 0 {
				ops = append(ops, fmt.Sprintf("read ×%d", e.reads))
			}
			if e.edited {
				ops = append(ops, "edit")
			}
			out = append(out, formatFileListLine(short, pathWidth, strings.Join(ops, "  ")))
		}
	}

	out = append(out, "", headStyle.Render("Result"))
	if len(b.EditedFiles) > 0 {
		out = append(out, fmt.Sprintf("  %d file(s) edited", len(b.EditedFiles)))
	} else {
		out = append(out, "  No files edited")
	}

	return strings.Join(out, "\n")
}

func formatSearchQuery(query string) string {
	query = strings.TrimSpace(query)
	query = strings.ReplaceAll(query, `\\`, `\`)
	replacer := strings.NewReplacer(
		`\|`, " | ",
		`\.`, ".",
		`\"`, `"`,
	)
	query = replacer.Replace(query)
	parts := strings.Split(query, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	query = strings.Join(parts, "  OR  ")
	return strings.TrimSpace(strings.TrimSuffix(query, `\`))
}
