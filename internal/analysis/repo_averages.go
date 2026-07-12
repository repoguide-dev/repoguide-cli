package analysis

import "github.com/repoguide/repoguide-core/model"

// RepoStartCost summarizes, across every session that made at least one
// edit, how much navigation happened before that first edit. It's the
// repo-wide baseline a single worst-session case study can be read against.
type RepoStartCost struct {
	Sessions         int     `json:"sessions"`
	AvgReads         float64 `json:"avg_reads"`
	AvgContextTokens float64 `json:"avg_context_tokens"`
	AvgToolCalls     float64 `json:"avg_tool_calls"`
}

// AverageStartCost computes RepoStartCost from raw session events.
func AverageStartCost(stored []model.RepoSessionEvents) RepoStartCost {
	var readsSum, toolsSum float64
	var ctxSum float64
	n := 0
	for _, s := range stored {
		m := analyzeSessionEvents(s.Events)
		editIdx := -1
		for i, b := range m.PromptBlocks {
			if len(b.EditedFiles) > 0 {
				editIdx = i
				break
			}
		}
		if editIdx == -1 {
			continue
		}
		readsSum += float64(m.PromptBlocks[editIdx].ReadsBeforeFirstEdit)
		ctxSum += promptContextBefore(m.PromptBlocks, editIdx)
		toolsSum += float64(toolCallsBeforeFirstEdit(s.Events))
		n++
	}
	if n == 0 {
		return RepoStartCost{}
	}
	return RepoStartCost{
		Sessions:         n,
		AvgReads:         safeDivide(readsSum, float64(n)),
		AvgContextTokens: safeDivide(ctxSum, float64(n)),
		AvgToolCalls:     safeDivide(toolsSum, float64(n)),
	}
}

func toolCallsBeforeFirstEdit(events []model.SessionEvent) int {
	count := 0
	for _, ev := range events {
		if len(ev.WritePaths) > 0 {
			break
		}
		if ev.Kind == "tool_call" {
			count++
		}
	}
	return count
}
