package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type nextCommandOption struct {
	command  string
	note     string
	selected bool
	enabled  bool
	run      func(*cobra.Command) error
}

func printSection(title string, lines ...string) {
	fmt.Println(renderSectionTitle(title))
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
}

func printNext(commands ...string) {
	fmt.Println(renderSectionTitle("Next:"))
	for _, command := range commands {
		fmt.Printf("  %s\n", command)
	}
}

func printNextSelection(options ...nextCommandOption) {
	fmt.Println(renderNextSelection(options))
}

func promptNextSelection(cmd *cobra.Command, options ...nextCommandOption) error {
	if !isatty.IsTerminal(os.Stdout.Fd()) || !isatty.IsTerminal(os.Stdin.Fd()) {
		printNextSelection(options...)
		return nil
	}

	finalModel, err := tea.NewProgram(newNextCommandModel(options)).Run()
	if err != nil {
		return err
	}

	selected := finalModel.(nextCommandModel).selected()
	if selected == nil || !selected.enabled || selected.run == nil {
		return nil
	}
	return selected.run(cmd)
}

func renderNextSelection(options []nextCommandOption) string {
	rows := make([]selectableRow, 0, len(options))
	for _, option := range options {
		label := option.command
		if option.note != "" {
			label = fmt.Sprintf("%s %s", label, muted.Render("("+option.note+")"))
		}
		rows = append(rows, selectableRow{
			label:    label,
			selected: option.selected,
		})
	}

	return renderSelectableList(
		renderSectionTitle("Next"),
		rows,
		"enter run  •  q quit",
	)
}

type nextCommandModel struct {
	options []nextCommandOption
	cursor  int
	done    bool
	run     bool
}

func newNextCommandModel(options []nextCommandOption) nextCommandModel {
	model := nextCommandModel{options: append([]nextCommandOption(nil), options...)}
	for i, option := range model.options {
		if option.selected {
			model.cursor = i
			break
		}
	}
	return model
}

func (m nextCommandModel) Init() tea.Cmd {
	return nil
}

func (m nextCommandModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.done = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter":
			m.done = true
			m.run = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m nextCommandModel) View() string {
	rows := make([]nextCommandOption, 0, len(m.options))
	for i, option := range m.options {
		option.selected = i == m.cursor
		rows = append(rows, option)
	}
	return renderNextSelection(rows)
}

func (m nextCommandModel) selected() *nextCommandOption {
	if !m.done || !m.run || m.cursor < 0 || m.cursor >= len(m.options) {
		return nil
	}
	return &m.options[m.cursor]
}

func printRunInsideRepo(command string) {
	fmt.Println("No Git repository found.")
	fmt.Println()
	fmt.Println(renderSectionTitle("Run this inside a repo:"))
	fmt.Println()
	fmt.Println("  cd your-repo")
	fmt.Printf("  %s\n", command)
}
