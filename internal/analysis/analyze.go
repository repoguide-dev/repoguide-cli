package analysis

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/repoguide/repoguide-cli/internal/session"
	"github.com/repoguide/repoguide-core/model"
)

var searchToolNames = map[string]bool{"WebSearch": true, "WebFetch": true, "Grep": true}
var searchShellCmds = map[string]bool{"grep": true, "rg": true, "ag": true, "ack": true, "find": true}
var shellSearchQueryPattern = regexp.MustCompile(`(?:^|[\s;&|])(?:rg|grep|ag|ack)\s+(?:-[^\s]+\s+)*(?:"([^"]+)"|'([^']+)'|([^\s]+))`)

// xmlTagPattern strips auto-injected wrapper tags (e.g. <environment_context>,
// <cwd>, <shell>) from prompt text so they don't get shown as if they were
// the user's actual ask.
var xmlTagPattern = regexp.MustCompile(`<[^>]+>`)

func toSessionUsage(usage *model.TokenUsage) *session.TokenUsage {
	if usage == nil {
		return nil
	}
	return &session.TokenUsage{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		Cumulative:       usage.Cumulative,
	}
}

func estimateCost(modelName string, usage *model.TokenUsage) float64 {
	pricing, ok := session.LookupModelPricing(modelName)
	if !ok {
		return 0
	}
	return session.EstimateCostUSD(pricing, toSessionUsage(usage))
}

// estimateInputCost is the input+cache-token slice of estimateCost (mirrors
// effectiveCtx's token set) - excludes output tokens, since those aren't
// affected by reducing how much context an agent loads before editing.
func estimateInputCost(modelName string, usage *model.TokenUsage) float64 {
	pricing, ok := session.LookupModelPricing(modelName)
	if !ok || usage == nil {
		return 0
	}
	const m = 1_000_000.0
	return float64(usage.InputTokens)*pricing.InputPerMTokUSD/m +
		float64(usage.CacheReadTokens)*pricing.CacheReadPerMTokUSD/m +
		float64(usage.CacheWriteTokens)*pricing.CacheWritePerMTokUSD/m
}

func analyzeSessionEvents(events []model.SessionEvent) sessionMetrics {
	readCounts := map[string]int{}
	writeCounts := map[string]int{}
	var metrics sessionMetrics

	var cumulative *model.TokenUsage
	var prevCumulative *model.TokenUsage
	var incremental model.TokenUsage
	firstEditSeen := false
	var errorIndices []int

	var curBlock *promptBlock
	curBlockReadSet := map[string]struct{}{}
	curBlockWriteSet := map[string]struct{}{}
	curBlockFirstEditSeen := false
	var curBlockUsage model.TokenUsage
	var activeSearches []int

	flush := func() {
		if curBlock == nil {
			return
		}
		for _, s := range curBlock.Searches {
			if s.EditTarget == "" {
				curBlock.DeadEndSearches++
			}
		}
		curBlock.ReadFiles = sortedKeys(curBlockReadSet)
		curBlock.EditedFiles = sortedKeys(curBlockWriteSet)
		if hasUsage(curBlockUsage) {
			u := curBlockUsage
			curBlock.TokenUsage = &u
		}
		if len(curBlock.EditedFiles) > 0 || curBlock.ReadsBeforeFirstEdit > 0 || len(curBlock.Searches) > 0 {
			metrics.PromptBlocks = append(metrics.PromptBlocks, *curBlock)
		}
		curBlock = nil
		curBlockReadSet = map[string]struct{}{}
		curBlockWriteSet = map[string]struct{}{}
		curBlockFirstEditSeen = false
		curBlockUsage = model.TokenUsage{}
		activeSearches = nil
	}

	for _, ev := range events {
		isSearch := ev.Kind == "tool_call" && isSearchEvent(ev)
		if len(ev.WritePaths) > 0 {
			firstEditSeen = true
			curBlockFirstEditSeen = true
		}
		switch ev.Kind {
		case "prompt":
			if strings.TrimSpace(ev.Text) != "" {
				flush()
				metrics.UserPromptCount++
				curBlock = &promptBlock{}
			}
		case "tool_call":
			metrics.ToolCallCount++
			if curBlock != nil && isSearch {
				curBlock.Searches = append(curBlock.Searches, searchTrace{Query: searchQuery(ev)})
				activeSearches = append(activeSearches, len(curBlock.Searches)-1)
			}
		case "tool_result":
			if ev.IsError {
				errorIndices = append(errorIndices, ev.Index)
			}
		case "token_usage":
			if ev.TokenUsage == nil {
				continue
			}
			if ev.TokenUsage.Cumulative {
				delta := tokenDelta(prevCumulative, ev.TokenUsage)
				cp := *ev.TokenUsage
				cumulative = &cp
				prevCumulative = &cp
				if !hasUsage(delta) {
					continue
				}
				addUsage(&incremental, delta)
				if curBlock != nil {
					addUsage(&curBlockUsage, delta)
				}
				continue
			}
			addUsage(&incremental, *ev.TokenUsage)
			if curBlock != nil {
				addUsage(&curBlockUsage, *ev.TokenUsage)
			}
		}

		for _, path := range ev.ReadPaths {
			if path == "" {
				continue
			}
			readCounts[path]++
			if curBlock == nil {
				continue
			}
			if !isSearch {
				for _, idx := range activeSearches {
					tr := &curBlock.Searches[idx]
					tr.ReadsBeforeEdit++
					if !containsStr(tr.ReadFiles, path) {
						tr.ReadFiles = append(tr.ReadFiles, path)
					}
				}
			}
			if !curBlockFirstEditSeen {
				if _, seen := curBlockReadSet[path]; !seen {
					curBlockReadSet[path] = struct{}{}
					curBlock.ReadsBeforeFirstEdit++
				}
			}
		}

		for _, path := range ev.WritePaths {
			if path == "" {
				continue
			}
			writeCounts[path]++
			if curBlock != nil {
				curBlockWriteSet[path] = struct{}{}
				for _, idx := range activeSearches {
					tr := &curBlock.Searches[idx]
					if tr.EditTarget == "" {
						tr.EditTarget = path
						tr.FoundViaSearch = containsStr(tr.ReadFiles, path)
					}
				}
				activeSearches = nil
			}
		}
		_ = firstEditSeen
	}
	flush()

	if cumulative != nil {
		metrics.TokenUsage = cumulative
	} else if hasUsage(incremental) {
		metrics.TokenUsage = &incremental
	}
	if len(readCounts) > 0 {
		metrics.ReadFileCounts = readCounts
	}
	if len(writeCounts) > 0 {
		metrics.EditFileCounts = writeCounts
	}

	if len(errorIndices) > 0 {
		// basic failure tracking (last tool call index for recovery detection)
		lastTC := -1
		for _, ev := range events {
			if ev.Kind == "tool_call" {
				lastTC = ev.Index
			}
		}
		_ = lastTC
	}

	metrics.Commands = extractSessionCommands(events)
	return metrics
}

const maxSessionCommands = 15

// commandSkipWords are leading words of commands that carry no reusable signal:
// navigation/inspection plus search commands (search behavior is modeled
// separately via searchTrace).
var commandSkipWords = map[string]bool{
	"cd": true, "ls": true, "pwd": true, "cat": true, "echo": true,
	"head": true, "tail": true, "which": true, "env": true, "printenv": true,
	"grep": true, "rg": true, "ag": true, "ack": true, "find": true,
}

// extractSessionCommands collects deduplicated shell commands run during a
// session and links each run to its tool_result (via toolCallId) to count
// failures. Failed commands are durable "this didn't work here" evidence;
// repeated successful commands are validation-workflow evidence.
func extractSessionCommands(events []model.SessionEvent) []commandStat {
	pending := map[string]string{} // toolCallId -> command text
	stats := map[string]*commandStat{}
	var order []string

	for _, ev := range events {
		switch ev.Kind {
		case "tool_call":
			text := normalizeCommandText(ev.CommandText)
			if text == "" {
				continue
			}
			st := stats[text]
			if st == nil {
				st = &commandStat{Text: text}
				stats[text] = st
				order = append(order, text)
			}
			st.Runs++
			if ev.ToolCallID != "" {
				pending[ev.ToolCallID] = text
			}
		case "tool_result":
			if !ev.IsError || ev.ToolCallID == "" {
				continue
			}
			if text, ok := pending[ev.ToolCallID]; ok {
				stats[text].Failures++
			}
		}
	}

	out := make([]commandStat, 0, len(order))
	for _, text := range order {
		out = append(out, *stats[text])
	}
	if len(out) > maxSessionCommands {
		// keep the most-run commands; stable to preserve session order on ties
		sort.SliceStable(out, func(i, j int) bool { return out[i].Runs > out[j].Runs })
		out = out[:maxSessionCommands]
	}
	return out
}

// normalizeCommandText trims and whitespace-collapses a command, dropping
// commands with no reuse value (navigation, search) and over-long one-offs.
func normalizeCommandText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	if commandSkipWords[fields[0]] {
		return ""
	}
	if len([]rune(text)) > 200 {
		return ""
	}
	return text
}

func sessionTimestamp(events []model.SessionEvent, fallback time.Time) time.Time {
	for _, ev := range events {
		if ev.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				return t
			}
		}
	}
	return fallback
}

func isSearchEvent(ev model.SessionEvent) bool {
	if searchToolNames[ev.ToolName] {
		return true
	}
	fields := strings.Fields(ev.CommandText)
	return len(fields) > 0 && searchShellCmds[fields[0]]
}

func searchQuery(ev model.SessionEvent) string {
	if q := strings.TrimSpace(ev.SearchQuery); q != "" {
		return q
	}
	if m := shellSearchQueryPattern.FindStringSubmatch(ev.CommandText); len(m) > 0 {
		for _, c := range m[1:] {
			if c != "" {
				return c
			}
		}
	}
	// No extractable query. Shell pipelines/one-liners are not queries - surfacing
	// them as "ambiguous searches" pollutes the served search context.
	cmd := strings.TrimSpace(ev.CommandText)
	if strings.ContainsAny(cmd, "|&;") || len(cmd) > 80 {
		return ""
	}
	return cmd
}

func addUsage(dst *model.TokenUsage, src model.TokenUsage) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
}

func tokenDelta(prev *model.TokenUsage, next *model.TokenUsage) model.TokenUsage {
	if prev == nil {
		return model.TokenUsage{
			InputTokens:      next.InputTokens,
			OutputTokens:     next.OutputTokens,
			CacheReadTokens:  next.CacheReadTokens,
			CacheWriteTokens: next.CacheWriteTokens,
		}
	}
	nd := func(a, b int64) int64 {
		if a <= b {
			return 0
		}
		return a - b
	}
	return model.TokenUsage{
		InputTokens:      nd(next.InputTokens, prev.InputTokens),
		OutputTokens:     nd(next.OutputTokens, prev.OutputTokens),
		CacheReadTokens:  nd(next.CacheReadTokens, prev.CacheReadTokens),
		CacheWriteTokens: nd(next.CacheWriteTokens, prev.CacheWriteTokens),
	}
}

func hasUsage(u model.TokenUsage) bool {
	return u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadTokens > 0 || u.CacheWriteTokens > 0
}

func effectiveCtx(u *model.TokenUsage) int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

func totalTok(u *model.TokenUsage) int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
