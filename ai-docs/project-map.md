# Project Map

이 문서는 `Server-CrawlStars`를 오랜만에 다시 보는 사람이 현재 상태를 빠르게 복구하기 위한 지도입니다. 세부 계약은 각 전용 문서가 source of truth이고, 이 문서는 전체 흐름을 연결해서 설명합니다.

## 한 줄 요약

서버는 지금 E1 core loop skeleton을 넘어 E2 client-server integration을 준비하는 단계입니다. `POST /matchmaking/join`으로 room/player/WebSocket path를 받고, WebSocket으로 input을 보내면 서버가 30Hz room tick에서 `Step(inputs) -> Snapshot`을 돌려 snapshot을 broadcast합니다.

## 현재 되는 것

- Health check: `GET /health`
- Server-hosted docs: `GET /openapi`, `GET /asyncapi`, `GET /openapi.yaml`, `GET /asyncapi.yaml`
- Simple matchmaking connector: `POST /matchmaking/join`
- Manual room debug API: `GET/POST /rooms`, `GET /rooms/{roomID}`, `POST /rooms/{roomID}/players`, `POST /rooms/{roomID}/start`
- WebSocket room stream: `WS /rooms/{roomID}/players/{playerID}`
- Server-authoritative simulation core:
  - static 5x5 map fixture
  - red spawn `(1, 1)`, blue spawn `(3, 3)`
  - movement at 30Hz
  - wall and map-boundary collision
  - attack input and projectile creation
  - projectile movement
  - projectile-wall/boundary destruction
  - projectile-player hit
  - HP and `IsDead` snapshot
  - 2-player WebSocket synchronization regression test

## 아직 안 되는 것

- Production matchmaking queue or rating algorithm
- Match complete/loading/ready/countdown WebSocket event
- Client ready ACK before simulation start
- Start-before-game cancel and ready timeout
- Start-after-game disconnect policy, ping/pong timeout, bot replacement
- Respawn, score, win/loss
- Persistence, database, auth, rate limit
- Shared client/server constants file or asset-driven config
- Unity prediction/interpolation

## Repository Map

```text
cmd/server
  main.go
    HTTP server entrypoint
    wires health, docs, matchmaking, rooms

internal/health
  minimal health model and handler

internal/docs
  embeds built OpenAPI/AsyncAPI specs and docs UI

internal/rooms
  REST room lifecycle
  simple matchmaking connector
  WebSocket connection management
  room-local 30Hz ticker
  input collection and snapshot broadcast
  in-memory room TTL cleanup

internal/simulation
  transport-independent gameplay core
  State.Step(inputs) -> Snapshot
  map, movement, collision, projectile, hit, HP/death rules

api
  openapi.yaml
  asyncapi.yaml

docs-ui
  dependency-light docs validation and build scripts

scripts/deploy
  Oracle VM pull deployment and systemd scripts

ai-docs
  repo workflow, architecture, protocol, API, deployment, ticket planning docs
```

## Request Flow

### 1. Server Boot

`cmd/server/main.go` reads `SERVER_ADDR`; if absent it binds to `127.0.0.1:8080`.

It creates one `http.ServeMux` and mounts:

- `/health`
- `/openapi`
- `/asyncapi`
- `/openapi.yaml`
- `/asyncapi.yaml`
- `/matchmaking/join`
- `/rooms`
- `/rooms/`

The room routes share one `rooms.Store` with a max active room cap of 5.

### 2. Matchmaking Join

`POST /matchmaking/join` is a simple connector, not a production matchmaking queue.

Current behavior:

1. Cleanup expired rooms.
2. Find a waiting room with capacity.
3. If none exists, create a waiting room.
4. Add a player using the same rule as the manual room API.
5. If the room now has exactly 2 players, start the room immediately.
6. Return `room`, `player`, and `webSocketPath`.

Player assignment:

- `player-1`, `player-2`, ...
- even join index: red
- odd join index: blue
- slot: `playerIndex / 2`

Important boundary: the server currently starts the simulation as soon as the second player joins. It does not wait for client loading/ready ACK.

### 3. WebSocket Attach

Clients connect to:

```text
WS /rooms/{roomID}/players/{playerID}
```

Before accepting the upgrade, the server checks:

- room exists
- player belongs to the room
- the same room/player is not already connected

Waiting rooms accept WebSocket connections and input, but do not broadcast gameplay snapshots until the room is started.

### 4. Room Start

A room starts in two ways:

- Manual debug path: `POST /rooms/{roomID}/start`
- Simple matchmaking path: second player joins through `POST /matchmaking/join`

On start:

1. Room status becomes `started`.
2. `simulation.NewStateWithConfig` is created from issued players and `StaticMapFixture`.
3. A room-local ticker starts at `1 / simulation.TickRate`.
4. Each ticker event calls `Store.tickRoom`.

This is a room-local gameplay loop. It is not a reusable scheduler, runner, daemon, or orchestration framework.

### 5. Input Collection

The WebSocket read loop accepts JSON input:

```json
{
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": true
}
```

Invalid JSON sends:

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

Valid input is stored as one pending input per player. If the same player sends multiple inputs before the next tick, the newest pending input wins.

### 6. Tick And Simulation

Each started room tick:

1. Copy all pending inputs.
2. Clear pending inputs.
3. Call `room.state.Step(inputs)`.
4. Wrap the result as `{"Type":"snapshot","Snapshot":...}`.
5. Broadcast the same snapshot to all connected clients.

`internal/simulation.State.Step` is transport-independent. It does not know HTTP, WebSocket, rooms, or matchmaking.

Step order:

1. Move existing active projectiles.
2. Destroy projectiles that hit walls or leave map bounds.
3. Apply projectile-player hits for still-active projectiles.
4. Apply each player input.
5. For movement, process X axis then Y axis with wall collision.
6. If `PressedAttack = true` and `AttackDir` is non-zero, create a new projectile at the moved player position.
7. Increment tick.
8. Return cloned player/projectile snapshot.

New projectiles do not move on the same tick they are created. They first appear at the owner position, then move on the next tick.

### 7. Snapshot Shape

Server snapshot wrapper:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "Tick": 1,
    "Players": [],
    "Projectiles": []
  }
}
```

`PlayerData` currently exposes:

- `Id`
- `Team`
- `Slot`
- `Pos`
- `MoveDir`
- `AttackDir`
- `Speed`
- `Radius`
- `HP`
- `PressedAttack`
- `IsDead`

`ProjectileData` currently exposes:

- `Id`
- `OwnerId`
- `Pos`
- `Dir`
- `Speed`
- `Damage`
- `Radius`
- `Type`
- `IsDestroyed`

### 8. Field Semantics

`AttackDir` and `PressedAttack` are separate on purpose.

- `AttackDir` means the current aim direction.
- `PressedAttack` means the fire trigger for this tick.

If attack were inferred from `AttackDir != zero`, a client that keeps aim direction could accidentally fire every tick. Keeping a separate trigger lets the client preserve aim without firing.

`IsDead` is explicit even though it can be derived from `HP <= 0`.

- It keeps client rendering simple.
- It avoids duplicating death rules in every client.
- It leaves space for future states such as downed, respawning, invulnerable, or spectator-like states.

The snapshot `PressedAttack` field may be revisited later because it is mostly input echo/debug state, but removing it is a protocol change and should be a separate issue.

### 9. Room Cleanup

The server is in-memory only.

Cleanup rules:

- Waiting room idle TTL: 10 minutes
- Started room all-disconnected TTL: 5 minutes after last WebSocket client disconnects
- Hard room lifetime: 1 hour
- Connected clients prevent waiting idle TTL and all-disconnected TTL cleanup

Current WebSocket close behavior:

- It removes the client connection.
- It removes that player pending input.
- If the room is started and all clients are gone, it starts the disconnected TTL.
- It does not remove a waiting player from a pre-start match.
- It does not convert a started disconnected player into a bot.

## Linear History

### E0

- `SL-1`: project kickoff/bootstrap epic
- Server bootstrap, CI, CD packaging, Oracle VM pull deployment, Cloudflare Tunnel, Linear/GitHub workflow were established.

### E1

- `SL-7`: E1 server/client foundation epic
- `SL-38`: simulation domain and `Step(inputs) -> Snapshot`
- `SL-39`: map, movement, wall collision
- `SL-40`: attack/projectile skeleton
- `SL-41`: room REST debug lifecycle
- `SL-42`: WebSocket snapshot broadcast
- `SL-43`: room TTL cleanup and invalid input regression
- `SL-47`: OpenAPI/AsyncAPI hosted docs
- `SL-51`, `SL-52`: docs tooling and Swagger deployed-origin fixes

### E2 Current

- `SL-10`: client-server integration epic
- `SL-12`: user matchmaking parent issue
  - `SL-49` server simple matchmaking is done.
  - `SL-50` client matchmaking is in review.
  - `SL-58` server ready/loading/countdown/cancel follow-up is next.
- `SL-14`: client prototype logic migrated to server
  - `SL-53`: projectile movement and wall collision done.
  - `SL-54`: hit, HP, death snapshot done.
  - `SL-55`: 2-player WebSocket synchronization regression done.
  - `SL-56`: protocol validation docs done.
  - `SL-57` client logic split is in review.
- `SL-30`: shared constants/data management is in progress but still needs scope refinement.

## Recommended Next Ticket

Pick `SL-58` next: `E2-2-3 [Server] 매칭 준비/카운트다운 상태 전이`.

Why this first:

- The server already has simple join and gameplay snapshot stream.
- Client discussion expects match complete, loading/ready ACK, and countdown.
- This closes the biggest gap between "two clients can connect" and "a human-friendly match start flow exists".

Suggested scope:

- Keep `POST /matchmaking/join` response shape.
- Add WebSocket server message type for match state.
- Let waiting/matched clients send ready/loading-complete input.
- Start countdown only when all required clients are ready.
- Start simulation after countdown.
- Treat WebSocket close before start as match cancel/removal.

Out of scope:

- Post-start disconnect policy
- Bot replacement
- Ping/pong timeout
- Respawn, score, win/loss
- Production matchmaking queue
- Persistence

## Second Next Ticket

Pick `SL-30` after the match start flow is clear.

Suggested v1:

- Define one shared game config artifact with tick rate, tile size, map, player defaults, projectile defaults, and max players.
- Add validation that Go constants/defaults and the artifact do not drift.
- Document field names and units for Unity.

Do not start with hot reload, editor tooling, or 10-player expansion. Those are separate follow-ups.

## Useful Commands

```sh
make docs-build
make ci
go test ./internal/simulation
go test ./internal/rooms
go run ./cmd/server
curl http://127.0.0.1:8080/health
curl -X POST http://127.0.0.1:8080/matchmaking/join
```

## Where To Read Next

- `ai-docs/architecture.md`: package ownership and boundaries
- `ai-docs/protocol.md`: protocol state and future message planning
- `ai-docs/api-reference.md`: API shapes and manual validation
- `ai-docs/decisions.md`: ADR-style history
- `ai-docs/server-todo.md`: current ticket board
