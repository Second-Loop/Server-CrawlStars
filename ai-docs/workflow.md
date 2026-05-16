# Workflow

This repository borrows a small part of OpenAI Symphony's operating model: issues are the task source of truth, work is scoped by acceptance criteria, and completion requires review plus validation.

It does not implement Symphony's scheduler, runner, daemon, multi-agent orchestration, dashboard, or automatic PR loop.

## Project Overview

`Server-CrawlStars` is the Go server repository for a Brawl Stars-style real-time multiplayer game. The Unity client is maintained separately.

The current repository phase is bootstrap. The purpose is to establish a stable human plus Codex development workflow before building gameplay systems.

## Current Phase

Phase E0: repository and collaboration foundation.

In scope:

- Go project initialization
- Minimal server entrypoint
- Health check code and tests
- GitHub Actions CI
- Linear issue workflow
- GitHub branch and PR workflow
- Shared documentation in `ai-docs/`

Out of scope:

- Game room server
- Matchmaking
- WebSocket gameplay loop
- Physics simulation
- Client prediction and reconciliation
- Database and ORM
- Kubernetes
- Scheduler, runner, or multi-agent orchestration
- Admin or web dashboards

## What Codex Should Do

- Read `AGENTS.md` first.
- Read the active Linear issue before implementation when Linear access is available.
- Confirm scope, acceptance criteria, and validation before editing.
- Prefer small, issue-sized changes.
- Keep code, tests, CI, and docs aligned.
- Leave validation commands and results in the final work summary or PR.
- Record uncertain architectural decisions in `ai-docs/decisions.md`.

## What Codex Must Not Do

- Do not expand scope beyond the active issue.
- Do not introduce gameplay systems during bootstrap.
- Do not add persistent storage, deployment platforms, or orchestration without an issue.
- Do not mark work complete before tests and CI validation pass.
- Do not treat local success as complete until PR review and CI pass.

## Default Flow

1. Select a Linear issue.
2. Confirm scope, acceptance criteria, and validation.
3. Create a branch containing the issue ID.
4. Make the smallest coherent change.
5. Run local validation.
6. Open a PR.
7. Wait for CI and human review.
8. Update Linear with status or blockers.

## Linear Issue Workflow

Linear is the source of truth for task intent.

Each issue should include:

- Summary
- Scope
- Acceptance criteria
- Validation commands or checks
- Links to related issues or decisions

Current project:

- Linear project: `Crawl Stars`
- Team: `Second Loop` (`SL`)
- Server bootstrap issue: `SL-3`

## GitHub Branch And PR Workflow

- Do not push directly to `main` after the initial repository bootstrap.
- Branch names should include the Linear issue ID when possible.
- PRs should be small enough to review in one sitting.
- PRs should link the Linear issue.
- Merge only after CI passes and a human review is complete.

Suggested branch naming:

```text
sl-3-server-bootstrap
```

PR body checklist:

- Linked Linear issue
- Summary of changes
- Validation performed
- Known risks or follow-ups

## CI Validation Rules

CI must run on pull requests and pushes to `main`.

Required checks:

- `go mod download`
- `gofmt` check
- `go vet ./...`
- `go test ./...`
- `go build ./cmd/server`

Local equivalent:

```sh
make ci
```

## Documentation Update Rules

Update docs when behavior, workflow, or architecture changes.

- `AGENTS.md`: thin entrypoint for agents
- `ai-docs/architecture.md`: server architecture overview
- `ai-docs/workflow.md`: detailed collaboration workflow
- `ai-docs/linear-control.md`: Linear SSOT and issue control model
- `ai-docs/github.md`: GitHub PR and review conventions
- `ai-docs/ci.md`: CI contract and local validation
- `ai-docs/protocol.md`: protocol planning notes
- `ai-docs/server-todo.md`: near-term server work
- `ai-docs/decisions.md`: lightweight ADR log

## Task Template

```md
## Summary

## Scope

## Out Of Scope

## Acceptance Criteria

- [ ]

## Validation

- [ ]

## Notes / Risks
```

## PR Checklist

- [ ] Linear issue linked
- [ ] Scope matches issue
- [ ] Tests added or updated when behavior changes
- [ ] `make ci` passes locally
- [ ] Docs updated or confirmed unchanged
- [ ] Risks and follow-ups documented
