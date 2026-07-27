package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	mcpinternal "github.com/repoguide/repoguide-cli/internal/mcp"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	pathCmd.Flags().String("task", "", "Task description to route")
	_ = pathCmd.MarkFlagRequired("task")
	repoCmd.AddCommand(pathCmd)
}

var pathCmd = &cobra.Command{
	Use:   "path",
	Short: "Get RepoGuide routing context for a task in the current repo",
	RunE:  runPath,
}

func runPath(cmd *cobra.Command, _ []string) error {
	task, _ := cmd.Flags().GetString("task")

	status := repopkg.DetectLocalSetup()
	if !status.InGitRepo {
		printRunInsideRepo("repoguide repo path")
		return nil
	}
	if !status.Initialized {
		fmt.Fprintln(os.Stderr, "Repo not initialized. Run: repoguide init")
		return nil
	}

	token, _ := clientauth.Load()
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}

	knownFiles := mcpinternal.ResolveKnownFiles(status.RepoID, status.RepoRoot, &client)
	result, err := client.GetMCPUnderstandTask(status.RepoID, task, "", nil, knownFiles)
	if err != nil {
		return fmt.Errorf("understand-task: %w", err)
	}
	if result == nil {
		fmt.Println(mcpinternal.UnderstandTaskResponse(status.RepoID))
		return nil
	}

	if result.Status == "needs_clarification" {
		topics, err := client.GetMCPTopics(status.RepoID)
		if err != nil || len(topics) == 0 {
			fmt.Println("Task maps to multiple topics. Re-run with a more specific task.")
			return nil
		}
		if !isatty.IsTerminal(os.Stdout.Fd()) {
			for _, t := range topics {
				fmt.Printf("%s\t%s\t%s\n", t.ID, t.Name, t.Summary)
			}
			return nil
		}
		chosen, err := runTopicPicker(task, topics)
		if err != nil || chosen == "" {
			return err
		}
		result, err = client.GetMCPUnderstandTask(status.RepoID, task, chosen, nil, knownFiles)
		if err != nil {
			return fmt.Errorf("understand-task: %w", err)
		}
		if result == nil {
			fmt.Println(mcpinternal.UnderstandTaskResponse(status.RepoID))
			return nil
		}
	}

	text := strings.TrimSpace(result.Explanation)
	if result.ContextText != "" {
		text += "\n\n" + strings.TrimSpace(result.ContextText)
	}

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		fmt.Println(text)
		return nil
	}
	return runPathViewer(task, text)
}

// ── topic picker ─────────────────────────────────────────────────────────────

type topicPickerModel struct {
	topics   []sessionimport.MCPTopicSummary
	cursor   int
	chosen   string
	quitting bool
}

func runTopicPicker(_ string, topics []sessionimport.MCPTopicSummary) (string, error) {
	m := topicPickerModel{topics: topics}
	result, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", err
	}
	return result.(topicPickerModel).chosen, nil
}

func (m topicPickerModel) Init() tea.Cmd { return nil }

func (m topicPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.topics)-1 {
				m.cursor++
			}
		case "enter":
			m.chosen = m.topics[m.cursor].ID
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m topicPickerModel) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Multiple topics match — select one:") + "\n\n")
	for i, t := range m.topics {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = "› "
			style = selected
		}
		line := fmt.Sprintf("%s  %s", t.Name, muted.Render(t.Summary))
		sb.WriteString(style.Render(cursor+line) + "\n")
	}
	sb.WriteString("\n" + muted.Render("↑/↓ navigate · enter select · q quit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(sb.String())
}

// ── context viewer ────────────────────────────────────────────────────────────

type pathViewerModel struct {
	vp     viewport.Model
	text   string
	task   string
	copied bool
	width  int
	height int
	ready  bool
}

func runPathViewer(task, text string) error {
	m := pathViewerModel{task: task, text: text}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m pathViewerModel) Init() tea.Cmd { return nil }

func (m pathViewerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.ready {
			m.vp = newDetailViewport(m.width, m.height-3)
			m.vp.SetContent(wordWrap(m.text, m.width-6))
			m.ready = true
		} else {
			m.vp.Width = max(20, m.width-4)
			m.vp.Height = max(5, m.height-7)
			m.vp.SetContent(wordWrap(m.text, m.width-6))
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "c":
			if err := copyToClipboard(m.text); err == nil {
				m.copied = true
			}
			return m, nil
		default:
			var handled bool
			m.vp, handled = updateViewportKeys(m.vp, msg)
			_ = handled
		}
	}
	return m, nil
}

func (m pathViewerModel) View() string {
	if !m.ready {
		return ""
	}
	header := titleStyle.Render("RepoGuide: "+m.task) + "\n"
	footer := muted.Render("↑/↓ scroll · c copy · q close" + scrollHint(m.vp))
	if m.copied {
		footer = muted.Render("Copied!  ↑/↓ scroll · q close")
	}
	return header + m.vp.View() + "\n" + footer
}

func copyToClipboard(s string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case "windows":
		cmd = exec.Command("clip")
	default:
		return fmt.Errorf("unsupported OS")
	}
	cmd.Stdin = bytes.NewBufferString(s)
	return cmd.Run()
}
