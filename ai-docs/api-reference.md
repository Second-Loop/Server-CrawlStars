# API 요약

Machine-readable 기준은 `api/openapi.yaml`, `api/asyncapi.yaml`입니다. 이 문서는 사람이 빠르게 읽기 위한 요약입니다.

## Docs

```text
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
```

`/openapi`와 `/asyncapi`는 UI이고, `*.yaml`은 raw spec입니다.

## REST

```text
GET /health
POST /matchmaking/join
GET /rooms
POST /rooms
DELETE /rooms
GET /rooms/{roomID}
DELETE /rooms/{roomID}
POST /rooms/{roomID}/players
POST /rooms/{roomID}/start
```

### `POST /matchmaking/join`

Waiting room에 player를 배정하고 WebSocket path를 돌려줍니다. 여유 waiting room이 없으면 새 room을 만듭니다. 같은 room에 두 번째 player가 들어오면 simulation을 바로 start합니다.

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

현재는 match-state WebSocket event, client ready ACK, countdown, start 전 cancel을 지원하지 않습니다. 이 흐름은 `SL-58`에서 다룹니다.

시뮬레이션 시작 트리거:

- `/matchmaking/join`을 한 번만 호출하면 room은 `waiting`이고 gameplay snapshot은 아직 오지 않습니다.
- 같은 waiting room에 두 번째 player가 들어오면 서버가 자동으로 `started`로 바꾸고 30Hz snapshot을 보냅니다.
- 1명으로 디버그할 때는 `POST /rooms/{roomID}/start`를 호출하면 됩니다.

### Room debug API

Room response:

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
    "map": {
      "width": 5,
      "height": 5,
      "index": 0,
      "maxPlayers": 6,
      "tileSize": 1.2,
      "map": [
        [1, 1, 1, 1, 1],
        [1, 0, 0, 0, 1],
        [1, 0, 1, 0, 1],
        [1, 0, 0, 0, 1],
        [1, 1, 1, 1, 1]
      ]
    },
    "latestSnapshot": {
      "tick": 0,
      "playerCount": 1,
    "projectileCount": 0
  }
}
```

`map`은 서버 simulation이 실제 collision에 쓰는 tile grid입니다. tile 값은 `0=ground`, `1=wall`, `2=spawnPoint`입니다.

기본 맵 파일은 `internal/simulation/fixtures/default-map.json`입니다. 서버는 시작할 때 이 JSON fixture를 로드해 room store에 주입하고, 파일 로드나 검증에 실패하면 `internal/simulation.StaticMapFixture()`의 5x5 map으로 fallback합니다. 실제 client map file과 공통 artifact로 맞추는 작업은 `SL-30` 범위입니다.

`latestSnapshot`은 마지막으로 생성된 snapshot의 요약입니다. 아직 room이 started 전이거나 첫 tick 전이면 `tick: 0`입니다.

Error response:

```json
{
  "error": {
    "code": "room_not_found",
    "message": "room not found"
  }
}
```

현재 error code:

- `room_not_found`
- `room_cap_reached`
- `room_full`
- `room_has_no_players`
- `method_not_allowed`
- `not_found`

### 409 room cap 회복

Active room cap은 5개입니다. 테스트 중 `room_cap_reached`가 나오면 debug API로 room을 비울 수 있습니다.

```text
DELETE /rooms
DELETE /rooms/{roomID}
```

응답:

```json
{
  "deleted": 5
}
```

삭제 시 해당 room의 ticker와 WebSocket connection도 함께 닫습니다. 서버가 in-memory room만 사용하므로 persistence 삭제는 없습니다.

## WebSocket

```text
WS /rooms/{roomID}/players/{playerID}
```

연결 전에 room과 player가 REST로 발급되어 있어야 합니다. 같은 room/player의 중복 연결은 거부합니다.

Client input:

```json
{
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false
}
```

Server snapshot:

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

Invalid input:

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

Field 이름은 Unity prototype과 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`, `Id`, `OwnerId`, `Pos`, `Dir`, `HP`, `IsDead`, `IsDestroyed`처럼 유지합니다.

## 현재 gameplay 값

- tick rate: 30Hz
- tile size: 1.2
- player speed: 2
- player radius: 0.5
- player HP: 100
- projectile speed: 13
- projectile damage: 10
- projectile radius: 0.3
- fixture max players: 6

공통 client/server constants artifact는 아직 없고 `SL-30`에서 다룹니다.

## 수동 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 응답의 `webSocketPath`로 WebSocket을 엽니다.
3. 한 client가 movement input을 보내면 두 연결이 같은 snapshot을 받아야 합니다.
4. 공격이 target에 닿으면 두 연결에서 projectile `IsDestroyed: true`, target `HP` 감소가 보여야 합니다.
5. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
6. 잘못된 JSON은 `invalid_input` error를 보내고 snapshot stream은 계속되어야 합니다.

자동 회귀는 `go test ./internal/rooms`가 담당합니다.

## 제약

이 API는 development surface입니다. Auth, rate limit, production matchmaking, persistence, respawn, score, win/loss, dashboard, scheduler, Kubernetes는 없습니다.
