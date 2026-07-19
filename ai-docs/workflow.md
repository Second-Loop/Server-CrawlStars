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
- client SL-79 `Map_0`과 Ground/Wall/SpawnPoint/Bush/Water tile 계약
- movement와 entity별 collision(Player는 Wall/Water, projectile은 Wall, boundary는 둘 다)
- projectile 생성, 이동, destroy
- hit, HP, death snapshot
- 기본 비활성화된 room REST debug API와 Bearer 보호
- player session token으로 보호된 room WebSocket snapshot stream
- room TTL cleanup
- simple `/matchmaking/join`
- `/matchmaking/join` IP별 token-bucket rate limit
- matchmaking Ready event/ready ACK/countdown/start
- sessionless server-owned bot participant와 human-only Ready quorum
- 첫 human join 기준 10초 bot fill, timer/human join first-lock-wins, failure rollback/no-retry
- start 전 match cancel
- GameEnd Win/Lose/Draw event와 종료 room 정리
- server-hosted OpenAPI/AsyncAPI docs
- JSON room/WebSocket lifecycle log와 loopback 전용 Prometheus metrics
- application HTTP와 private metrics의 coordinated graceful shutdown/HTTP timeout

아직 issue 없이 추가하지 않는 범위:

- production matchmaking
- ready timeout
- bot replacement, reconnect grace, ClientTick/ACK(SL-94)
- respawn, score
- persistence, database, account auth
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

계약 문서는 다음 순서를 고정합니다.

```sh
rtk node docs-ui/scripts/validate.mjs
REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_UPDATE_NOTICE=true rtk npx --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml
rtk npx --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error
rtk node docs-ui/scripts/build.mjs
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/docs -count=1
```

1. `validate.mjs`가 source marker와 cross-file 구조를 확인합니다.
2. Pinned Redocly `2.38.0`과 AsyncAPI CLI `6.0.2`가 YAML parse/schema를 공식 검증합니다.
3. Source validator가 모두 통과한 뒤에만 `build.mjs`로 ignored embed output을 갱신합니다.
4. 마지막 Go docs test가 새 generated spec/UI를 실제 handler에서 읽어 확인합니다.

`make ci`는 docs validation/build, `go vet`, `go test`, server build, network를 쓰지 않는 deploy 회귀 테스트, deploy script syntax check를 함께 실행합니다. Deploy만 빠르게 확인할 때는 `make deploy-test`를 실행합니다. Clean checkout에서 `go test ./...`만 바로 실행하면 Go embed 대상 docs 파일이 없을 수 있으므로 공식 검증은 `make ci`입니다.

`docs-ui/scripts/validate.mjs`는 필수 marker와 secret-free DTO 경계를 빠르게 확인하지만 YAML parser는 아닙니다. 따라서 이 script가 통과해도 pinned Redocly/AsyncAPI CLI 두 command를 생략하거나 `build.mjs`를 먼저 실행하지 않습니다.

## 문서 업데이트

코드, workflow, architecture가 바뀌면 `ai-docs/`를 함께 확인합니다.

REST/WebSocket 계약이 바뀌면 같은 PR에서 다음을 확인합니다.

- `api/openapi.yaml`
- `api/asyncapi.yaml`
- `ai-docs/api-reference.md`
- `ai-docs/api-docs.md`
- 필요하면 `ai-docs/protocol.md`, `ai-docs/architecture.md`, `ai-docs/decisions.md`
- 보안 환경 변수나 proxy trust가 바뀌면 `ai-docs/deployment.md`

## 문서 역할

- `ai-docs/project-map.md`: 현재 상태와 다음 작업
- `ai-docs/workflow.md`: 지금 읽는 작업 규칙
- `ai-docs/architecture.md`: package/runtime 책임
- `ai-docs/protocol.md`: simulation, WebSocket, matchmaking protocol 경계
- `ai-docs/api-reference.md`: 사람이 읽는 API 요약
- `ai-docs/api-docs.md`: OpenAPI/AsyncAPI 문서화 기준
- `ai-docs/deployment.md`: 배포와 Cloudflare Tunnel
- `ai-docs/decisions.md`: ADR 기록
