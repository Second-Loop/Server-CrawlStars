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

