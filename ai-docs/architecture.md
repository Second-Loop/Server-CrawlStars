# Architecture

## Phase

This repository is in bootstrap phase. The server architecture is intentionally small.

## Current Shape

```text
cmd/server
  process entrypoint

internal/health
  health status model
  HTTP health handler
```

The server currently exposes a minimal `/health` endpoint for local and CI validation. It does not implement gameplay, rooms, matchmaking, persistence, physics, or networking protocols for Unity clients.

## Runtime Deployment Shape

The initial Oracle VM runtime is intentionally direct:

```text
GitHub Actions
  builds linux/amd64 tarball
  publishes GitHub artifact and Release asset

Oracle VM
  pulls the Release asset
  stores immutable releases under /opt/crawl-stars-server/releases/<sha>
  switches /opt/crawl-stars-server/current
  runs /opt/crawl-stars-server/current/crawl-stars-server through systemd

Cloudflare
  terminates public HTTPS for tolerblanc.com
  routes api-crawlstars.tolerblanc.com through Cloudflare Tunnel to 127.0.0.1:8080
  routes tolerblanc.com through Cloudflare Tunnel to local Caddy on 127.0.0.1:8081
```

The systemd unit sets `SERVER_ADDR=127.0.0.1:8080`. Cloudflare Tunnel is the public exposure path. Caddy is local-only for the apex hello page. Tailscale, Docker, Kubernetes, and dashboards are outside the current scope.

## Near-Term Direction

The next architecture work should define the first vertical slice before implementation:

- process model
- protocol boundary
- room lifecycle vocabulary
- validation and test strategy
- observability basics

Avoid generalizing the game architecture before the first slice is chosen.
