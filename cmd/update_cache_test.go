package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/repoguide/repoguide-cli/internal"
)

// The startup check runs on every command. Each miss spends one of the 60
// unauthenticated api.github.com requests an IP gets per hour - the same budget
// `repoguide update` needs - so a fresh cache entry must not hit the network.
func TestCachedLatestReleaseVersionServesFreshCache(t *testing.T) {
	dir := t.TempDir()
	internal.SetRepoGuideDirOverride(dir)
	t.Cleanup(func() { internal.SetRepoGuideDirOverride("") })

	// A version the real API would never return, so a network call is visible.
	writeLatestReleaseCache(latestReleaseCache{Version: "99.99.99", CheckedAt: time.Now()})

	got, err := cachedLatestReleaseVersion(time.Nanosecond)
	if err != nil {
		t.Fatalf("fresh cache must not need the network: %v", err)
	}
	if got != "99.99.99" {
		t.Fatalf("got %q, want the cached 99.99.99", got)
	}
}

func TestCachedLatestReleaseVersionIgnoresExpiredCache(t *testing.T) {
	dir := t.TempDir()
	internal.SetRepoGuideDirOverride(dir)
	t.Cleanup(func() { internal.SetRepoGuideDirOverride("") })

	writeLatestReleaseCache(latestReleaseCache{
		Version:   "99.99.99",
		CheckedAt: time.Now().Add(-latestReleaseCacheTTL - time.Minute),
	})

	// Timeout is far too short to reach GitHub, so an expired entry must fail
	// rather than be served.
	if got, err := cachedLatestReleaseVersion(time.Nanosecond); err == nil && got == "99.99.99" {
		t.Fatal("expired cache entry was served instead of refetched")
	}
}

func TestWriteLatestReleaseCachePreservesNotifiedVersion(t *testing.T) {
	dir := t.TempDir()
	internal.SetRepoGuideDirOverride(dir)
	t.Cleanup(func() { internal.SetRepoGuideDirOverride("") })

	writeLatestReleaseCache(latestReleaseCache{Version: "1.0.0", CheckedAt: time.Now(), NotifiedVersion: "1.0.0"})
	if got := readLatestReleaseCache(); got.NotifiedVersion != "1.0.0" {
		t.Fatalf("NotifiedVersion = %q, want 1.0.0 - Homebrew users would be re-warned every command", got.NotifiedVersion)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "cache", "latest-release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk latestReleaseCache
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("cache file is not valid json: %v", err)
	}
}
