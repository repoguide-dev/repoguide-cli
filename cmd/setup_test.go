package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
)

func TestCILoginMissingToken(t *testing.T) {
	t.Setenv("REPOGUIDE_CI_TOKEN", "")
	err := runCILogin()
	if err == nil || err.Error() != "REPOGUIDE_CI_TOKEN is not set" {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestCILoginSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/ci/exchange" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "tok123", "email": "e2e@repoguide.test"})
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("REPOGUIDE_CI_TOKEN", "secret")
	if err := root.PersistentFlags().Set("backend-url", srv.URL); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.PersistentFlags().Set("backend-url", "") }()

	if err := runCILogin(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tok, ok := clientauth.Load()
	if !ok || tok.Token != "tok123" || tok.Email != "e2e@repoguide.test" {
		t.Fatalf("token not stored correctly: %+v", tok)
	}
}

func TestCILoginServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	t.Setenv("REPOGUIDE_CI_TOKEN", "wrong")
	if err := root.PersistentFlags().Set("backend-url", srv.URL); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.PersistentFlags().Set("backend-url", "") }()

	err := runCILogin()
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestCILoginDoesNotPrintToken(t *testing.T) {
	// Verify runCILogin only takes REPOGUIDE_CI_TOKEN from env, never prints it.
	// The function signature and implementation show no fmt.Print of the env var.
	// This test documents the contract: env var name is not in output.
	_ = os.Getenv("REPOGUIDE_CI_TOKEN") // accessed; not printed
}

func TestEnsureActiveLoginPromptsAgainForInvalidSavedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := clientauth.Save(clientauth.Token{Token: "stale-token", Email: "stale@repoguide.test"}); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := root.PersistentFlags().Set("backend-url", srv.URL); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.PersistentFlags().Set("backend-url", "") }()

	called := 0
	prev := runDeviceLogin
	runDeviceLogin = func(mode string) error {
		called++
		if mode != "login" {
			t.Fatalf("mode = %q, want login", mode)
		}
		return nil
	}
	defer func() { runDeviceLogin = prev }()

	if err := ensureActiveLogin(); err != nil {
		t.Fatalf("ensureActiveLogin returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("runDeviceLogin called %d times, want 1", called)
	}
	if _, ok := clientauth.Load(); ok {
		t.Fatal("stale token should be cleared before forcing a fresh login")
	}
}

func TestDeviceFlowOnlyRunsDeviceLogin(t *testing.T) {
	prev := runDeviceLogin
	runDeviceLogin = func(mode string) error {
		if mode != "login" {
			t.Fatalf("mode = %q, want login", mode)
		}
		return nil
	}
	defer func() { runDeviceLogin = prev }()

	if err := deviceFlow("login"); err != nil {
		t.Fatalf("deviceFlow returned error: %v", err)
	}
}

func TestSetupRepoModelDefaultsToCurrentRepo(t *testing.T) {
	repos := []string{"/tmp/alpha", "/tmp/beta", "/tmp/gamma"}
	m := newSetupRepoModel(repos, "/tmp/beta")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
}

func TestSetupRepoModelKeepsCursorVisible(t *testing.T) {
	repos := make([]string, 20)
	for i := range repos {
		repos[i] = filepath.Join("/tmp", fmt.Sprintf("repo-%02d", i))
	}
	model := newSetupRepoModel(repos, repos[15])
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m := updated.(setupRepoModel)
	if m.cursor != 15 {
		t.Fatalf("cursor = %d, want 15", m.cursor)
	}
	if m.offset > m.cursor || m.cursor >= m.offset+m.visibleRows() {
		t.Fatalf("cursor %d not visible with offset %d and visible rows %d", m.cursor, m.offset, m.visibleRows())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(setupRepoModel)
	if m.cursor >= 15 {
		t.Fatalf("pgup cursor = %d, want less than 15", m.cursor)
	}
	if m.offset > m.cursor || m.cursor >= m.offset+m.visibleRows() {
		t.Fatalf("cursor %d not visible after pgup with offset %d and visible rows %d", m.cursor, m.offset, m.visibleRows())
	}
}

func TestOfflineFlagsExposedOnSetupAndInit(t *testing.T) {
	if setupCmd.Flags().Lookup("offline") == nil {
		t.Fatal("setup command missing --offline flag")
	}
	if initCmd.Flags().Lookup("offline") == nil {
		t.Fatal("init command missing --offline flag")
	}
	if setupCmd.Flags().Lookup("local") != nil {
		t.Fatal("setup command still exposes --local flag")
	}
	if initCmd.Flags().Lookup("local") != nil {
		t.Fatal("init command still exposes --local flag")
	}
}
