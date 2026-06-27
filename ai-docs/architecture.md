# 아키텍처

서버는 아직 작게 유지합니다. E1 core loop는 들어왔고, 현재는 E2 client-server integration을 위해 필요한 표면만 issue 단위로 추가합니다.

## Package 책임

```text
cmd/server
  process entrypoint와 route wiring

internal/health
  /health model과 handler

internal/docs
  OpenAPI/AsyncAPI raw spec과 docs UI embed

internal/rooms
  in-memory room store
  REST debug lifecycle
  simple matchmaking connector
  match ready/starting state
  WebSocket connection adapter
  room-local 30Hz ticker
  GameEnd event와 종료 room 정리
  TTL cleanup

internal/simulation
  transport-independent gameplay core
  State.Step(inputs) -> Snapshot
  map, movement, collision, projectile, hit, HP/death rule
  default map fixture loader
```

`internal/simulation`은 HTTP, WebSocket, room lifecycle, matchmaking을 모릅니다. `internal/rooms`가 REST/WebSocket transport와 room state를 맡고, tick마다 simulation을 호출합니다.

## Runtime

```text
GitHub Actions
  -> linux/amd64 binary package
  -> GitHub Release asset

Oracle VM
  -> latest release pull
  -> /opt/crawl-stars-server/releases/<sha>
  -> current symlink 전환
  -> systemd restart

Cloudflare Tunnel
  -> api-crawlstars.tolerblanc.com -> 127.0.0.1:8080
  -> tolerblanc.com                -> 127.0.0.1:8081 Caddy hello
```

Go server는 production에서도 `127.0.0.1:8080`에 bind합니다. Public HTTPS edge는 Cloudflare Tunnel입니다. Caddy는 apex hello page용 local service입니다.

## Simulation core

현재 계약:

```text
State.Step(inputs []InputCommand) Snapshot
```

핵심 값:

- `TickRate = 30`
- `TileSize = 1.2`
- player speed/radius/HP = `2`, `0.5`, `100`
- projectile speed/damage/radius = `13`, `10`, `0.3`
- default map fixture path = `internal/simulation/fixtures/default-map.json`
- fixture load/validation failure fallback = 5x5 static map, max players `6`
- player spawn = map의 `TileSpawnPoint(2)`를 join 순서대로 사용, 없으면 legacy 5x5 좌표 fallback

Movement:

- `MoveDir * Speed * TickDuration`으로 이동합니다.
- X축과 Y축을 분리해 wall collision을 검사합니다.
- wall rectangle에 닿거나 map 밖으로 나가면 해당 axis movement를 무시합니다.
- non-finite input은 무시합니다.
- player-player collision은 아직 없습니다.

Attack/projectile:

- `PressedAttack = true`이고 `AttackDir`가 zero가 아니면 projectile을 만듭니다.
- 새 projectile은 이동 후 player 위치에서 생성됩니다.
- 기존 projectile은 tick마다 `Dir * Speed * TickDuration`으로 이동합니다.
- wall 또는 boundary에 닿으면 `IsDestroyed = true`가 됩니다.
- destroyed projectile은 snapshot에 남지만 더 움직이지 않습니다.

Hit/death:

- owner가 아닌 live player와 projectile circle이 겹치면 hit입니다.
- hit projectile은 destroyed가 됩니다.
- target HP는 projectile damage만큼 감소합니다.
- HP가 0 이하가 되면 `HP = 0`, `IsDead = true`입니다.
- respawn, score는 아직 없습니다.

## Room과 WebSocket

REST debug API:

- `GET /rooms`
- `POST /rooms`
- `DELETE /rooms`
- `GET /rooms/{roomID}`
- `DELETE /rooms/{roomID}`
- `POST /rooms/{roomID}/players`
- `POST /rooms/{roomID}/start`

Room response에는 서버 simulation이 쓰는 `map` 데이터와 마지막 tick의 `latestSnapshot` summary가 포함됩니다. 외부 응답의 `map` row는 Base64 문자열이 아니라 JSON number array로 직렬화합니다. `DELETE` debug API는 in-memory room을 삭제하고 room-local ticker와 WebSocket connection을 닫습니다.

`cmd/server`는 시작할 때 `internal/simulation/fixtures/default-map.json`을 로드해 `rooms.StoreConfig`로 주입합니다. fixture를 읽지 못하거나 shape 검증에 실패하면 `internal/simulation.StaticMapFixture()`를 사용합니다.

Simple matchmaking:

- `POST /matchmaking/join`
- waiting room을 찾거나 만듭니다.
- player를 발급합니다.
- 2명이 되면 room을 matched 상태로 잠그고 late join을 막습니다.
- 두 WebSocket client가 연결되면 `Type: Ready` event로 map과 player별 spawn 위치를 보냅니다.
- 두 client가 `Type: ready`를 보내면 starting 신호를 1번 보내고, server 내부 5초 countdown 후 room을 start합니다.
- response는 `room`, `player`, `webSocketPath`를 포함합니다.

WebSocket:

- `WS /rooms/{roomID}/players/{playerID}`
- 발급된 room/player만 연결할 수 있습니다.
- waiting room은 input을 받을 수 있지만 snapshot을 보내지 않습니다.
- matchmaking ready 단계는 `Type: Ready` event로 렌더 준비 데이터를 보내고, starting 단계는 `Type: snapshot` wrapper 안에서 lowercase `Snapshot.status`와 `Snapshot.countdown: 5`를 1번 보냅니다.
- started room은 `Snapshot.status: started`와 함께 30Hz gameplay snapshot을 broadcast합니다.
- HP가 0인 player가 생기면 같은 tick의 snapshot 뒤 player별 `Type: GameEnd` event를 보내고 room을 정리합니다.
- 한 명만 사망하면 생존 player는 `Win`, 사망 player는 `Lose`입니다. 같은 tick에 양쪽 player가 동시에 사망하면 v1에서는 양쪽 모두 `Lose`입니다.
- WebSocket write deadline은 10ms입니다. 느린 client write가 tick loop를 초 단위로 밀지 않게 하기 위한 개발 서버 budget입니다.
- invalid input은 error message만 보내고 연결은 유지합니다.

## Cleanup

Room store는 in-memory라 TTL이 중요합니다.

- waiting idle TTL: 10분
- started all-disconnected TTL: 5분
- hard lifetime: 1시간
- connected client가 있으면 idle/all-disconnected cleanup을 막습니다.
- matchmaking start 전 WebSocket close는 match cancel로 room과 남은 connection을 정리합니다.
- GameEnd가 발생한 started room은 결과 event 전송 후 room-local ticker와 WebSocket connection을 정리합니다.

## 의도적으로 없는 것

- production matchmaking queue/rating
- persistence/database/auth
- generic scheduler/runner/orchestration
- dashboard
- Kubernetes
- respawn, score
- bot replacement

공유 gameplay config는 `client-config/game-config.json`입니다. 서버 repo가 source of truth를 갖고, server binary는 이 JSON을 embed해서 room store와 simulation 기본값으로 사용합니다. Client CI는 `client-config`만 sparse checkout해 Unity runtime asset 경로로 복사할 수 있습니다. 이 config에는 tick rate, tile size, player/projectile type별 기본값, map이 들어갑니다.
