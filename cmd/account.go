package cmd

import (
	"fmt"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	accountDeleteCmd.Flags().Bool("yes", false, "Confirm account deletion without prompting")
	root.AddCommand(accountCmd)
	accountCmd.AddCommand(accountDeleteCmd)
}

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage your account",
}

var accountDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Permanently delete your account",
	RunE:  runAccountDelete,
}

func runAccountDelete(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		return fmt.Errorf("pass --yes to confirm account deletion")
	}
	token, ok := clientauth.Load()
	if !ok {
		return fmt.Errorf("not logged in")
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
	if err := client.DeleteMe(); err != nil {
		return fmt.Errorf("account deletion failed: %w", err)
	}
	clientauth.Delete()
	fmt.Println("Account deleted.")
	return nil
}
