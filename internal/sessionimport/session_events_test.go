package sessionimport

import "testing"

func TestRedactEventsRedactsSecretsButKeepsPaths(t *testing.T) {
	events := []SessionEvent{{
		Text:        `open "/repo/pkg/auth.go" with token=sk-live-abcdefghijklmnopqrstuvwxyz`,
		CommandText: `rg "password=hunter2" /repo/pkg/auth.go`,
		SearchQuery: `Bearer eyJaaaaaaaaaa.bbbbbbbbbbbb.cccccccccccc`,
	}}

	redactEvents(events)

	if events[0].Text != `open "/repo/pkg/auth.go" with token=[redacted]` {
		t.Fatalf("unexpected text redaction: %q", events[0].Text)
	}
	if events[0].CommandText != `rg "password=[redacted]" /repo/pkg/auth.go` {
		t.Fatalf("unexpected command redaction: %q", events[0].CommandText)
	}
	if events[0].SearchQuery != `Bearer [redacted]` {
		t.Fatalf("unexpected search redaction: %q", events[0].SearchQuery)
	}
}
