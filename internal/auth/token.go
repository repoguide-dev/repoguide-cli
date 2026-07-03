package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Token struct {
	Token string `json:"token"`
	Email string `json:"email"`
	Plan  string `json:"plan,omitempty"`
}

func tokenPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".repoguide", "auth.json")
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
