package cmd

import (
	"fmt"

	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(Version)
		},
	})
}

// The backend gates behavior on which CLI is calling, and internal packages
// can't import cmd (cmd imports them), so the ldflags-injected version is
// pushed down at startup rather than pulled up.
func init() { sessionimport.ClientVersion = Version }
