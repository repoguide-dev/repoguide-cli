package cmd

import (
	"fmt"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	testCmd.AddCommand(smokeCmd)
	root.AddCommand(testCmd)
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run checks against the CLI and API",
}

var smokeCmd = &cobra.Command{
	Use:   "smoke",
	Short: "Verify CLI auth and API connectivity",
	RunE:  runSmoke,
}

func runSmoke(_ *cobra.Command, _ []string) error {
	token, ok := clientauth.Load()
	if !ok {
		return fmt.Errorf("not logged in; run: repoguide setup --ci")
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
	me, err := client.GetMe()
	if err != nil {
		return fmt.Errorf("smoke check failed: %w", err)
	}
	fmt.Printf("OK: authenticated as %s\n", me.Email)
	return nil
}
