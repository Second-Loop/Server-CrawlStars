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
  WebSocket connection adapter
  room-local 30Hz ticker
  TTL cleanup

internal/simulation
  transport-independent gameplay core
  State.Step(inputs) -> Snapshot
  map, movement, collision, projectile, hit, HP/death rule
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
- static map fixture max players = `6`

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
- respawn, score, win/loss는 아직 없습니다.

## Room과 WebSocket

REST debug API:

- `GET /rooms`
- `POST /rooms`
- `GET /rooms/{roomID}`
- `POST /rooms/{roomID}/players`
- `POST /rooms/{roomID}/start`

Simple matchmaking:

- `POST /matchmaking/join`
- waiting room을 찾거나 만듭니다.
- player를 발급합니다.
- 2명이 되면 바로 room을 start합니다.
- response는 `room`, `player`, `webSocketPath`를 포함합니다.

WebSocket:

- `WS /rooms/{roomID}/players/{playerID}`
- 발급된 room/player만 연결할 수 있습니다.
- waiting room은 input을 받을 수 있지만 snapshot을 보내지 않습니다.
- started room은 30Hz로 snapshot을 broadcast합니다.
- invalid input은 error message만 보내고 연결은 유지합니다.

## Cleanup

Room store는 in-memory라 TTL이 중요합니다.

- waiting idle TTL: 10분
- started all-disconnected TTL: 5분
- hard lifetime: 1시간
- connected client가 있으면 idle/all-disconnected cleanup을 막습니다.

## 의도적으로 없는 것

- production matchmaking queue/rating
- persistence/database/auth
- generic scheduler/runner/orchestration
- dashboard
- Kubernetes
- respawn, score, win/loss
- bot replacement

다음 architecture 확장은 `SL-58` match start state transition과 `SL-30` shared constants/config를 우선합니다.
