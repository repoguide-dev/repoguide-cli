package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/repoguide/repoguide-cli/internal"
	clientauth "github.com/repoguide/repoguide-cli/internal/auth"
	"github.com/repoguide/repoguide-cli/internal/config"
	"github.com/repoguide/repoguide-cli/internal/localrunner"
	mcpinternal "github.com/repoguide/repoguide-cli/internal/mcp"
	repopkg "github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/runtime"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
	"github.com/repoguide/repoguide-cli/internal/sqlitestore"
	"github.com/spf13/cobra"
)

// ponytail: overridden via -ldflags in run-local.sh for local dev
var defaultBackendURL = "https://repoguide.dev"

// developmentBuild is set only by run-local.sh. It deliberately changes no
// command-line behavior; it only identifies the local executable in help.
var developmentBuild = ""

const tokenRefreshLeadTime = 7 * 24 * time.Hour

var root = &cobra.Command{
	Use:     "repoguide",
	Aliases: []string{"rgd"},
	Short:   rootShortDescription(),
	Long:    rootLongDescription(),
}

func rootShortDescription() string {
	if developmentBuild != "" {
		return "Local development build of RepoGuide"
	}
	return "Replay and learn from your AI coding sessions"
}

func rootLongDescription() string {
	if developmentBuild != "" {
		return "Local development build of RepoGuide. It uses local development services and separate local state."
	}
	return "Replay and learn from your AI coding sessions"
}

func Execute() {
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	defaultURL := os.Getenv("REPOGUIDE_BACKEND_URL")
	if defaultURL == "" {
		defaultURL = defaultBackendURL
	}
	root.PersistentFlags().String("backend-url", defaultURL, "RepoGuide backend URL")
	_ = root.PersistentFlags().MarkHidden("backend-url")
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		if skipBackgroundTasks(cmd) {
			return
		}
		setupFileLogging()
		go refreshCachedAuthProfile()
		go backgroundSync()
	}
}

// skipBackgroundTasks prevents commands that delete the local store from
// concurrently recreating it through auth refresh or repository sync.
func skipBackgroundTasks(cmd *cobra.Command) bool {
	return cmd.Name() == "purge"
}

// semverLess returns true if version a is less than version b (e.g. "1.2.3" < "1.3.0").
// Strips leading "v" and ignores pre-release suffixes. Returns false on parse error.
func semverLess(a, b string) bool {
	parse := func(s string) [3]int {
		s = strings.TrimPrefix(s, "v")
		parts := strings.SplitN(s, ".", 3)
		var v [3]int
		for i, p := range parts {
			if i < 3 {
				v[i], _ = strconv.Atoi(strings.SplitN(p, "-", 2)[0])
			}
		}
		return v
	}
	av, bv := parse(a), parse(b)
	for i := range av {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}

func backgroundSync() {
	mcpinternal.MaybeUpdateHints()
	token, ok := clientauth.Load()
	repos, err := repopkg.ListConfiguredRepos()
	if err != nil || len(repos) == 0 {
		return
	}

	var cloudRepos, localRepos []internal.RepoConfig
	for _, r := range repos {
		if r.Mode == "local" {
			localRepos = append(localRepos, r)
		} else {
			cloudRepos = append(cloudRepos, r)
		}
	}

	if len(localRepos) > 0 {
		localBackgroundSyncRepos(localRepos)
	}
	if !ok {
		return
	}
	client := sessionimport.CloudClient{BaseURL: getBackendURL(), Token: token.Token}
	if Version != "dev" {
		if required, err := client.CheckRequiredVersion(); err == nil && semverLess(Version, required) {
			fmt.Fprintf(os.Stderr, "\nWarning: your repoguide CLI (%s) is outdated (required: %s). Please upgrade.\n\n", Version, required)
		}
	}
	if len(cloudRepos) == 0 {
		return
	}
	for _, repo := range cloudRepos {
		_ = client.UploadRepoEvents(repo.RepoID, repo.RepoRoot)
	}
}

func refreshCachedAuthProfile() {
	token, ok := clientauth.Load()
	if !ok || strings.TrimSpace(token.Token) == "" {
		return
	}
	updated, changed := refreshedAuthToken(getBackendURL(), token)
	if changed {
		_ = clientauth.Save(updated)
	}
}

func refreshedAuthToken(baseURL string, token clientauth.Token) (clientauth.Token, bool) {
	updated := token
	client := sessionimport.CloudClient{BaseURL: baseURL, Token: updated.Token}
	if clientauth.ExpiresSoon(updated.Token, tokenRefreshLeadTime) {
		refreshed, err := client.RefreshAuthToken()
		if err == nil && strings.TrimSpace(refreshed.Token) != "" {
			updated.Token = refreshed.Token
			if email := strings.TrimSpace(refreshed.Email); email != "" {
				updated.Email = email
			}
			client.Token = updated.Token
		}
	}
	me, err := client.GetMe()
	if err != nil {
		return updated, updated != token
	}
	if email := strings.TrimSpace(me.Email); email != "" {
		updated.Email = email
	}
	updated.Plan = strings.ToUpper(strings.TrimSpace(me.Plan))
	return updated, updated != token
}

func setupFileLogging() {
	dir := internal.RepoGuideDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "repoguide.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func localBackgroundSyncRepos(repos []internal.RepoConfig) {
	if len(repos) == 0 {
		return
	}
	dbPath, err := internal.RepoGuideDBPath()
	if err != nil {
		slog.Error("local sync: db path", "err", err)
		return
	}
	st, err := sqlitestore.Open(dbPath)
	if err != nil {
		slog.Error("local sync: open db", "err", err)
		return
	}
	defer st.Close()
	rt := localrunner.New(st, runtime.Config{AnthropicAPIKey: config.AnthropicAPIKey(), UseClaudeCLI: config.UseClaudeCLI()})
	ctx := context.Background()
	for _, repo := range repos {
		events, docs, err := sessionimport.ParseRepoEventsForLocal(repo.RepoID, repo.RepoRoot)
		if err != nil {
			slog.Error("local sync: parse events", "repo_id", repo.RepoID, "err", err)
			continue
		}
		if len(events) > 0 {
			if err := rt.Services.Sessions.PutEvents(ctx, repo.RepoID, events); err != nil {
				slog.Error("local sync: put events", "repo_id", repo.RepoID, "err", err)
			} else {
				slog.Info("local sync: stored events", "repo_id", repo.RepoID, "count", len(events))
			}
		}
		if len(docs) > 0 {
			if err := rt.Services.Sessions.PutDocs(ctx, repo.RepoID, docs); err != nil {
				slog.Error("local sync: put docs", "repo_id", repo.RepoID, "err", err)
			} else {
				slog.Info("local sync: stored docs", "repo_id", repo.RepoID, "count", len(docs))
			}
		}
	}
	rt.RunStartupMaintenance(ctx)
}
