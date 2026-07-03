package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	headStyle        = lipgloss.NewStyle().Bold(true)
	selected         = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	muted            = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dangerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	repoStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	untrackedRepo    = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	pathStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Italic(true)
	promptTextStyle  = lipgloss.NewStyle().Italic(true)
	searchQueryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
)

const (
	fileListPathMinWidth = 24
	fileListPathMaxWidth = 56
)

func footerHint(parts ...string) string {
	return strings.Join(parts, "  •  ")
}

func stylesEnabled() bool {
	return isatty.IsTerminal(os.Stdout.Fd())
}

func renderPath(path string) string {
	return renderPathText(displayPath(path))
}

func renderPathText(value string) string {
	if !stylesEnabled() || strings.TrimSpace(value) == "" {
		return value
	}
	return pathStyle.Render(value)
}

func renderDanger(value string) string {
	if !stylesEnabled() || strings.TrimSpace(value) == "" {
		return value
	}
	return dangerStyle.Render(value)
}

func renderSectionTitle(value string) string {
	if !stylesEnabled() || strings.TrimSpace(value) == "" {
		return value
	}
	return headStyle.Render(value)
}

func renderRepoPath(path string) string {
	displayed := displayPath(path)
	if !stylesEnabled() || strings.TrimSpace(displayed) == "" {
		return displayed
	}

	dir, base := splitPath(displayed)
	if base == "" {
		return pathStyle.Render(displayed)
	}
	if dir == "" {
		return repoStyle.Render(base)
	}
	return pathStyle.Render(dir) + repoStyle.Render(base)
}

func fileListPathWidth(paths []string) int {
	width := fileListPathMinWidth
	for _, path := range paths {
		width = max(width, lipgloss.Width(path))
	}
	return min(width, fileListPathMaxWidth)
}

func formatFileListLine(path string, pathWidth int, stats string) string {
	display := truncateMiddle(path, pathWidth)
	padding := max(0, pathWidth-lipgloss.Width(display))
	return "  " + renderPathText(display+strings.Repeat(" ", padding)) + "  " + stats
}

func truncateMiddle(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}

	runes := []rune(value)
	available := width - 3
	left := max(6, available/3)
	if left >= available {
		left = available - 1
	}
	right := available - left
	if left <= 0 || right <= 0 || left+right >= len(runes) {
		return string(runes[:width])
	}

	return string(runes[:left]) + "..." + string(runes[len(runes)-right:])
}

func formatTokensK(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%dk tokens", n/1000)
	}
	return fmt.Sprintf("%d tokens", n)
}

func splitPath(path string) (string, string) {
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return "", path
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if dir == "." {
		return "", base
	}
	if dir == string(filepath.Separator) {
		return dir, base
	}
	return dir + string(filepath.Separator), base
}
