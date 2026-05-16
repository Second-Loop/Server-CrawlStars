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

## Near-Term Direction

The next architecture work should define the first vertical slice before implementation:

- process model
- protocol boundary
- room lifecycle vocabulary
- validation and test strategy
- observability basics

Avoid generalizing the game architecture before the first slice is chosen.

