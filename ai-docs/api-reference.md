# API Reference

## Scope

이 문서는 SL-47 기준 E1 debug API를 사람이 읽기 쉬운 형태로 요약합니다. Raw machine-readable source of truth는 다음 파일입니다.

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

REST endpoint는 E1 room lifecycle을 수동 검증하기 위한 debug surface입니다.

```text
GET /health
GET /rooms
POST /rooms
GET /rooms/{roomID}
POST /rooms/{roomID}/players
POST /rooms/{roomID}/start
```

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

Client-facing WebSocket field names intentionally follow the Unity prototype vocabulary: `MoveDir`, `AttackDir`, `PressedAttack`, `Type`, `Snapshot`, `Error`, `Id`, `OwnerId`, `Pos`, `Dir`, `IsDestroyed`.

## E1 Constraints

These APIs are `e1-debug` surfaces. They do not implement production authentication, rate limiting, matchmaking queue, persistence, gameplay scoring, hit detection, death, respawn, admin dashboard, scheduler, or Kubernetes deployment.

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
