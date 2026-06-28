# 작업 흐름

이 문서는 Codex와 사람이 이 레포에서 일할 때 따르는 규칙입니다. 자세한 현재 상태는 `ai-docs/project-map.md`를 먼저 봅니다.

## 기준

- Linear issue가 작업 범위와 acceptance criteria의 기준입니다.
- GitHub PR은 구현 diff, review, CI 결과를 남기는 장소입니다.
- `main`에는 직접 push하지 않습니다. 작은 branch와 PR을 사용합니다.
- 완료를 주장하기 전에 validation을 실행하고 결과를 PR 또는 Linear에 남깁니다.
- gameplay, matchmaking, persistence, deployment platform은 Linear issue 범위가 있을 때만 추가합니다.

## 현재 단계

Phase E2: E1 server-authoritative core loop 위에 client-server integration surface를 붙이는 단계입니다.

이미 있는 서버 범위:

- `Step(inputs) -> Snapshot`
- movement, wall collision
- projectile 생성, 이동, destroy
- hit, HP, death snapshot
- room REST debug API
- room WebSocket snapshot stream
- room TTL cleanup
- simple `/matchmaking/join`
- matchmaking Ready event/ready ACK/countdown/start
- start 전 match cancel
- GameEnd Win/Lose/Draw event와 종료 room 정리
- server-hosted OpenAPI/AsyncAPI docs

아직 issue 없이 추가하지 않는 범위:

- production matchmaking
- ready timeout
- start 후 disconnect, bot replacement, ping/pong timeout
- respawn, score
- persistence, database, auth
- Kubernetes, dashboard, scheduler, runner

## Linear 규칙

Issue에는 최소한 다음이 있어야 합니다.

- 요약
- scope
- out of scope
- acceptance criteria
- validation
- 관련 issue 또는 PR link

상태 의미:

- `Backlog`: 아직 scope가 덜 잡힘
- `Todo`: 바로 시작 가능
- `In Progress`: branch 또는 active work 있음
- `In Review`: PR이 열림
- `Done`: PR merge 또는 non-code work 완료

Local validation 통과만으로 `Done`으로 옮기지 않습니다.

## GitHub 규칙

- Branch 이름은 가능하면 issue ID를 포함합니다. 예: `sl-58-match-start-state`
- PR 제목은 issue ID를 포함합니다. 예: `[SL-58] 매칭 시작 상태 전이 추가`
- PR은 한 번에 review 가능한 크기로 유지합니다.
- CI 실패 상태에서는 merge하지 않습니다.
- 후속 작업은 열린 PR을 계속 키우지 말고 Linear issue로 분리합니다.

PR 본문에는 짧게 적습니다.

```md
## 왜 해당 PR을 올렸나요?

- 핵심 이유를 1-3개 적습니다.

## 무엇을 어떻게 수정했나요?

- 변경 내용을 bullet로 적습니다.
```

## Commit 규칙

Linear ticket이 있으면 commit title에 붙입니다.

```text
[SL-58] feat(rooms): 매칭 시작 상태 전이 추가

- ready state message 추가
- countdown 이후 simulation start로 변경
- pre-start close regression test 추가
```

Ticket이 없으면 `[SL-58]` 부분만 생략합니다.

## Validation

기본 local validation:

```sh
make ci
```

계약 문서만 확인할 때:

```sh
make docs-build
```

`make ci`는 docs validation/build, `go vet`, `go test`, server build, deploy script syntax check를 함께 실행합니다. Clean checkout에서 `go test ./...`만 바로 실행하면 Go embed 대상 docs 파일이 없을 수 있으므로 공식 검증은 `make ci`입니다.

## 문서 업데이트

코드, workflow, architecture가 바뀌면 `ai-docs/`를 함께 확인합니다.

REST/WebSocket 계약이 바뀌면 같은 PR에서 다음을 확인합니다.

- `api/openapi.yaml`
- `api/asyncapi.yaml`
- `ai-docs/api-reference.md`
- `ai-docs/api-docs.md`
- 필요하면 `ai-docs/protocol.md`, `ai-docs/architecture.md`, `ai-docs/decisions.md`

## 문서 역할

- `ai-docs/project-map.md`: 현재 상태와 다음 작업
- `ai-docs/workflow.md`: 지금 읽는 작업 규칙
- `ai-docs/architecture.md`: package/runtime 책임
- `ai-docs/protocol.md`: simulation, WebSocket, matchmaking protocol 경계
- `ai-docs/api-reference.md`: 사람이 읽는 API 요약
- `ai-docs/api-docs.md`: OpenAPI/AsyncAPI 문서화 기준
- `ai-docs/deployment.md`: 배포와 Cloudflare Tunnel
- `ai-docs/decisions.md`: ADR 기록
