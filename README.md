# Server Crawl Stars

Brawl Stars 스타일 실시간 멀티플레이어 게임을 위한 Go 서버 레포지토리입니다.

이 레포지토리는 E1 서버 권위 core loop skeleton을 main에 반영했고, 지금은 E2 client-server integration을 위해 필요한 서버 surface를 작게 확장하는 단계입니다. 목표는 production matchmaking, persistence, full game service를 한 번에 만드는 것이 아니라, Linear issue 단위로 서버 기능과 계약을 검증 가능하게 늘리는 것입니다.

오랜만에 들어왔다면 먼저 `ai-docs/project-map.md`를 읽습니다. 현재 구조, 게임루프 흐름, 완료된 Linear 이슈, 다음 티켓 후보를 한 번에 볼 수 있습니다.

## 현재 범위

- Go module: `github.com/Second-Loop/Server-CrawlStars`
- `cmd/server`의 HTTP server entrypoint
- `/health`, `/openapi`, `/asyncapi`, `/matchmaking/join`, `/rooms`, room WebSocket endpoint
- `internal/simulation`의 `Step(inputs) -> Snapshot` server-authoritative core loop
- static map movement, wall collision, projectile movement, hit, HP/death snapshot
- `internal/rooms`의 in-memory room lifecycle, simple matchmaking connector, 30Hz WebSocket snapshot broadcast
- docs UI와 raw OpenAPI/AsyncAPI spec hosting
- format, vet, test, docs build, binary build를 실행하는 validation loop
- linux/amd64 서버 release packaging과 Oracle VM pull 방식 CD script
- `AGENTS.md`의 얇은 agent entrypoint
- `ai-docs/`의 architecture, protocol, workflow, deployment 문서

## 명령어

새 개발 머신에서는 먼저 mise toolchain을 설치합니다.

```sh
mise trust
mise install
```

```sh
make fmt
make vet
make test
make build
make deploy-check
make ci
```

로컬에서 서버 실행:

```sh
go run ./cmd/server
```

Health check:

```sh
curl http://127.0.0.1:8080/health
```

간단한 client-facing 매칭 connector:

```sh
curl -X POST http://127.0.0.1:8080/matchmaking/join
```

서버는 기본적으로 `127.0.0.1:8080`에 bind합니다. 다른 host에서 접근 가능해야 하는 경우에만 의도적으로 `SERVER_ADDR=:8080 go run ./cmd/server`를 사용합니다.

배포 문서는 `ai-docs/deployment.md`에 있습니다. production systemd unit도 `SERVER_ADDR=127.0.0.1:8080`을 설정합니다. Cloudflare Tunnel은 `api-crawlstars.tolerblanc.com`을 Go 서버로, `tolerblanc.com`을 local-only Caddy hello page로 노출합니다.

## 문서 지도

- `ai-docs/project-map.md`: 현재 repo A-Z, 게임루프, Linear 흐름, 다음 작업 후보
- `ai-docs/architecture.md`: package 책임과 서버 경계
- `ai-docs/protocol.md`: Step/snapshot/WebSocket/matchmaking protocol planning
- `ai-docs/api-reference.md`: 사람이 읽는 REST/WebSocket API 요약
- `ai-docs/api-docs.md`: OpenAPI/AsyncAPI 문서화 정책
- `ai-docs/server-todo.md`: 완료된 흐름과 다음 후보 티켓
- `ai-docs/workflow.md`: Linear/GitHub/CI 협업 규칙

## 작업 합의

작업 범위와 acceptance criteria의 source of truth는 Linear issue입니다. 구현 review는 GitHub branch와 pull request를 사용합니다. Agent는 `AGENTS.md`에서 시작해야 하며, 자세한 협업 규칙은 `ai-docs/workflow.md`에 있습니다.
