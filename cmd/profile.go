package cmd

import (
	"fmt"
	"strings"
	"time"

	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(profileCmd)
}

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Show your RepoGuide account, plan, and usage",
	RunE:  runProfile,
}

func runProfile(_ *cobra.Command, _ []string) error {
	token, ok := clientauth.Load()
	if !ok {
		fmt.Println("Not logged in. Run: repoguide login")
		return nil
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}

	me, err := client.GetMe()
	if err != nil {
		fmt.Printf("Email: %s  (offline)\n", token.Email)
	} else {
		plan := me.Plan
		if plan == "" {
			plan = "FREE"
		}
		fmt.Printf("Email:  %s\n", me.Email)
		fmt.Printf("Plan:   %s\n", plan)
	}

	limitsResp, limitsErr := client.GetLimits()
	if limitsErr == nil && limitsResp != nil {
		fmt.Println()
		fmt.Println("Usage this period:")
		for _, l := range limitsResp.Limits {
			if l.Max < 0 {
				fmt.Printf("  %-30s  %d / unlimited\n", l.Type, l.Used)
			} else {
				bar := usageBar(l.Used, l.Max)
				pct := 0
				if l.Max > 0 {
					pct = l.Used * 100 / l.Max
				}
				fmt.Printf("  %-30s  %d / %d  (%d%%)  %s\n", l.Type, l.Used, l.Max, pct, bar)
			}
		}
		fmt.Printf("  Period resets: %s\n", limitsResp.Period.ResetAt.Format(time.DateOnly))
	}

	fmt.Println()
	fmt.Println("Repos:")
	stats, err := sessionimport.LoadRepoSessionStats()
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		fmt.Println("  No repos tracked. Run: repoguide init")
		return nil
	}
	for _, s := range stats {
		status := "local"
		if s.Online {
			status = "online"
		} else if s.Repo.Mode != "local" && s.Repo.RepoID != "" {
			status = "offline"
		}
		fmt.Printf("  %-40s  %-7s  %d sessions\n",
			repoDisplayName(s.Repo),
			status,
			s.Total,
		)
	}
	return nil
}

func usageBar(used, max int) string {
	if max <= 0 {
		return ""
	}
	const width = 20
	filled := used * width / max
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
