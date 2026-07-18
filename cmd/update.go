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
	c := exec.Command("sh", "-c", "curl -fsSL https://repoguide.dev/install.sh | sh")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
}
