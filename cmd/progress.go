package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

type workDoneMsg struct{}

type progressUpdateMsg struct {
	current int
	total   int
	label   string
}

type spinModel struct {
	spinner spinner.Model
	title   string
	label   string
	current int
	total   int
	done    bool
}

func newSpinModel(title string) spinModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return spinModel{spinner: s, title: title}
}

func (m spinModel) Init() tea.Cmd { return m.spinner.Tick }

func (m spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case workDoneMsg:
		m.done = true
		return m, tea.Quit
	case progressUpdateMsg:
		m.current = msg.current
		m.total = msg.total
		m.label = msg.label
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m spinModel) View() string {
	if m.done {
		return ""
	}
	status := m.title
	if m.total > 0 {
		status = fmt.Sprintf("%s (%d/%d)", status, m.current, m.total)
	}
	if m.label != "" {
		status += "\n  " + styleDim.Render(m.label)
	}
	return "\n  " + m.spinner.View() + " " + status + "\n\n"
}

// RunWithSpinner shows a spinner while work() runs, then returns the result.
// Any command can use this for long-running operations.
func RunWithSpinner[T any](label string, work func(progress func(current, total int, label string)) T) T {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return work(nil)
	}

	resultCh := make(chan T, 1)
	p := tea.NewProgram(newSpinModel(label))

	go func() {
		result := work(func(current, total int, stepLabel string) {
			p.Send(progressUpdateMsg{
				current: current,
				total:   total,
				label:   stepLabel,
			})
		})
		resultCh <- result
		p.Send(workDoneMsg{})
	}()

	p.Run() //nolint
	// ponytail: if p.Run() exited early (Ctrl-C/SIGINT), don't block on work that's still running
	select {
	case result := <-resultCh:
		return result
	default:
		var zero T
		return zero
	}
}

func RunWithStatusScreen(render func(spinnerView string) string, work func() error) error {
	if !isatty.IsTerminal(os.Stdout.Fd()) || !isatty.IsTerminal(os.Stdin.Fd()) {
		return work()
	}

	errCh := make(chan error, 1)
	p := tea.NewProgram(newSpinScreenModel(render), tea.WithAltScreen())

	go func() {
		errCh <- work()
		p.Send(workDoneMsg{})
	}()

	_, _ = p.Run()
	return <-errCh
}

type spinScreenModel struct {
	spinner spinner.Model
	render  func(spinnerView string) string
	done    bool
}

func newSpinScreenModel(render func(spinnerView string) string) spinScreenModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return spinScreenModel{spinner: s, render: render}
}

func (m spinScreenModel) Init() tea.Cmd { return m.spinner.Tick }

func (m spinScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case workDoneMsg:
		m.done = true
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m spinScreenModel) View() string {
	if m.done {
		return ""
	}
	return m.render(m.spinner.View())
}
