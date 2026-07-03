package cmd

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	gMetricPrice    = 0
	gMetricContext  = 1
	gMetricRWRatio  = 2
	gMetricSearches = 3
	gMetricTools    = 4
	gMetricCount    = 5
	gYAxisWidth     = 9
)

var (
	gMetricNames = [gMetricCount]string{
		"Price (USD)",
		"Context (K tokens)",
		"Read/Write ratio",
		"Searches (pre-edit)",
		"Tool calls",
	}
	gMetricFmt = [gMetricCount]string{"$%.3f", "%.0fK", "%.1f×", "%.0f", "%.0f"}
	barBlocks  = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

	styleRepoGuide = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))  // cyan-green
	styleNormal    = lipgloss.NewStyle().Foreground(lipgloss.Color("247")) // gray
	styleCursor    = lipgloss.NewStyle().Foreground(lipgloss.Color("220")) // yellow
	styleYAxis     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleLegend    = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
)

type graphPoint struct {
	label         string
	date          string
	usedRepoGuide bool
	values        [gMetricCount]float64
}

type graphModel struct {
	points []graphPoint
	metric int
	cursor int
	offset int
	width  int
	height int
}

func buildGraphModel(analyzed []analyzedSession) graphModel {
	pts := make([]graphPoint, len(analyzed))
	for i, a := range analyzed {
		m := a.metrics
		var vals [gMetricCount]float64
		vals[gMetricPrice] = m.EstimatedCostUSD
		if cs := m.ContextStats; cs != nil {
			vals[gMetricContext] = float64(cs.MaxEffectiveInputTokens) / 1000
		}
		if m.EditedFileCount > 0 {
			vals[gMetricRWRatio] = float64(m.ReadFileCount) / float64(m.EditedFileCount)
		}
		if es := m.ExplorationStats; es != nil {
			vals[gMetricSearches] = float64(es.SearchesBeforeFirstEdit)
			vals[gMetricTools] = float64(es.ToolCallsBeforeFirstEdit)
		} else {
			vals[gMetricTools] = float64(m.ToolCallCount)
		}
		pts[i] = graphPoint{
			label:         sessionLabel(a.summary),
			date:          a.summary.Timestamp.Format("Jan 02"),
			usedRepoGuide: a.summary.UsedRepoGuide,
			values:        vals,
		}
	}
	// reverse to oldest→newest for left→right display
	for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
		pts[i], pts[j] = pts[j], pts[i]
	}
	cursor := max(0, len(pts)-1)
	return graphModel{points: pts, cursor: cursor}
}

func (m graphModel) Init() tea.Cmd { return nil }

func (m graphModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampOffset()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "left", "h":
			if m.cursor > 0 {
				m.cursor--
				m.clampOffset()
			}
		case "right", "l":
			if m.cursor < len(m.points)-1 {
				m.cursor++
				m.clampOffset()
			}
		case "up", "k":
			m.metric = (m.metric + gMetricCount - 1) % gMetricCount
		case "down", "j":
			m.metric = (m.metric + 1) % gMetricCount
		}
	}
	return m, nil
}

func (m *graphModel) clampOffset() {
	visible := m.visibleCount()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m graphModel) visibleCount() int {
	w := m.width - gYAxisWidth
	if w < 2 {
		return 1
	}
	return w / 2
}

func (m graphModel) chartHeight() int {
	h := m.height - 6 // title + axis line + detail + footer + 2 padding
	if h < 3 {
		return 3
	}
	return h
}

func (m graphModel) View() string {
	if len(m.points) == 0 {
		return "No sessions to graph.\n"
	}

	visible := m.visibleCount()
	end := m.offset + visible
	if end > len(m.points) {
		end = len(m.points)
	}
	pts := m.points[m.offset:end]

	// find max value for current metric
	var maxVal float64
	for _, pt := range m.points {
		if v := pt.values[m.metric]; v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	chartH := m.chartHeight()
	lines := make([]string, 0, m.height)

	// title
	lines = append(lines, titleStyle.Render(fmt.Sprintf("  %s  (%d/%d)", gMetricNames[m.metric], m.metric+1, gMetricCount)))

	// chart rows (top → bottom)
	for row := chartH - 1; row >= 0; row-- {
		var sb strings.Builder

		// Y axis label on first, middle, last rows
		yLabel := ""
		if row == chartH-1 {
			yLabel = fmt.Sprintf(gMetricFmt[m.metric], maxVal)
		} else if row == 0 {
			yLabel = fmt.Sprintf(gMetricFmt[m.metric], 0.0)
		}
		sb.WriteString(styleYAxis.Render(fmt.Sprintf("%*s", gYAxisWidth-1, yLabel)))
		if row == chartH-1 {
			sb.WriteString(styleYAxis.Render("┐"))
		} else if row == 0 {
			sb.WriteString(styleYAxis.Render("┘"))
		} else {
			sb.WriteString(styleYAxis.Render("│"))
		}

		// bars
		for i, pt := range pts {
			v := pt.values[m.metric]
			// bar height in 8ths
			eighths := int(v / maxVal * float64(chartH) * 8)
			fullRows := eighths / 8
			frac := eighths % 8

			var char rune
			if row < fullRows {
				char = barBlocks[8]
			} else if row == fullRows && frac > 0 {
				char = barBlocks[frac]
			} else {
				char = ' '
			}

			isCursor := (m.offset + i) == m.cursor
			barStr := string(char) + " "
			switch {
			case isCursor:
				sb.WriteString(styleCursor.Render(barStr))
			case pt.usedRepoGuide:
				sb.WriteString(styleRepoGuide.Render(barStr))
			default:
				sb.WriteString(styleNormal.Render(barStr))
			}
		}
		lines = append(lines, sb.String())
	}

	// axis line
	axisLine := styleYAxis.Render(fmt.Sprintf("%*s┴", gYAxisWidth-1, "")) +
		styleYAxis.Render(strings.Repeat("──", len(pts)))
	lines = append(lines, axisLine)

	// detail line for cursor session
	if m.cursor < len(m.points) {
		pt := m.points[m.cursor]
		val := fmt.Sprintf(gMetricFmt[m.metric], pt.values[m.metric])
		hTag := ""
		if pt.usedRepoGuide {
			hTag = " " + styleLegend.Render("◆ repoguide")
		}
		detail := fmt.Sprintf("  %s  %s  %s%s", muted.Render(pt.date), pt.label, val, hTag)
		lines = append(lines, detail)
	}

	// legend + footer
	legend := styleNormal.Render("█") + " no repoguide  " + styleRepoGuide.Render("█") + " repoguide  " + styleCursor.Render("█") + " cursor"
	footer := muted.Render("◄/► session  ▲/▼ metric  q quit")
	lines = append(lines, "  "+legend+"    "+footer)

	return strings.Join(lines, "\n")
}

func runStatsGraph(analyzed []analyzedSession) error {
	m := buildGraphModel(analyzed)
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
