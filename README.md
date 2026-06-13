# Server Crawl Stars

Brawl Stars 스타일 실시간 멀티플레이어 게임을 위한 Go 서버입니다.

현재 서버는 E1 server-authoritative core loop를 main에 반영했고, E2 client-server integration에 필요한 기능을 Linear issue 단위로 작게 늘리는 중입니다. 오랜만에 보면 [ai-docs/project-map.md](ai-docs/project-map.md)를 먼저 읽습니다.

## 지금 되는 것

- `GET /health`
- `POST /matchmaking/join`
- `/rooms` debug REST API
- `WS /rooms/{roomID}/players/{playerID}`
- 30Hz room-local game loop
- movement, wall collision, projectile movement, hit, HP/death snapshot
- OpenAPI/AsyncAPI raw spec과 docs UI
- GitHub Actions CI/CD와 Oracle VM pull deployment

아직 production matchmaking, persistence, auth, respawn, score/win-loss, bot replacement는 없습니다.

## 자주 쓰는 명령

```sh
mise trust
mise install
make ci
```

로컬 실행:

```sh
go run ./cmd/server
curl http://127.0.0.1:8080/health
curl -X POST http://127.0.0.1:8080/matchmaking/join
```

서버는 기본적으로 `127.0.0.1:8080`에 bind합니다. 외부 bind가 필요할 때만 명시적으로 `SERVER_ADDR=:8080 go run ./cmd/server`를 사용합니다.

## 문서 지도

- [ai-docs/project-map.md](ai-docs/project-map.md): 현재 구조, 게임루프, Linear 흐름, 다음 작업
- [ai-docs/workflow.md](ai-docs/workflow.md): Linear, GitHub, CI, docs 작업 규칙
- [ai-docs/architecture.md](ai-docs/architecture.md): package 책임과 runtime 구조
- [ai-docs/protocol.md](ai-docs/protocol.md): simulation, WebSocket, matchmaking protocol 경계
- [ai-docs/api-reference.md](ai-docs/api-reference.md): 사람이 읽는 API 요약
- [ai-docs/api-docs.md](ai-docs/api-docs.md): OpenAPI/AsyncAPI 문서화 기준
- [ai-docs/deployment.md](ai-docs/deployment.md): Oracle VM 배포와 Cloudflare Tunnel
- [ai-docs/decisions.md](ai-docs/decisions.md): ADR 형태의 결정 기록

작업 범위와 acceptance criteria는 Linear issue를 기준으로 합니다. Agent는 `AGENTS.md`에서 시작하고, 자세한 협업 규칙은 [ai-docs/workflow.md](ai-docs/workflow.md)를 따릅니다.
