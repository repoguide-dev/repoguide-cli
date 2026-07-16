package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/spf13/cobra"
)

func TestCompiledDefaultBackendURL(t *testing.T) {
	if defaultBackendURL != "https://repoguide.dev" {
		t.Fatalf("defaultBackendURL = %q, want %q", defaultBackendURL, "https://repoguide.dev")
	}
}

func TestSkipBackgroundTasks(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "purge", want: true},
		{name: "doctor", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: tt.name}
			if got := skipBackgroundTasks(cmd); got != tt.want {
				t.Fatalf("skipBackgroundTasks(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestRefreshedAuthTokenRefreshesExpiringJWT(t *testing.T) {
	oldToken := testJWTWithExp(time.Now().Add(30 * time.Minute).Unix())
	newToken := testJWTWithExp(time.Now().Add(30 * 24 * time.Hour).Unix())
	var refreshCalls int
	var meAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/refresh":
			refreshCalls++
			if auth := r.Header.Get("Authorization"); auth != "Bearer "+oldToken {
				t.Fatalf("refresh auth = %q", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": newToken,
				"email": "fresh@repoguide.test",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/auth/me":
			meAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"email":    "fresh@repoguide.test",
				"user_id":  "user-1",
				"plan":     "PRO",
				"is_admin": false,
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	updated, changed := refreshedAuthToken(server.URL, clientauth.Token{Token: oldToken, Email: "stale@repoguide.test"})
	if !changed {
		t.Fatal("expected refreshedAuthToken to report changes")
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if meAuth != "Bearer "+newToken {
		t.Fatalf("me Authorization = %q, want refreshed token", meAuth)
	}
	if updated.Token != newToken {
		t.Fatalf("updated token not replaced")
	}
	if updated.Email != "fresh@repoguide.test" {
		t.Fatalf("updated email = %q", updated.Email)
	}
	if updated.Plan != "PRO" {
		t.Fatalf("updated plan = %q", updated.Plan)
	}
}

func testJWTWithExp(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return strings.Join([]string{header, payload, "signature"}, ".")
}
