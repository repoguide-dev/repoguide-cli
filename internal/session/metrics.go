package session

import (
	"strings"
	"time"
)

func AnalyzeSessionEventLog(eventLog SessionEventLog) SessionMetrics {
	readSet := map[string]struct{}{}
	readCounts := map[string]int{}
	writeSet := map[string]struct{}{}
	editCounts := map[string]int{}
	metrics := SessionMetrics{
		EventCount: len(eventLog.Events),
	}

	var cumulativeUsage *TokenUsage
	var prevCumulativeUsage *TokenUsage
	var incrementalUsage TokenUsage
	var exploration ExplorationStats
	explorationReadSet := map[string]struct{}{}
	firstEditSeen := false
	var explorationUsage TokenUsage
	var errorIndices []int

	var curBlock *PromptBlock
	var curBlockStartTime time.Time
	var curBlockUsage TokenUsage
	curBlockReadSet := map[string]struct{}{}
	curBlockWriteSet := map[string]struct{}{}
	curBlockFirstEditSeen := false
	curBlockReadCountsBefore := map[string]int{}
	curBlockReadCountsAfter := map[string]int{}
	var activeSearches []int

	flushBlock := func(endTime time.Time) {
		if curBlock == nil {
			return
		}
		for _, search := range curBlock.Searches {
			if search.EditTarget == "" {
				curBlock.DeadEndSearches++
			}
		}
		curBlock.ReadFiles = sortedKeys(curBlockReadSet)
		curBlock.EditedFiles = sortedKeys(curBlockWriteSet)
		if len(curBlockReadCountsBefore) > 0 {
			curBlock.ReadFileCountsBefore = curBlockReadCountsBefore
		}
		if len(curBlockReadCountsAfter) > 0 {
			curBlock.ReadFileCountsAfter = curBlockReadCountsAfter
		}
		if hasTokenUsage(curBlockUsage) {
			u := curBlockUsage
			curBlock.TokenUsage = &u
		}
		if !curBlockStartTime.IsZero() && !endTime.IsZero() {
			curBlock.DurationSec = endTime.Sub(curBlockStartTime).Seconds()
		}
		if len(curBlock.EditedFiles) > 0 || curBlock.ReadsBeforeFirstEdit > 0 || len(curBlock.Searches) > 0 {
			metrics.PromptBlocks = append(metrics.PromptBlocks, *curBlock)
		}
		curBlock = nil
		curBlockStartTime = time.Time{}
		curBlockUsage = TokenUsage{}
		curBlockReadSet = map[string]struct{}{}
		curBlockWriteSet = map[string]struct{}{}
		curBlockFirstEditSeen = false
		curBlockReadCountsBefore = map[string]int{}
		curBlockReadCountsAfter = map[string]int{}
		activeSearches = nil
	}

	for _, event := range eventLog.Events {
		eventIsSearch := event.Kind == "tool_call" && isSearchEvent(event)
		if len(event.WritePaths) > 0 {
			firstEditSeen = true
			curBlockFirstEditSeen = true
		}

		var eventTime time.Time
		if event.Timestamp != "" {
			eventTime, _ = time.Parse(time.RFC3339Nano, event.Timestamp)
		}

		switch event.Kind {
		case "prompt":
			if strings.TrimSpace(event.Text) != "" {
				flushBlock(eventTime)
				metrics.UserPromptCount++
				fullText := strings.TrimSpace(event.Text)
				curBlock = &PromptBlock{
					Text:     truncatePrompt(fullText),
					FullText: fullText,
				}
				curBlockStartTime = eventTime
			}
		case "assistant_message":
			if strings.TrimSpace(event.Text) != "" {
				metrics.AssistantMessageCount++
			}
		case "tool_call":
			metrics.ToolCallCount++
			if curBlock != nil && eventIsSearch {
				curBlock.Searches = append(curBlock.Searches, SearchTrace{
					Query: searchEventQuery(event),
				})
				activeSearches = append(activeSearches, len(curBlock.Searches)-1)
			}
			if !firstEditSeen {
				exploration.ToolCallsBeforeFirstEdit++
				if isSearchEvent(event) {
					exploration.SearchesBeforeFirstEdit++
				}
			}
		case "tool_result":
			if event.IsError {
				errorIndices = append(errorIndices, event.Index)
			}
		case "token_usage":
			if event.TokenUsage == nil {
				continue
			}
			if event.TokenUsage.Cumulative {
				copyUsage := *event.TokenUsage
				deltaUsage := tokenUsageDelta(prevCumulativeUsage, copyUsage)
				cumulativeUsage = &copyUsage
				prevCumulativeUsage = &copyUsage
				if !hasTokenUsage(deltaUsage) {
					continue
				}
				addTokenUsage(&incrementalUsage, deltaUsage)
				if curBlock != nil {
					addTokenUsage(&curBlockUsage, deltaUsage)
				}
				if !firstEditSeen {
					addTokenUsage(&explorationUsage, deltaUsage)
				}
				continue
			}

			addTokenUsage(&incrementalUsage, *event.TokenUsage)
			if curBlock != nil {
				addTokenUsage(&curBlockUsage, *event.TokenUsage)
			}
			if !firstEditSeen {
				addTokenUsage(&explorationUsage, *event.TokenUsage)
			}
		}

		for _, path := range event.ReadPaths {
			if path == "" {
				continue
			}
			readSet[path] = struct{}{}
			readCounts[path]++
			if !firstEditSeen {
				explorationReadSet[path] = struct{}{}
			}
			if curBlock == nil {
				continue
			}
			if !eventIsSearch {
				for _, searchIdx := range activeSearches {
					trace := &curBlock.Searches[searchIdx]
					trace.ReadsBeforeEdit++
					if !containsPath(trace.ReadFiles, path) {
						trace.ReadFiles = append(trace.ReadFiles, path)
					}
				}
			}
			if !curBlockFirstEditSeen {
				if _, seen := curBlockReadSet[path]; !seen {
					curBlockReadSet[path] = struct{}{}
					curBlock.ReadsBeforeFirstEdit++
				}
				curBlockReadCountsBefore[path]++
				continue
			}
			curBlockReadCountsAfter[path]++
		}

		for _, path := range event.WritePaths {
			if path == "" {
				continue
			}
			writeSet[path] = struct{}{}
			editCounts[path]++
			if curBlock != nil {
				curBlockWriteSet[path] = struct{}{}
				curBlock.EditCount++
				for _, searchIdx := range activeSearches {
					trace := &curBlock.Searches[searchIdx]
					if trace.EditTarget == "" {
						trace.EditTarget = path
						trace.FoundViaSearch = containsPath(trace.ReadFiles, path)
					}
				}
				activeSearches = nil
			}
		}
	}
	flushBlock(time.Time{})

	metrics.TurnCount = metrics.UserPromptCount
	metrics.ReadFiles = sortedKeys(readSet)
	metrics.EditedFiles = sortedKeys(writeSet)
	metrics.ReadFileCount = len(metrics.ReadFiles)
	metrics.EditedFileCount = len(metrics.EditedFiles)
	if len(readCounts) > 0 {
		metrics.ReadFileCounts = readCounts
	}
	if len(editCounts) > 0 {
		metrics.EditFileCounts = editCounts
	}

	if cumulativeUsage != nil {
		metrics.TokenUsage = cumulativeUsage
	} else if hasTokenUsage(incrementalUsage) {
		metrics.TokenUsage = &incrementalUsage
	}

	exploration.FilesReadBeforeFirstEdit = len(explorationReadSet)
	if hasTokenUsage(explorationUsage) {
		copyUsage := explorationUsage
		exploration.TokensBeforeFirstEdit = &copyUsage
	}
	if exploration.ToolCallsBeforeFirstEdit > 0 || exploration.FilesReadBeforeFirstEdit > 0 {
		metrics.ExplorationStats = &exploration
	}

	if len(errorIndices) > 0 {
		metrics.FailureStats = buildFailureStats(eventLog.Events, errorIndices)
	}

	metrics.ContextStats = computeContextStats(eventLog.Events, metrics.TokenUsage)
	return metrics
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func buildFailureStats(events []SessionEvent, errorIndices []int) *FailureStats {
	lastToolCallIndex := -1
	for _, event := range events {
		if event.Kind == "tool_call" {
			lastToolCallIndex = event.Index
		}
	}

	var recovered, unresolved int
	for _, errIdx := range errorIndices {
		if lastToolCallIndex > errIdx {
			recovered++
		} else {
			unresolved++
		}
	}

	return &FailureStats{
		FailedToolCalls:    len(errorIndices),
		RecoveredFailures:  recovered,
		UnresolvedFailures: unresolved,
	}
}
