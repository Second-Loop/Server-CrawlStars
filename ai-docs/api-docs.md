# API Documentation Policy

## Scope

이 문서는 REST API와 WebSocket message contract를 문서화하는 E1 결정을 기록합니다.

E1은 client가 server-authoritative input과 snapshot 흐름을 확인할 수 있는 작은 development contract surface를 노출합니다. 이는 최종 production API contract가 아닙니다.

## Decision

- REST API는 OpenAPI 3.x를 사용합니다.
- Interactive page는 server binary에 embed되는 repository-owned static UI로 render합니다.
- WebSocket message contract는 AsyncAPI를 사용합니다.
- OpenAPI는 `ws://` 또는 `wss://` server URL을 언급할 수 있지만, bidirectional WebSocket message stream의 primary source of truth로 취급하지 않습니다.
- Development/debug API는 formal client contract로 승격되기 전까지 unstable 및 E1-only로 명시합니다.

## REST Documentation

다음처럼 request/response가 명확한 API에는 OpenAPI를 사용합니다.

- `GET /health`
- `POST /matchmaking/join`
- `GET /rooms`
- `POST /rooms`
- `GET /rooms/{roomID}`
- `POST /rooms/{roomID}/players`
- `POST /rooms/{roomID}/start`

REST는 각 operation이 bounded request와 response를 가지므로 OpenAPI가 적합합니다. Server-hosted docs UI는 endpoint를 읽기 쉽게 만들고 raw spec으로 바로 이동할 수 있게 합니다.

## SL-49 Simple Matchmaking REST API

SL-49에서 구현한 matchmaking endpoint는 Unity client가 room/player를 직접 조립하지 않고 WebSocket 접속 정보를 받을 수 있는 단순 connector입니다. Production matchmaking queue, rating algorithm, auth, persistence는 포함하지 않습니다.

```text
POST /matchmaking/join
```

Response:

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

Matching은 waiting room 중 여유가 있는 room을 사용하고, 없으면 새 room을 만듭니다. 두 번째 player가 들어와 room player count가 2가 되면 room simulation을 자동 start합니다. Matchmaking path는 이미 `started`인 room에 late join하지 않습니다. 현재 cap은 `simulation.StaticMapFixture().MaxPlayers = 6`이며 10명 확장은 후속 issue 범위입니다.

## E1 Room Debug REST API

SL-41에서 구현한 room REST endpoint는 E1 debug API입니다. 실제 Unity gameplay client가 장기 의존할 정식 gameplay contract가 아니며, OpenAPI 승격 전까지 response shape는 `e1-debug` 안정성으로 취급합니다.

현재 JSON response shape는 다음 최소 필드를 사용합니다.

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

`GET /rooms`는 room object 배열을 `rooms` field로 감싸서 반환합니다.

```json
{
  "rooms": []
}
```

REST error response는 다음 형태로 통일합니다.

```json
{
  "error": {
    "code": "room_not_found",
    "message": "room not found"
  }
}
```

현재 error code는 다음을 사용합니다.

- `room_not_found`
- `room_cap_reached`
- `room_full`
- `room_has_no_players`
- `method_not_allowed`
- `not_found`

SL-43부터 E1 room store는 public debug API에 room이 무한히 쌓이지 않도록 다음 cleanup rule을 적용합니다.

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 마지막 WebSocket client disconnect 후 5분
- hard room lifetime: room 생성 후 1시간
- connected client가 있으면 waiting idle TTL과 all-disconnected TTL로 즉시 삭제하지 않습니다.

## WebSocket Documentation

다음처럼 지속적인 message flow에는 AsyncAPI를 사용합니다.

- client가 room/player WebSocket endpoint에 connect
- client가 input message 전송
- server가 snapshot broadcast
- server가 structured error message 전송

AsyncAPI는 channel, message, payload schema, bidirectional event stream을 설명할 수 있으므로 WebSocket에 적합합니다.

## E1 WebSocket Debug API

SL-42에서 구현한 WebSocket endpoint는 E1 debug API입니다.

```text
WS /rooms/{roomID}/players/{playerID}
```

이 endpoint는 REST room lifecycle로 생성한 room/player를 사용합니다. Unknown room/player와 duplicate same player connection은 upgrade 전에 JSON error response로 거부합니다. Room이 아직 `started`가 아니면 connection과 input 수신은 허용하지만 snapshot broadcast는 하지 않습니다.

Client input payload는 client `PlayerData` 이름과 맞춘 `MoveDir`, `AttackDir`, `PressedAttack` field를 사용합니다. Unity `Vector2` 값은 `x`, `y` field로 전달합니다. Invalid JSON input은 connection을 끊지 않고 error message를 보낸 뒤 해당 input만 무시합니다.

Server는 started room에서 30Hz tick마다 다음 wrapper 형태의 snapshot message를 broadcast합니다.

```json
{
  "Type": "snapshot",
  "Snapshot": {}
}
```

Invalid input error message는 snapshot wrapper와 같은 top-level naming convention을 따릅니다.

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

Snapshot 내부의 `PlayerData`와 `ProjectileData` field는 client code와 맞춰 `Id`, `Pos`, `MoveDir`, `AttackDir`, `PressedAttack`, `IsDead`, `OwnerId`, `Dir`, `IsDestroyed` 이름을 사용합니다. REST room debug API의 `id`, `status`, `players`, `latestSnapshot` field는 별도 debug surface로 유지합니다.

이 schema는 AsyncAPI spec으로 승격되기 전까지 `e1-debug` 안정성입니다.

## OpenAPI WebSocket Boundary

OpenAPI는 `ws://` 또는 `wss://` server URL을 설명할 수 있고, WebSocket connection 전에 room을 만들거나 player ID를 발급하는 HTTP endpoint를 문서화할 수 있습니다.

하지만 Swagger UI가 지속적인 bidirectional gameplay stream을 안정적으로 실행하는 데 적합하지 않으므로, OpenAPI를 E1의 전체 WebSocket contract로 취급하지 않습니다.

## Document Locations

Source spec은 repository에 둡니다.

```text
api/openapi.yaml
api/asyncapi.yaml
```

Running server는 SL-47부터 다음 path를 노출합니다.

```text
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
```

`/openapi`와 `/asyncapi`는 human-readable static UI입니다. `/openapi.yaml`과 `/asyncapi.yaml`은 raw spec입니다.

Docs UI는 `docs-ui`에서 build하고, build 결과는 `internal/docs/static/`과 `internal/docs/api/`에 생성한 뒤 Go `embed`로 server binary에 포함합니다. 생성물은 commit하지 않습니다. 따라서 clean checkout에서 Go compile/test/build를 실행하기 전에는 `make docs-build` 또는 `make ci`가 먼저 필요합니다.

GitHub Pages는 나중에 static mirror가 필요할 때 검토합니다. E1의 primary hosting path는 running server입니다.

## Development And Debug API Marking

E1-only API는 docs에 다음을 표시해야 합니다.

- `x-stability: e1-debug`
- summary 또는 description에 `E1 debug API` 포함
- Unity client code가 해당 endpoint에 의존해도 되는지 설명하는 note

Debug API는 암묵적으로 stable client contract로 승격하지 않습니다. 승격에는 follow-up Linear issue와 docs update가 필요합니다.

## Validation

처음으로 spec file을 추가하는 implementation issue는 해당 spec validation도 함께 추가해야 합니다. 최소 기준:

- OpenAPI spec이 정상 parse됩니다.
- AsyncAPI spec이 정상 parse됩니다.
- Documentation path가 `ai-docs/protocol.md`에 언급되어 있습니다.
- `make ci`가 docs install, spec validation, docs build, Go validation을 함께 실행합니다.
