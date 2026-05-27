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

## ADR-0004: Expose HTTPS Through Cloudflare Tunnel

Status: Accepted

Context: SL-35 needs public HTTPS hostnames for the Go server while keeping the Go process private to the VM. OCI public inbound changes should be avoided for now, so direct Caddy `80/tcp` and `443/tcp` ingress is not the selected production path.

Decision: Keep the Go server on `127.0.0.1:8080` and run a Cloudflare Tunnel connector on the VM. Cloudflare routes `api-crawlstars.tolerblanc.com` to `http://127.0.0.1:8080`. The apex `tolerblanc.com` routes to local Caddy on `http://127.0.0.1:8081`, which returns a minimal hello response. Cloudflare owns public HTTPS at the edge; Caddy is local-only in this tunnel-backed setup.

Consequences:

- OCI public inbound does not need application `80/tcp` or `443/tcp` for this path; the connector makes outbound connections to Cloudflare.
- The Go server port should not be opened in VM firewall, OCI Security Lists, or NSGs.
- WebSocket traffic can use the same Cloudflare Tunnel hostname when the Go server implements a WebSocket endpoint.
- Caddy still runs under systemd, but it only listens on `127.0.0.1:8081` for the apex hello page.

## ADR-0005: Document REST With OpenAPI And WebSocket Messages With AsyncAPI

Status: Accepted

Context: E1 needs a small contract surface for room lifecycle, client input, and server snapshot flows. REST endpoints should be easy to inspect and manually call, while WebSocket gameplay traffic is a bidirectional stream that Swagger UI does not model well.

Decision: Use OpenAPI 3.x for REST APIs and Swagger UI when an interactive REST page is added. Use AsyncAPI for WebSocket channels and message payloads. OpenAPI may reference `ws://` or `wss://` server URLs, but AsyncAPI is the source of truth for WebSocket input and snapshot streams.

Consequences:

- REST and WebSocket contracts can evolve independently while sharing schema vocabulary where useful.
- E1 debug APIs must be clearly marked as unstable and E1-only until promoted.
- The first implementation issue that adds spec files should validate both OpenAPI and AsyncAPI documents.
- The preferred hosted paths are `/docs/rest`, `/docs/ws`, `/docs/openapi.yaml`, and `/docs/asyncapi.yaml`.
