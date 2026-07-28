package analysis

import (
	"sort"
	"strings"

	"github.com/repoguide/repoguide-core/model"
)

// SessionStrip is one session rendered as a left-to-right strip of tool calls,
// each classified by a mechanical marker. Nothing here is a semantic judgment
// about whether the agent understood the task - only what the event stream
// shows: a search that led to no read, a file read but never edited, a file
// re-read, and where the edits landed.
type SessionStrip struct {
	Title     string   `json:"title"`
	Calls     int      `json:"calls"`
	EditIndex int      `json:"edit_index"` // 1-based position of the first edit; 0 = no edit
	CostUSD   float64  `json:"cost_usd"`   // 0 when the session's model has no known pricing
	UsedGuide bool     `json:"used_guide"` // the session called a RepoGuide tool
	Markers   []string `json:"markers"`    // one per call: "ok" | "cold" | "unused" | "reopen" | "edit"
	Labels    []string `json:"labels"`     // hover text per call: tool name plus its file or query
	Files     []string `json:"files"`      // file each call touched, "" for non-file calls; used to link repeats on hover

	// Everything after the first edit, counted as well as drawn - the divider
	// says where the work started, these say what the rest of it was.
	AfterCalls int `json:"after_calls"`
	AfterEdits int `json:"after_edits"`
	AfterReads int `json:"after_reads"`
	AfterOther int `json:"after_other"`

	// TopFile is the file this session opened most, with how many times. The
	// re-reading, not the pre-edit window, is where the call volume actually
	// goes in long sessions.
	TopFile  string `json:"top_file,omitempty"`
	TopReads int    `json:"top_reads,omitempty"`
}

const (
	markerOK     = "ok"     // nothing mechanical to flag
	markerCold   = "cold"   // search with no file read in the calls that follow
	markerUnused = "unused" // read a file the session never edits
	markerReopen = "reopen" // read a file already read earlier in the session
	markerEdit   = "edit"   // a write call
)

// coldSearchLookahead is how many calls after a search may still count as
// "the search paid off". Beyond that the read is more plausibly the result of
// a later call than of this search.
const coldSearchLookahead = 3

// BuildSessionStrips renders every session that made at least one tool call.
// Sorted worst-first by flagged-marker share so the noisiest traces lead.
func BuildSessionStrips(repoRoot string, stored []model.RepoSessionEvents) []SessionStrip {
	// Sessions that never edit have no first-edit mark to align on, so they
	// can't be read against the others and are left out.
	strips := make([]SessionStrip, 0, len(stored))
	for _, s := range stored {
		if strip, ok := buildSessionStrip(repoRoot, s); ok && strip.EditIndex > 0 {
			strips = append(strips, strip)
		}
	}
	// Rank by how many calls came before the first edit - the longest hunt
	// leads, which is also how far right the divider sits in each row.
	sort.SliceStable(strips, func(i, j int) bool {
		return strips[i].EditIndex > strips[j].EditIndex
	})
	return strips
}

func buildSessionStrip(repoRoot string, s model.RepoSessionEvents) (SessionStrip, bool) {
	calls := make([]model.SessionEvent, 0, len(s.Events))
	for _, ev := range s.Events {
		if ev.Kind == "tool_call" {
			calls = append(calls, ev)
		}
	}
	if len(calls) == 0 {
		return SessionStrip{}, false
	}

	// Every path the session ever writes: a read of a file outside this set
	// never turned into an edit.
	edited := map[string]struct{}{}
	for _, ev := range calls {
		for _, raw := range ev.WritePaths {
			if p := repoRelPath(repoRoot, raw); p != "" {
				edited[p] = struct{}{}
			}
		}
	}
	// Which calls read a real file - used to decide whether a search paid off.
	reads := make([][]string, len(calls))
	for i, ev := range calls {
		for _, raw := range ev.ReadPaths {
			p := repoRelPath(repoRoot, raw)
			if p != "" && realFileExists(repoRoot, p) {
				reads[i] = append(reads[i], p)
			}
		}
	}

	strip := SessionStrip{
		Title:     sessionStripTitle(s),
		Calls:     len(calls),
		CostUSD:   sessionCostUSD(s),
		UsedGuide: usedRepoGuide(calls),
		Markers:   make([]string, len(calls)),
		Labels:    make([]string, len(calls)),
		Files:     make([]string, len(calls)),
	}
	read := map[string]struct{}{}
	for i, ev := range calls {
		file := callFile(repoRoot, ev, reads[i])
		marker := markerOK
		switch {
		case len(ev.WritePaths) > 0:
			// Every edit is green - the strip is about how much came before the
			// work, so the work itself always reads the same.
			marker = markerEdit
			if strip.EditIndex == 0 {
				strip.EditIndex = i + 1
			}
		case inSet(read, reads[i]):
			marker = markerReopen
		case len(reads[i]) > 0 && !inSet(edited, reads[i]):
			marker = markerUnused
		case isSearchEvent(ev) && coldSearch(reads, i):
			marker = markerCold
		}
		for _, p := range reads[i] {
			read[p] = struct{}{}
		}
		strip.Markers[i] = marker
		strip.Files[i] = file
		strip.Labels[i] = callLabel(ev, file)
	}
	strip.TopFile, strip.TopReads = mostOpenedFile(reads)
	strip.countTail()
	return strip, true
}

// countTail summarizes what happened after the first edit. The whole session
// is still drawn - the first-edit divider is what separates the hunt from the
// work - but the counts say what the tail is made of without counting blocks.
func (s *SessionStrip) countTail() {
	window := s.EditIndex
	if window == 0 {
		window = len(s.Markers) // no edit: the whole session was the search
	}
	for _, m := range s.Markers[window:] {
		switch m {
		case markerEdit:
			s.AfterEdits++
		case markerReopen, markerUnused:
			s.AfterReads++
		default:
			s.AfterOther++
		}
	}
	s.AfterCalls = len(s.Markers) - window
}

// PreEditIndex is the 0-based position of the first edit block, i.e. where the
// divider goes. -1 when the session never edited, so nothing matches it.
func (s SessionStrip) PreEditIndex() int { return s.EditIndex - 1 }

func mostOpenedFile(reads [][]string) (string, int) {
	counts := map[string]int{}
	top, n := "", 0
	for _, paths := range reads {
		for _, p := range paths {
			counts[p]++
			if counts[p] > n || (counts[p] == n && p < top) {
				top, n = p, counts[p]
			}
		}
	}
	if n < 2 {
		return "", 0
	}
	return top, n
}

func inSet(set map[string]struct{}, paths []string) bool {
	for _, p := range paths {
		if _, ok := set[p]; ok {
			return true
		}
	}
	return false
}

// sessionCostUSD prices a session by the model its own events report. The
// agent name (s.Agent, e.g. "claude-code") is not a model and never matches
// the pricing table, so it can't be used as a proxy here.
func sessionCostUSD(s model.RepoSessionEvents) float64 {
	usage := analyzeSessionEvents(s.Events).TokenUsage
	if usage == nil {
		return 0
	}
	for _, ev := range s.Events {
		if ev.Model == "" {
			continue
		}
		if cost := estimateCost(ev.Model, usage); cost > 0 {
			return cost
		}
	}
	return 0
}

// usedRepoGuide marks sessions that already had repo experience served to
// them - their strips are the comparison case, not more evidence of the tax.
func usedRepoGuide(calls []model.SessionEvent) bool {
	for _, ev := range calls {
		if strings.Contains(strings.ToLower(ev.ToolName), "repoguide") {
			return true
		}
	}
	return false
}

// callFile is the file a call touched, if any - the key the report uses to
// light up every other call against the same file when one is hovered.
func callFile(repoRoot string, ev model.SessionEvent, reads []string) string {
	if len(ev.WritePaths) > 0 {
		return repoRelPath(repoRoot, ev.WritePaths[0])
	}
	if len(reads) > 0 {
		return reads[0]
	}
	return ""
}

// callLabel is the hover text for one block: the tool, plus whichever of the
// file it touched or the query it ran is available.
func callLabel(ev model.SessionEvent, file string) string {
	tool := ev.ToolName
	if tool == "" {
		tool = "tool"
	}
	detail := file
	if detail == "" {
		detail = searchQuery(ev)
	}
	if detail == "" {
		return tool
	}
	return tool + " · " + truncatePromptPreview(detail, 80)
}

func coldSearch(reads [][]string, i int) bool {
	for j := i + 1; j < len(reads) && j <= i+coldSearchLookahead; j++ {
		if len(reads[j]) > 0 {
			return false
		}
	}
	return true
}

func sessionStripTitle(s model.RepoSessionEvents) string {
	if title := sessionCaseStudyTitle(s.Name); title != "" {
		return title
	}
	for _, ev := range s.Events {
		if ev.Kind != "prompt" {
			continue
		}
		if cleaned := cleanPromptText(ev.Text); usableCaseStudyText(cleaned) {
			return truncatePromptPreview(cleaned, 90)
		}
	}
	return "(untitled session)"
}
