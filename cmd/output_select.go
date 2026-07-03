package cmd

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type selectableRow struct {
	label    string
	selected bool
}

func renderSelectableList(title string, rows []selectableRow, footer string) string {
	parts := []string{title, ""}
	for _, row := range rows {
		cursor := "  "
		lineStyle := lipgloss.NewStyle()
		if row.selected {
			cursor = "› "
			lineStyle = selected
		}
		parts = append(parts, lineStyle.Render(cursor+row.label))
	}
	if footer != "" {
		parts = append(parts, "", muted.Render(footer))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(parts, "\n"))
}
