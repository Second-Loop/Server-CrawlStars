# Server Crawl Stars

Go server repository for a Brawl Stars-style real-time multiplayer game.

This repository is in the bootstrap phase. The current goal is not gameplay logic, matchmaking, physics, or persistence. The goal is a small, repeatable server development and deployment loop that works with Linear issues, GitHub pull requests, CI, and an Oracle VM pull-based CD path.

## Current Scope

- Go module: `github.com/Second-Loop/Server-CrawlStars`
- Minimal HTTP server entrypoint in `cmd/server`
- Health package and tests in `internal/health`
- GitHub Actions CI for format, vet, test, and build
- GitHub Actions CD packaging for linux/amd64 server releases
- VM pull deployment scripts for systemd-managed releases
- Thin agent entrypoint in `AGENTS.md`
- Shared workflow documentation in `ai-docs/`

## Commands

```sh
make fmt
make vet
make test
make build
make deploy-check
make ci
```

Run the server locally:

```sh
go run ./cmd/server
```

Health check:

```sh
curl http://localhost:8080/health
```

Deployment docs live in `ai-docs/deployment.md`. The production systemd unit binds the server to `127.0.0.1:8080` by setting `SERVER_ADDR`.

## Working Agreement

Use Linear issues as the source of truth for task scope and acceptance criteria. Use GitHub branches and pull requests for implementation review. Agents should start with `AGENTS.md`; detailed collaboration rules live in `ai-docs/workflow.md`.
