package cmd

import (
	"fmt"
	"strings"
)

type tableColumn struct {
	title string
	width int
}

func renderTableHeader(columns []tableColumn) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, column.title)
	}
	return headStyle.Render(renderTableRow(columns, values...))
}

func renderTableRow(columns []tableColumn, values ...string) string {
	cells := make([]string, 0, len(columns))
	for i, column := range columns {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		cells = append(cells, fmt.Sprintf("%-*s", column.width, truncate(value, column.width)))
	}
	return strings.Join(cells, "  ")
}

func truncate(value string, width int) string {
	if width <= 0 || len(value) <= width {
		return value
	}
	if width <= 1 {
		return value[:width]
	}
	return value[:width-1] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
