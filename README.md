# RepoGuide CLI

RepoGuide learns from past coding-agent sessions in a repo (what was searched, edited, tested, and fixed) and gives the next agent (Claude Code, Codex CLI, or any MCP-compatible agent) the files, workflows, tests, and pitfalls it needs before it starts searching from scratch. `repoguide-cli` is the `repoguide` command-line application, and can run against the hosted backend or fully offline on your own machine.

Website: [repoguide.dev](https://repoguide.dev)

## Install

```sh
brew install --cask repoguide-dev/tap/repoguide
```

or

```sh
curl -fsSL https://repoguide.dev/install.sh | sh
```

Binaries and checksums for every release are also published on [`repoguide-releases`](https://github.com/repoguide-dev/repoguide-releases).

Then, from inside a Git repo:

```sh
repoguide setup
```

This discovers your Git repos, initializes the selected one, and installs the MCP server for it. By default it signs you into the hosted backend (cloud mode); pass `--offline` to skip login and run fully local instead:

```sh
repoguide setup --offline
```

## Cloud vs. offline mode

Chosen at `repoguide setup` / `repoguide repo init` time via the `--offline` flag:

- **Cloud mode** (default) signs in to the hosted RepoGuide backend (`repoguide login`) and uses it for AI analysis and sync. No API key to manage yourself.
- **Offline mode** (`--offline`) never talks to the backend — no login, no sync, nothing leaves your machine. Session parsing, AI analysis, and storage all run locally (SQLite-backed). It still needs a way to call Claude, so first run asks you to pick one:
  - the `claude` CLI, if you're already logged in via Claude Code, or
  - an `ANTHROPIC_API_KEY` you provide.

  Pass `--approve` to skip that prompt and default to the `claude` CLI (useful for scripts/CI). Switch an already-initialized repo to offline mode later with:

  ```sh
  repoguide repo init --offline --force
  ```

## Contents

- `cmd/` - Cobra commands and user-facing command wiring
- `internal/` - session parsing, MCP integration, local runtime, config, and repo sync logic
- `brew/` - release and Homebrew packaging helpers ([`homebrew-tap`](https://github.com/repoguide-dev/homebrew-tap))

## Development

`go.mod` depends on [`repoguide-core`](https://github.com/repoguide-dev/repoguide-core) via a relative `replace` directive (`../repoguide-core`), so building standalone requires it checked out as a sibling directory:

```sh
git clone https://github.com/repoguide-dev/repoguide-core ../repoguide-core
go test ./...
go build .
```

Useful local checks:

```sh
go test ./cmd/...
go test ./internal/...
```

## Build And Run

Build the CLI directly:

```sh
go build .
```

Run it from this repo:

```sh
go run . --help
```
