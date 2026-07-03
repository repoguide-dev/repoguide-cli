# RepoGuide CLI

RepoGuide learns from past coding-agent sessions in a repo (what was searched, edited, tested, and fixed) and gives the next agent (Claude Code, Codex CLI, or any MCP-compatible agent) the files, workflows, tests, and pitfalls it needs before it starts searching from scratch. `repoguide-cli` is the local, offline `repoguide` command-line application: your machine, your model key, local storage, nothing synced anywhere.

Website: [repoguide.dev](https://repoguide.dev)

## Install

```sh
brew install --cask repoguide-dev/tap/repoguide
```

Binaries and checksums for every release are also published on [`repoguide-releases`](https://github.com/repoguide-dev/repoguide-releases).

## Contents

- `cmd/` - Cobra commands and user-facing command wiring
- `internal/` - session parsing, MCP integration, local runtime, config, and repo sync logic
- `brew/` - release and Homebrew packaging helpers ([`homebrew-tap`](https://github.com/repoguide-dev/homebrew-tap))
- `hindsight` - legacy binary artifact retained until packaging is cleaned up further

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
