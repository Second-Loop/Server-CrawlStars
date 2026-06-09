# API Reference

## Scope

이 문서는 SL-49 기준 simple client matchmaking API와 E1 debug API를 사람이 읽기 쉬운 형태로 요약합니다. Raw machine-readable source of truth는 다음 파일입니다.

```text
api/openapi.yaml
api/asyncapi.yaml
```

Running server는 같은 내용을 다음 path로 제공합니다.

```text
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
```

`/openapi`와 `/asyncapi`는 human-readable UI입니다. `/openapi` Swagger UI의 기본 server는 현재 접속한 server origin(`/`)이며, `http://localhost:8080`은 local development 선택지로 유지합니다. `/openapi.yaml`과 `/asyncapi.yaml`은 client, test, tool이 읽는 raw spec입니다.

## REST API

REST endpoint는 client-facing simple matchmaking surface와 E1 room lifecycle을 수동 검증하기 위한 debug surface를 함께 제공합니다.

```text
GET /health
POST /matchmaking/join
GET /rooms
POST /rooms
GET /rooms/{roomID}
POST /rooms/{roomID}/players
POST /rooms/{roomID}/start
```

Matchmaking join response shape:

```json
{
  "room": {
    "id": "room-1",
    "status": "waiting",
    "players": [
      {
        "id": "player-1",
        "team": "red",
        "slot": 0
      }
    ],
    "maxPlayers": 6,
    "latestSnapshot": {
      "tick": 0,
      "playerCount": 1,
      "projectileCount": 0
    }
  },
  "player": {
    "id": "player-1",
    "team": "red",
    "slot": 0
  },
  "webSocketPath": "/rooms/room-1/players/player-1"
}
```

`POST /matchmaking/join`은 waiting room 중 여유가 있는 room에 player를 배정하고, 없으면 새 room을 만듭니다. 두 번째 player가 같은 waiting room에 들어오면 room simulation을 자동으로 start합니다. Matchmaking path는 이미 `started`인 room에 late join하지 않고 새 waiting room을 찾거나 만듭니다.

Room response shape:

```json
{
  "id": "room-1",
  "status": "waiting",
  "players": [
    {
      "id": "player-1",
      "team": "red",
      "slot": 0
    }
  ],
  "maxPlayers": 6,
  "latestSnapshot": {
    "tick": 0,
    "playerCount": 1,
    "projectileCount": 0
  }
}
```

Error response shape:

```json
{
  "error": {
    "code": "room_not_found",
    "message": "room not found"
  }
}
```

Current REST error codes:

- `room_not_found`
- `room_cap_reached`
- `room_full`
- `room_has_no_players`
- `method_not_allowed`
- `not_found`

## WebSocket API

WebSocket endpoint는 REST로 생성한 room/player를 사용합니다.

```text
WS /rooms/{roomID}/players/{playerID}
```

Client input message:

```json
{
  "MoveDir": {
    "x": 1,
    "y": 0
  },
  "AttackDir": {
    "x": 0,
    "y": 1
  },
  "PressedAttack": false
}
```

Server snapshot message:

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

Invalid input error message:

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

Client-facing WebSocket field names intentionally follow the Unity prototype vocabulary: `MoveDir`, `AttackDir`, `PressedAttack`, `Type`, `Snapshot`, `Error`, `Id`, `OwnerId`, `Pos`, `Dir`, `HP`, `IsDead`, `IsDestroyed`.

Projectile snapshots keep destroyed projectiles visible with `IsDestroyed: true`. Existing projectiles move on later ticks by `Dir * Speed * TickDuration`, and wall or map-boundary collision marks them destroyed.

Player snapshots expose `HP` as current health. Projectile hits against non-owner live players subtract `Damage`; hit projectiles are destroyed, and players at `HP <= 0` are reported with `HP: 0` and `IsDead: true`.

## Two-Player Validation Scenario

For a manual server check, create or join one room with two players, open both WebSocket paths, then compare the snapshot stream from both connections.

Expected checks:

- Both clients receive the same `Snapshot.Tick`, `Players`, and `Projectiles` data for each broadcast tick.
- Movement input from one player changes that player's `Pos` in both streams.
- Projectile hits reduce the target player's `HP` and mark the projectile `IsDestroyed: true` in both streams.
- Repeated hits eventually report the target as `HP: 0` and `IsDead: true` in both streams.
- Invalid JSON input returns an `invalid_input` error message without stopping later snapshot broadcasts.

Map note: red starts at map `(1, 1)` and blue starts at map `(3, 3)`. A direct diagonal red-to-blue attack crosses the center wall, so manual hit validation should move red into blue's column before attacking downward. The automated regression for this scenario is covered by `go test ./internal/rooms`.

## E1 Constraints

These APIs are development surfaces. `POST /matchmaking/join` is a simple client-facing connector for SL-49, while `/rooms` remains the manual debug lifecycle API. They do not implement production authentication, rate limiting, matchmaking algorithm, production queue, persistence, gameplay scoring, respawn, admin dashboard, scheduler, or Kubernetes deployment.

The current player cap is `simulation.StaticMapFixture().MaxPlayers = 6`. Ten-player expansion is intentionally out of scope.

Shared constants such as tick rate, tile size, player speed/radius/HP, projectile speed/damage/radius, and max players are documented here as current server behavior. A shared client/server constants source or asset-driven config remains SL-30 scope.

Room cleanup follows the SL-43 in-memory TTL rules:

- Waiting room idle TTL: 10 minutes
- Started room all-disconnected TTL: 5 minutes
- Hard room lifetime: 1 hour

## Build And Validation

Docs source files are validated and rendered at build time.

```sh
make docs-build
make ci
```

Generated embed files live under `internal/docs/api/` and `internal/docs/static/`, and are intentionally ignored by git.
