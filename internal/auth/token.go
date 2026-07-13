package auth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Token struct {
	Token string `json:"token"`
	Email string `json:"email"`
	Plan  string `json:"plan,omitempty"`
}

// dataDirName is overridden in the local development build.
var dataDirName = ".repoguide"

func tokenPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, dataDirName, "auth.json")
}

func Load() (Token, bool) {
	data, err := os.ReadFile(tokenPath())
	if err != nil {
		return Token{}, false
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return Token{}, false
	}
	return t, t.Token != ""
}

func Save(t Token) error {
	path := tokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func Delete() {
	os.Remove(tokenPath())
}

func ExpiresAt(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func ExpiresSoon(token string, within time.Duration) bool {
	expiresAt, ok := ExpiresAt(token)
	if !ok {
		return false
	}
	return time.Until(expiresAt) <= within
}
