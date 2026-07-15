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

Waiting room에 player를 배정하고 WebSocket path를 돌려줍니다. 여유 waiting room이 없으면 새 room을 만듭니다. 같은 room에 두 번째 player가 들어오면 REST 응답 shape는 유지하되 room은 ready/start 전까지 `waiting`으로 남습니다.

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
  },
  "player": {
    "id": "player-1",
    "team": "red",
    "slot": 0
  },
  "webSocketPath": "/rooms/room-1/players/player-1"
}
```

시뮬레이션 시작 트리거:

- `/matchmaking/join`을 한 번만 호출하면 room은 `waiting`이고 gameplay snapshot은 아직 오지 않습니다.
- 같은 waiting room에 두 번째 player가 들어오면 room은 matchmaking match로 잠기고 late join 대상에서 빠집니다.
- 두 player가 WebSocket에 연결하면 `Type: "Ready"` event로 map과 player별 spawn 위치를 받습니다.
- 두 client가 `{"Type":"ready"}`를 보내면 countdown 시작 신호를 1번 받고, client는 fake timer를 표시합니다.
- Server는 5초를 내부에서 센 뒤 `Snapshot.status: "started"`를 보내고 30Hz snapshot을 시작합니다.
- start 전 WebSocket close는 match cancel로 room을 제거합니다.
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

`map`은 서버 simulation이 실제 collision에 쓰는 tile grid입니다. tile 값은 `0=ground`, `1=wall`, `2=spawnPoint`입니다. `map` row는 Base64 문자열이 아니라 JSON number array입니다.

기본 map source는 server binary가 embed한 `server-config/game-config.json`의 `map`입니다. 현재 기본 map은 spawn point tile을 포함한 20x20이며, 이 문서의 예시는 간결함을 위해 5x5 fallback map 기준입니다. config 로드나 검증에 실패하면 `internal/simulation.StaticGameConfig()`의 5x5 map으로 fallback합니다. `internal/simulation/fixtures/default-map.json`은 테스트용 fixture로만 남아 있습니다.

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
- `player_not_found` (WebSocket upgrade 전 검증에서 반환)
- `player_already_connected` (WebSocket upgrade 전 검증에서 반환)

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

서버는 유한한 `MoveDir`의 크기가 `1` 이하이면 그대로 보존하고, 더 크면 unit vector로 clamp합니다. Zero가 아닌 유한한 `AttackDir`는 항상 unit vector로 정규화하며, NaN/Inf가 포함된 input은 적용하지 않습니다. 각 player는 server-only 4 attack charge로 시작하고 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. `PressedAttack: true`여도 player가 사망했거나 방향이 zero이거나 charge가 소진됐으면 공격을 거부합니다.

Ready event:

```json
{
  "Type": "Ready",
  "Map": {
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
  "Players": [
    {
      "Id": "player-1",
      "Team": "red",
      "Slot": 0,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    }
  ]
}
```

Server snapshot:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "started",
    "Tick": 1,
    "Players": [],
    "Projectiles": []
  }
}
```

Snapshot의 `Players[].PressedAttack`은 input echo가 아니라 방향, 생존 상태, 남은 charge를 검증한 뒤 서버가 해당 tick의 공격을 승인했는지 나타내는 transient 결과입니다.

Starting signal:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "starting",
    "countdown": 5,
    "Tick": 0,
    "Players": null,
    "Projectiles": null
  }
}
```

Ready ACK:

```json
{
  "Type": "ready"
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

GameEnd event:

```json
{
  "Type": "GameEnd",
  "PlayerId": "player-1",
  "Result": "Win"
}
```

Field 이름은 Unity prototype과 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`, `Id`, `OwnerId`, `Pos`, `Dir`, `HP`, `IsDead`, `IsDestroyed`처럼 유지합니다.
단, match lifecycle field인 `Snapshot.status`와 `Snapshot.countdown`은 REST `room.status`와 맞춰 lowercase입니다. `starting`의 `countdown`은 client fake timer 기준값이며, server는 중간 countdown 값을 broadcast하지 않습니다.
HP가 0인 player가 생기면 server는 같은 tick의 snapshot을 먼저 보낸 뒤 player별 `GameEnd` event를 보냅니다. 한 명만 사망하면 생존 player는 `Win`, 사망 player는 `Lose`입니다. 같은 tick에 양쪽 player가 동시에 사망하면 양쪽 모두 `Draw`입니다. Server는 `GameEnd` 이후 room과 WebSocket connection을 정리합니다.

## 현재 gameplay 값

- tick rate: 30Hz
- tile size: 1.2
- player speed: 2
- player radius: 0.5
- player HP: 100
- attack charges: 4
- attack recharge: 30 tick마다 1 charge
- projectile speed: 13
- projectile damage: 10
- projectile radius: 0.3
- map/debug room max players: 6

Gameplay config artifact는 client 공유용과 server runtime용을 분리합니다.

- `client-config/game-config.json`: client build가 sparse checkout해서 가져가는 공유 config입니다. `tileSize`, `playerRadius`, `playerTypes`, `projectileRadius`, `projectileTypes`만 포함합니다.
- `server-config/game-config.json`: server binary가 embed해서 room store와 simulation 기본값으로 쓰는 server-only config입니다. `tickRate`, `tile.size`, player type별 `maxAttackCharges`/`attackRechargeTicks`, player/projectile type별 runtime 값, `map`을 포함합니다.

Client는 gameplay state를 여전히 서버 snapshot에서 받습니다. `HP`, speed, damage, tick rate, map과 attack charge 진행도는 server-only config/state나 Ready/snapshot message의 책임입니다.

## 수동 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 응답의 `webSocketPath`로 WebSocket을 엽니다.
3. 두 연결이 같은 `Type: "Ready"` event를 받아야 합니다.
4. Ready event의 `Map.map` row는 숫자 배열이어야 하고, `Players[].SpawnPosition`이 있어야 합니다.
5. 두 client가 `{"Type":"ready"}`를 보내면 `starting` 신호를 1번 받고, 중간 countdown broadcast 없이 5초 뒤 `started`를 받아야 합니다.
6. 한 client가 movement input을 보내면 두 연결이 같은 gameplay snapshot을 받아야 합니다.
7. 공격이 target에 닿으면 두 연결에서 projectile `IsDestroyed: true`, target `HP` 감소가 보여야 합니다.
8. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
9. HP가 0이 된 tick의 snapshot 이후 player별 `GameEnd`를 받아야 합니다.
10. `GameEnd` 이후 해당 room은 정리되어야 합니다.
11. 잘못된 JSON은 `invalid_input` error를 보내고 snapshot stream은 계속되어야 합니다.

자동 회귀는 `go test ./internal/rooms`가 담당합니다.

## 제약

이 API는 development surface입니다. Auth, rate limit, production matchmaking, persistence, respawn, score, dashboard, scheduler, Kubernetes는 없습니다.
