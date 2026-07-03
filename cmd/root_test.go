package cmd

import "testing"

func TestCompiledDefaultBackendURL(t *testing.T) {
	if defaultBackendURL != "https://repoguide.dev" {
		t.Fatalf("defaultBackendURL = %q, want %q", defaultBackendURL, "https://repoguide.dev")
	}
}
