package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Update repoguide to the latest version",
		Run:   runUpdate,
	})
}

func runUpdate(_ *cobra.Command, _ []string) {
	if exe, err := os.Executable(); err == nil && strings.Contains(exe, "Cellar") {
		fmt.Println("Installed via Homebrew — run `brew upgrade repoguide` instead.")
		return
	}

	fmt.Println("==> Fetching latest repoguide release...")
	// ponytail: curl's built-in retry backoff (1s,2s,4s...capped at 20s) covers
	// transient 5xx/network errors; --retry-all-errors makes it retry HTTP
	// status failures too, not just connection errors.
	c := exec.Command("sh", "-c", "curl -fsSL --retry 5 --retry-all-errors --retry-max-time 60 https://repoguide.dev/install.sh | sh")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
}
