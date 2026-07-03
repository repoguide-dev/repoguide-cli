package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// newDetailViewport creates a viewport sized for a detail panel, leaving room
// for header padding (top=1, bottom=1) and a footer line.
func newDetailViewport(width, height int) viewport.Model {
	vp := viewport.New(max(20, width-4), max(5, height-4))
	vp.Style = lipgloss.NewStyle()
	return vp
}

// scrollHint returns a scroll-position indicator for a footer line.
func scrollHint(vp viewport.Model) string {
	if vp.AtTop() && vp.AtBottom() {
		return ""
	}
	return fmt.Sprintf("  •  %d%%", int(vp.ScrollPercent()*100))
}

// wordWrap breaks s at word boundaries so no line exceeds width runes.
func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	for i, para := range strings.Split(s, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		col := 0
		for j, word := range strings.Fields(para) {
			if j == 0 {
				out.WriteString(word)
				col = len(word)
			} else if col+1+len(word) > width {
				out.WriteByte('\n')
				out.WriteString(word)
				col = len(word)
			} else {
				out.WriteByte(' ')
				out.WriteString(word)
				col += 1 + len(word)
			}
		}
		if len(strings.Fields(para)) == 0 {
			// preserve blank lines
		}
	}
	return out.String()
}

// updateViewportKeys forwards common scroll keys to a viewport.
// Returns (updated viewport, handled bool).
func updateViewportKeys(vp viewport.Model, msg tea.KeyMsg) (viewport.Model, bool) {
	switch msg.String() {
	case "up", "k":
		vp.ScrollUp(1)
		return vp, true
	case "down", "j":
		vp.ScrollDown(1)
		return vp, true
	case "pgup", "b":
		vp.PageUp()
		return vp, true
	case "pgdown", "f", " ":
		vp.PageDown()
		return vp, true
	case "home", "g":
		vp.GotoTop()
		return vp, true
	case "end", "G":
		vp.GotoBottom()
		return vp, true
	}
	return vp, false
}
