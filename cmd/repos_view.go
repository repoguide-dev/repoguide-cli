package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
)

func (m reposModel) View() string {
	switch m.view {
	case reposDetailView:
		return m.renderDetail()
	case reposSessionsView:
		return m.sessionsChild.View()
	default:
		return m.renderList()
	}
}

func (m reposModel) renderList() string {
	title := titleStyle.Render("Repos")
	if m.loading {
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + m.spinner.View() + " Loading repo stats...")
	}
	if m.err != nil {
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + m.err.Error() + "\n\n" + muted.Render(footerHint("r reload", "q quit")))
	}
	if len(m.repos) == 0 {
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\nNo repos found.\n\n" + muted.Render(footerHint("q quit")))
	}

	parts := []string{title}
	if m.statusMessage != "" {
		parts = append(parts, "", selected.Render("✓ ")+m.statusMessage)
	}
	parts = append(parts, "", m.table.View())
	if m.confirmDelete {
		repo := m.selectedRepo()
		name := ""
		if repo != nil {
			name = " " + repoDisplayName(repo.Repo)
		}
		parts = append(parts, "", renderDanger("Remove"+name+"? [y/N]"))
	} else {
		parts = append(parts, "", muted.Render(footerHint("enter show repo", "i init repo", "d remove repo", "r reload", "q quit")))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(
		strings.Join(parts, "\n"),
	)
}

func (m reposModel) renderDetail() string {
	repo := m.selectedRepo()
	if repo == nil {
		return ""
	}

	rows := make([]selectableRow, 0, len(sessionimport.SupportedSessionAgents()))
	agentColumns := []tableColumn{
		{title: "Agent", width: 16},
		{title: "Sessions", width: 8},
	}
	for i, agent := range sessionimport.SupportedSessionAgents() {
		rows = append(rows, selectableRow{
			label: renderTableRow(
				agentColumns,
				displayAgentName(agent),
				fmt.Sprintf("%d", repo.AgentCounts[agent]),
			),
			selected: i == m.agentCursor,
		})
	}

	lines := []string{
		titleStyle.Render(repoDisplayName(repo.Repo)),
		"",
		fmt.Sprintf("%s %s", headStyle.Render("Path:"), renderRepoPath(repo.Repo.RepoRoot)),
		fmt.Sprintf("%s %s", headStyle.Render("Mode:"), repoModeLabel(repo.Repo)),
		fmt.Sprintf("%s %s", headStyle.Render("Status:"), repoStatusLabel(*repo)),
		fmt.Sprintf("%s %s", headStyle.Render("Repo ID:"), valueOrFallback(repo.Repo.RepoID, "-")),
		fmt.Sprintf("%s %s", headStyle.Render("Last synced:"), repoLastSynced(*repo)),
		fmt.Sprintf("%s %d", headStyle.Render("Total sessions:"), repo.Total),
		"",
		headStyle.Render("Sessions by agent"),
		renderTableHeader(agentColumns),
		muted.Render(strings.Repeat("─", 28)),
	}
	for _, row := range rows {
		if row.selected {
			lines = append(lines, selected.Render("›")+" "+row.label)
			continue
		}
		lines = append(lines, "  "+row.label)
	}
	lines = append(lines, "", muted.Render(footerHint("i init repo", "enter open sessions", "q back")))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(lines, "\n"))
}

func repoTableColumns(width int) []tableColumn {
	if width <= 0 {
		width = 100
	}
	return []tableColumn{
		{title: "Repo", width: max(18, width/6)},
		{title: "Status", width: 8},
		{title: "Path", width: max(24, width/2)},
		{title: "Total", width: 5},
		{title: "Codex", width: 5},
		{title: "Claude", width: 6},
	}
}

func repoLastSynced(repo sessionimport.RepoSessionStats) string {
	if repo.LastSynced.IsZero() {
		return "never"
	}
	return repo.LastSynced.In(time.Local).Format("2006-01-02 15:04")
}

func repoDisplayName(repo internal.RepoConfig) string {
	name := filepath.Base(repo.RepoRoot)
	if strings.TrimSpace(name) == "" || name == "." || name == string(filepath.Separator) {
		return repo.RepoID
	}
	return name
}

func repoStatusLabel(repo sessionimport.RepoSessionStats) string {
	switch repo.Repo.Mode {
	case "local":
		return "local"
	case "online":
		return "online"
	}
	if repo.Online {
		return "online"
	}
	return ""
}

func repoModeLabel(repo internal.RepoConfig) string {
	switch repo.Mode {
	case "local":
		return "Local Mode"
	case "online":
		return "Online Mode"
	}
	return "-"
}
