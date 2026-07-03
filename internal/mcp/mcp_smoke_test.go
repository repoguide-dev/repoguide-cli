package mcp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestRunMCPSmoke(t *testing.T) {
	repoRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(repoRoot, "README.md"), "# smoke\n")
	mustWriteFile(t, filepath.Join(repoRoot, "cli", "go.mod"), "module example\n")
	mustWriteFile(t, filepath.Join(repoRoot, "cli", "cmd", "root.go"), "package cmd\n")

	serverStdoutReader, serverStdoutWriter := io.Pipe()
	serverStdinReader, serverStdinWriter := io.Pipe()

	go func() {
		_ = RunMCPServer(serverStdinReader, serverStdoutWriter, "", "")
		_ = serverStdoutWriter.Close()
	}()

	report, err := runMCPSmoke(newMCPSmokeClient(serverStdoutReader, serverStdinWriter), "smoke test mcp tools", repoRoot, "", "")
	if err != nil {
		t.Fatalf("runMCPSmoke returned error: %v", err)
	}
	if !report.Success() {
		t.Fatalf("expected smoke report success, got %+v", report.Checks)
	}
}
