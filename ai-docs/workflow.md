# Workflow

이 레포지토리는 OpenAI Symphony 운영 모델의 일부만 차용합니다. Issue가 task source of truth이고, 작업은 acceptance criteria 기준으로 scope가 정해지며, 완료에는 review와 validation이 필요합니다.

이 레포지토리는 Symphony의 scheduler, runner, daemon, multi-agent orchestration, dashboard, automatic PR loop를 구현하지 않습니다.

## Project Overview

`Server-CrawlStars`는 Brawl Stars 스타일 실시간 멀티플레이어 게임을 위한 Go server repository입니다. Unity client는 별도 레포지토리에서 관리합니다.

현재 레포지토리는 E1 서버 권위 core loop skeleton을 준비하는 단계입니다. 안정적인 인간 + Codex 개발 workflow를 유지하면서, issue 단위로 gameplay server 기반을 확장합니다.

## Current Phase

Phase E1: server-authoritative core loop skeleton.

이미 완료된 E0 범위:

- Go project initialization
- Minimal server entrypoint
- Health check code and tests
- GitHub Actions CI
- GitHub Actions CD packaging
- Oracle VM pull-based systemd deployment scripts
- Linear issue workflow
- GitHub branch and PR workflow
- Shared documentation in `ai-docs/`

E1에서 아직 scope가 명시되기 전에는 제외되는 작업:

- Production matchmaking
- Full gameplay loop
- Persistence
- Database and ORM
- Kubernetes
- Scheduler, runner, or multi-agent orchestration
- Admin or web dashboards

## What Codex Should Do

- `AGENTS.md`를 먼저 읽습니다.
- Linear access가 가능하면 구현 전에 활성 Linear issue를 읽습니다.
- 편집 전에 scope, acceptance criteria, validation을 확인합니다.
- 작고 issue 크기에 맞는 변경을 선호합니다.
- code, tests, CI, docs를 함께 맞춥니다.
- 최종 작업 요약 또는 PR에 validation command와 result를 남깁니다.
- 불확실한 architecture decision은 `ai-docs/decisions.md`에 기록합니다.

## What Codex Must Not Do

- 활성 issue 범위를 넘어서 확장하지 않습니다.
- Linear issue 없이 gameplay system을 추가하지 않습니다.
- Issue 없이 persistent storage, deployment platform, orchestration을 추가하지 않습니다.
- Test와 CI validation이 통과하기 전에 완료로 표시하지 않습니다.
- PR review와 CI가 통과하기 전에는 local success를 완료로 취급하지 않습니다.

## Default Flow

1. Linear issue를 선택합니다.
2. Scope, acceptance criteria, validation을 확인합니다.
3. Issue ID를 포함한 branch를 만듭니다.
4. 가장 작은 일관된 변경을 만듭니다.
5. Local validation을 실행합니다.
6. PR을 엽니다.
7. CI와 human review를 기다립니다.
8. Linear에 status 또는 blocker를 업데이트합니다.

## Linear Issue Workflow

Linear는 task intent의 source of truth입니다.

각 issue는 다음을 포함해야 합니다.

- Summary
- Scope
- Acceptance criteria
- Validation command 또는 check
- 관련 issue 또는 decision link

Current project:

- Linear project: `Crawl Stars`
- Team: `Second Loop` (`SL`)
- Server bootstrap issue: `SL-3`

## GitHub Branch And PR Workflow

- Initial repository bootstrap 이후에는 `main`에 직접 push하지 않습니다.
- 가능하면 branch name에 Linear issue ID를 포함합니다.
- PR은 한 번에 review할 수 있을 만큼 작아야 합니다.
- PR은 Linear issue와 연결해야 합니다.
- CI 통과와 human review가 끝난 뒤 merge합니다.

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

CI는 pull request와 `main` push에서 실행되어야 합니다.

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

Behavior, workflow, architecture가 바뀌면 docs를 업데이트합니다.

- `AGENTS.md`: agent를 위한 얇은 entrypoint
- `ai-docs/architecture.md`: server architecture 개요
- `ai-docs/workflow.md`: 상세 협업 workflow
- `ai-docs/linear-control.md`: Linear SSOT 및 issue control model
- `ai-docs/github.md`: GitHub PR 및 review convention
- `ai-docs/ci.md`: CI contract 및 local validation
- `ai-docs/deployment.md`: Oracle VM CD 및 systemd deployment note
- `ai-docs/api-docs.md`: REST 및 WebSocket 문서화 정책
- `ai-docs/protocol.md`: protocol planning note
- `ai-docs/server-todo.md`: 가까운 server 작업
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
