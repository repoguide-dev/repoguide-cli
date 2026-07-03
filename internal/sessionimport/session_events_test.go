package sessionimport

import "testing"

func TestRedactEventsPreservesAllFields(t *testing.T) {
	events := []SessionEvent{{
		Text:        "token sk_live_1234567890/with-symbol",
		CommandText: `rg "token sk_live_1234567890/with-symbol" README.md`,
		SearchQuery: `token sk_live_1234567890/with-symbol`,
	}}

	redactEvents(events)

	if events[0].Text != "token sk_live_1234567890/with-symbol" {
		t.Fatalf("expected text to be preserved, got %q", events[0].Text)
	}
	if events[0].CommandText != `rg "token sk_live_1234567890/with-symbol" README.md` {
		t.Fatalf("expected command text to be preserved, got %q", events[0].CommandText)
	}
	if events[0].SearchQuery != `token sk_live_1234567890/with-symbol` {
		t.Fatalf("expected search query to be preserved, got %q", events[0].SearchQuery)
	}
}
