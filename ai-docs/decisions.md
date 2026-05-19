# Decisions

## ADR-0001: Start With A Minimal Go HTTP Server

Status: Accepted

Context: The repository needs CI-verifiable code before gameplay systems exist.

Decision: Start with a small Go module, a `cmd/server` entrypoint, and an `internal/health` package exposing `/health`.

Consequences:

- CI can validate format, vet, tests, and build immediately.
- The server has a concrete executable without committing to gameplay architecture.
- Future networking decisions remain open.

## ADR-0002: Keep Symphony Borrowing To Workflow Rules

Status: Accepted

Context: The project wants issue-driven, review-gated collaboration without building orchestration infrastructure.

Decision: Borrow issue-as-source-of-truth, acceptance criteria, validation, PR review, and repository workflow docs. Do not build a scheduler, runner, polling daemon, web dashboard, or automatic merge loop.

Consequences:

- The process is explicit and versioned in the repo.
- Automation can be added later only when justified by Linear-scoped work.

## ADR-0003: Use VM Pull CD With systemd For The Initial Oracle VM Runtime

Status: Accepted

Context: SL-6 needs a small deployment path for the Go server after `main` updates. The VM has SSH access and passwordless sudo, but no Docker, Cloudflare Tunnel, Tailscale, nginx, caddy, or required public app port. OCI Security List and NSG changes are outside the issue scope.

Decision: GitHub Actions builds a linux/amd64 tarball and publishes both a workflow artifact and a GitHub Release asset. The Oracle VM pulls the latest release asset, installs it under `/opt/crawl-stars-server/releases/<sha>/`, flips `/opt/crawl-stars-server/current`, restarts a systemd service, and checks `http://127.0.0.1:8080/health`.

Consequences:

- No inbound application port is required for deployment.
- GitHub Release assets keep the public-repo pull path simple for the VM.
- The server process is managed by systemd instead of Docker, PM2, or Kubernetes.
- Rollback is a symlink switch back to `/opt/crawl-stars-server/previous` plus systemd restart.
