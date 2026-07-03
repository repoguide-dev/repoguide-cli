package cmd

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPrintNextSelection(t *testing.T) {
	t.Parallel()

	out := captureStdout(t, func() {
		printNextSelection(
			nextCommandOption{command: "repoguide sessions", selected: true},
			nextCommandOption{command: "repoguide stats"},
			nextCommandOption{command: "repoguide files"},
		)
	})

	if !strings.Contains(out, "Next") {
		t.Fatalf("expected Next heading in output, got %q", out)
	}
	if !strings.Contains(out, "› repoguide sessions") {
		t.Fatalf("expected selected sessions command in output, got %q", out)
	}
	if !strings.Contains(out, "  repoguide stats") {
		t.Fatalf("expected stats command in output, got %q", out)
	}
	if !strings.Contains(out, "  repoguide files") {
		t.Fatalf("expected files command in output, got %q", out)
	}
	if !strings.Contains(out, "enter run  •  q quit") {
		t.Fatalf("expected picker footer in output, got %q", out)
	}
}

func TestNextCommandModelSelectsHighlightedOptionOnEnter(t *testing.T) {
	t.Parallel()

	model := newNextCommandModel([]nextCommandOption{
		{command: "repoguide sessions", selected: true, enabled: true},
		{command: "repoguide sync", note: "TBD"},
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(nextCommandModel)

	if !next.done {
		t.Fatal("expected model to mark itself done")
	}
	if cmd == nil {
		t.Fatal("expected enter to return a quit command")
	}
	selected := next.selected()
	if selected == nil || selected.command != "repoguide sessions" {
		t.Fatalf("expected sessions to be selected, got %#v", selected)
	}
}

func TestNextCommandModelDoesNotSelectOptionOnQuit(t *testing.T) {
	t.Parallel()

	model := newNextCommandModel([]nextCommandOption{
		{command: "repoguide sessions", selected: true, enabled: true},
		{command: "repoguide sync", note: "TBD"},
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	next := updated.(nextCommandModel)

	if !next.done {
		t.Fatal("expected model to mark itself done")
	}
	if cmd == nil {
		t.Fatal("expected q to return a quit command")
	}
	if selected := next.selected(); selected != nil {
		t.Fatalf("expected no selection on q, got %#v", selected)
	}
}
