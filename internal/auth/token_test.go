package auth

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func TestExpiresAt(t *testing.T) {
	want := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	token := testJWTWithExp(want.Unix())

	got, ok := ExpiresAt(token)
	if !ok {
		t.Fatal("ExpiresAt returned ok=false")
	}
	if !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}
}

func TestExpiresSoon(t *testing.T) {
	soon := testJWTWithExp(time.Now().Add(30 * time.Minute).Unix())
	later := testJWTWithExp(time.Now().Add(48 * time.Hour).Unix())

	if !ExpiresSoon(soon, time.Hour) {
		t.Fatal("expected near-expiry token to require refresh")
	}
	if ExpiresSoon(later, time.Hour) {
		t.Fatal("expected long-lived token not to require refresh")
	}
}

func TestExpiresAtRejectsMalformedToken(t *testing.T) {
	if _, ok := ExpiresAt("not-a-jwt"); ok {
		t.Fatal("malformed token should not decode")
	}
}

func testJWTWithExp(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return header + "." + payload + ".signature"
}
