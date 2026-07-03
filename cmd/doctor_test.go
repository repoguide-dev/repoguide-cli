package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
)

var stdoutCaptureMu sync.Mutex

func TestPrintLocalSetupIncludesStorageUsage(t *testing.T) {
	t.Parallel()

	status := repopkg.LocalSetupStatus{
		InGitRepo:         true,
		Initialized:       true,
		RepoID:            "repo_1234",
		StoreDir:          "/Users/test/.repoguide/repos/repo_1234",
		RepoGuideDir:      "/Users/test/.repoguide",
		LocalStorageFound: true,
		LocalStorageBytes: 1536,
	}

	out := captureStdout(t, func() {
		printLocalSetup(status, false)
	})

	if !strings.Contains(out, "Local storage: ~/.repoguide (1.5 KB)") {
		t.Fatalf("expected storage usage in output, got %q", out)
	}
}

func TestPrintLocalSetupReportsMissingStore(t *testing.T) {
	t.Parallel()

	status := repopkg.LocalSetupStatus{
		RepoGuideDir:      "/Users/test/.repoguide",
		LocalStorageFound: false,
	}

	out := captureStdout(t, func() {
		printLocalSetup(status, false)
	})

	if !strings.Contains(out, "Local storage: ~/.repoguide (not created yet)") {
		t.Fatalf("expected missing store message in output, got %q", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	stdoutCaptureMu.Lock()
	defer stdoutCaptureMu.Unlock()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer reader.Close()

	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	return buf.String()
}
