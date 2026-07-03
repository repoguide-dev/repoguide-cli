package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmRemove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		want   bool
		prompt string
	}{
		{name: "default no", input: "\n", want: false, prompt: "Continue? [y/N]: "},
		{name: "short yes", input: "y\n", want: true, prompt: "Continue? [y/N]: "},
		{name: "long yes", input: "yes\n", want: true, prompt: "Continue? [y/N]: "},
		{name: "explicit no", input: "no\n", want: false, prompt: "Continue? [y/N]: "},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			got, err := confirmRemove(strings.NewReader(tt.input), &out, "/repo", "/tmp/store")
			if err != nil {
				t.Fatalf("confirmRemove returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("confirmRemove = %v, want %v", got, tt.want)
			}
			if !strings.Contains(out.String(), tt.prompt) {
				t.Fatalf("prompt %q not found in output %q", tt.prompt, out.String())
			}
		})
	}
}

func TestConfirmRemoveAll(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	got, err := confirmRemoveAll(strings.NewReader("yes\n"), &out, "/tmp/home/.repoguide", 3)
	if err != nil {
		t.Fatalf("confirmRemoveAll returned error: %v", err)
	}
	if !got {
		t.Fatal("confirmRemoveAll = false, want true")
	}

	output := out.String()
	if !strings.Contains(output, "This will remove ALL RepoGuide data:") {
		t.Fatalf("expected destructive heading in output, got %q", output)
	}
	if !strings.Contains(output, "Repo config cleanup: 3 repos") {
		t.Fatalf("expected repo count in output, got %q", output)
	}
}
