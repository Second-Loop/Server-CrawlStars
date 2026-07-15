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

`GET /health`, docs, `POST /matchmaking/join`은 public surface입니다. 일곱 Room debug operation은 기본 비활성화되어 `404 not_found`를 반환합니다. 활성화한 환경에서는 모두 다음 header가 필요합니다.

```text
Authorization: Bearer <DEBUG_API_TOKEN>
```

Debug guard 우선순위는 `disabled 404 not_found` → `enabled + missing/wrong/multiple credential 401 unauthorized` → `authenticated route result`입니다. 올바른 credential 뒤에야 2xx, `room_not_found` 404, 405, 409, 500을 평가합니다. WebSocket GET은 이 Bearer guard 대상이 아니고 player session query token으로 인증합니다.

### `POST /matchmaking/join`

요청한 game mode의 waiting room에 player를 배정하고 player session credential이 포함된 WebSocket path를 돌려줍니다. 같은 mode의 여유 waiting room이 없으면 새 room을 만들며, 다른 mode의 pool은 재사용하지 않습니다.

Request body는 optional입니다. 다음 세 요청은 모두 기본 `duel_1v1`을 선택해 기존 client와 호환됩니다.

- body 없음
- `{}`
- `{"gameMode":""}`

새 client는 `duel_1v1`, `solo`, `team` 중 하나를 보내거나 field를 생략합니다.

```http
POST /matchmaking/join
Content-Type: application/json

{"gameMode":"solo"}
```

지원하지 않는 non-empty 값은 400 `invalid_game_mode`, malformed JSON이나 두 개 이상의 JSON value는 400 `invalid_request`입니다.

```json
{
  "gameMode": "solo",
  "room": {
    "id": "room_AbCdEfGhIjKlMnOpQrStUv",
    "gameMode": "solo",
    "status": "waiting",
    "players": [
      {
        "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
        "team": "solo-1",
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
    "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
    "team": "solo-1",
    "slot": 0
  },
  "sessionToken": "<player-session-token>",
  "webSocketPath": "/rooms/room_AbCdEfGhIjKlMnOpQrStUv/players/player_VuTsRqPoNmLkJiHgFeDcBa?token=<player-session-token>"
}
```

Top-level `gameMode`와 nested `room.gameMode`는 항상 같고, room이 생성될 때 선택된 값으로 lifecycle 동안 고정됩니다.

```json
{"error":{"code":"invalid_game_mode","message":"invalid game mode"}}
```

```json
{"error":{"code":"invalid_request","message":"invalid request"}}
```

Room/player ID는 16 random bytes를 Raw URL Base64로 인코딩한 22자 payload에 `room_`/`player_` prefix를 붙입니다. Session token은 32 random bytes를 같은 방식으로 인코딩한 43자 opaque value입니다. Raw token은 발급 JSON의 `sessionToken`과 tokenized `webSocketPath` 두 곳에 같은 secret으로 나타나고, 이후 inbound WebSocket query로 다시 전달됩니다. 서버 private state에는 SHA-256 digest만 저장합니다.

`sessionToken`, tokenized `webSocketPath`, inbound query는 모두 secret-bearing surface입니다. Raw 값이나 전체 query 문자열을 log, telemetry, 문서에 남기지 않습니다. Public Room/Player/list/detail/Ready/Snapshot/GameEnd payload에는 raw token이나 digest가 없습니다.

Join 요청은 `client IP resolve → quota 평가/소비 → body decode와 mode 검증 → store join` 순서입니다. 기본 limiter는 process-local per-IP token bucket이며 10 requests/minute, burst 4입니다. Bucket이 비면 body와 store 상태보다 먼저 429를 반환하므로 malformed/unknown mode 400, room cap 409, `internal_error` 500보다 우선합니다. Store에서 409/500으로 끝난 허용 요청도 quota 1개를 이미 소비합니다. POST가 아닌 method의 405는 quota를 소비하지 않습니다.

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 6

{"error":{"code":"rate_limited","message":"rate limit exceeded"}}
```

`Retry-After`는 필요한 대기 시간을 올림한 최소 1초의 delta-seconds 정수입니다.

Client IP는 immediate peer를 기본값으로 씁니다. Peer가 `TRUSTED_PROXY_CIDRS`에 속하고 `CF-Connecting-IP`가 정확히 하나의 valid IP일 때만 그 값을 신뢰합니다. Header가 없거나 malformed/multiple이면 요청을 거부하지 않고 peer IP bucket으로 fallback합니다. `X-Forwarded-For`는 항상 무시합니다. Cloudflare Tunnel loopback peer를 trust하지 않으면 public client가 하나의 loopback bucket을 공유할 수 있으므로 배포 설정은 `ai-docs/deployment.md`를 따릅니다.

시뮬레이션 시작 트리거:

- `/matchmaking/join`을 한 번만 호출하면 room은 `waiting`이고 gameplay snapshot은 아직 오지 않습니다.
- 선택 mode의 required player 수가 차면 room은 matchmaking match로 잠기고 late join 대상에서 빠집니다.
- 모든 matched player가 WebSocket에 연결하면 `Type: "Ready"` event로 map과 player별 spawn 위치를 받습니다.
- 모든 client가 `{"Type":"ready"}`를 보내면 countdown 시작 신호를 1번 받고, client는 fake timer를 표시합니다.
- Server는 5초를 내부에서 센 뒤 `Snapshot.status: "started"`를 보내고 30Hz snapshot을 시작합니다.
- start 전 WebSocket close는 match cancel로 room을 제거합니다.
- 1명으로 디버그할 때는 `POST /rooms/{roomID}/start`를 호출하면 됩니다.

Mode별 match 정원과 team/slot은 다음과 같습니다.

| gameMode | 정원 | assignment |
| --- | ---: | --- |
| `duel_1v1` | 2 | `red/0`, `blue/0` |
| `solo` | 6 | `solo-1/0`부터 `solo-6/0` |
| `team` | 6 | `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2` |

### Room debug API

Room response:

```json
{
  "id": "room_AbCdEfGhIjKlMnOpQrStUv",
  "gameMode": "duel_1v1",
  "status": "waiting",
  "players": [
    {
      "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
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

`map`은 서버 simulation이 실제 collision에 쓰는 tile grid입니다. tile 값은 `0=ground`, `1=wall`, `2=spawnPoint`, `3=bush`, `4=water`입니다. `map` row는 Base64 문자열이 아니라 JSON number array입니다.

| Tile | 값 | Player | Projectile |
| --- | ---: | --- | --- |
| Ground | 0 | 통과 | 통과 |
| Wall | 1 | 충돌 | 충돌 |
| SpawnPoint | 2 | 통과 | 통과 |
| Bush | 3 | 통과 | 통과 |
| Water | 4 | 충돌 | 통과 |
| Map boundary | - | 충돌 | 충돌 |

기본 map source는 server binary가 embed한 `server-config/game-config.json`의 `map`입니다. 현재 기본 map은 client SL-79에서 merge된 `Map_0`과 값이 같은 20x20 grid이며 exact-grid Go regression으로 drift를 막습니다. 이 문서의 예시는 간결함을 위해 5x5 fallback map 기준입니다. config 로드나 검증에 실패하면 `internal/simulation.StaticGameConfig()`의 5x5 map으로 fallback합니다. `internal/simulation/fixtures/default-map.json`은 테스트용 fixture로만 남아 있습니다.

`latestSnapshot`은 마지막으로 생성된 snapshot의 요약입니다. 아직 room이 started 전이거나 첫 tick 전이면 `tick: 0`입니다.

`POST /rooms/{roomID}/players`의 인증된 debug 응답도 matchmaking과 같은 player session을 발급합니다.

```json
{
  "player": {
    "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
    "team": "red",
    "slot": 0
  },
  "sessionToken": "<player-session-token>",
  "webSocketPath": "/rooms/room_AbCdEfGhIjKlMnOpQrStUv/players/player_VuTsRqPoNmLkJiHgFeDcBa?token=<player-session-token>"
}
```

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
- `invalid_game_mode`
- `invalid_request`
- `unauthorized`
- `rate_limited`
- `internal_error`
- `player_not_found` (WebSocket upgrade 전 검증에서 반환)
- `player_already_connected` (WebSocket upgrade 전 검증에서 반환)

### 409 room cap 회복

Active room cap은 5개입니다. 테스트 중 `room_cap_reached`가 나오면 debug API를 명시적으로 활성화하고 올바른 Bearer credential로 room을 비울 수 있습니다.

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

AsyncAPI channel address 자체는 query를 제외한 path-only 값입니다.

```text
WS /rooms/{roomID}/players/{playerID}
```

실제 연결은 path에 정확히 한 개의 non-empty token query를 붙입니다.

```text
WS /rooms/{roomID}/players/{playerID}?token=<player-session-token>
```

연결 전에 room/player/session이 REST로 발급되어 있어야 합니다. 정상적인 다른 query key는 허용하지만 어느 query pair든 malformed하면 전체 query를 fail-closed 401로 처리합니다. 검증 순서는 room 404 → player 404 → token 401 → live connection 또는 in-flight reservation 409입니다. Wrong token은 reservation 충돌보다 먼저 401입니다.

Token은 일회용 credential이 아니며 room/player session이 존재하는 동안 재사용할 수 있습니다. 다만 matchmaking의 matched/loading/starting 단계에서 실제 연결이 끊기면 pre-start cancel로 room이 삭제되어 reconnect할 수 없습니다. Started room도 all-disconnected 5분 TTL과 hard 1시간 lifetime 안에서만 남습니다. HTTP-to-WebSocket upgrade 자체가 실패하면 reservation만 rollback하고 room을 취소하지 않으므로 같은 발급 path로 재시도할 수 있습니다.

Server는 연결마다 snapshot fanout과 독립적인 heartbeat를 30초마다 실행하고, 각 Ping에 90초 deadline을 둡니다. Ping error/timeout은 read/write failure와 같은 idempotent close 경로로 현재 session만 한 번 해제합니다. Pre-start match에서는 기존 cancel 정책을 적용하고, started room의 마지막 client가 사라지면 5분 disconnected TTL을 시작합니다. Bot replacement나 별도 reconnect grace는 없습니다.

일반 gameplay snapshot은 client별 크기 1 latest-only slot에서 합쳐 느린 client가 room tick이나 다른 client를 막지 않게 합니다. `Ready`, `starting`, `started`, `error`는 reliable control queue에서 순서를 보존합니다. 종료 시에는 남은 일반 snapshot을 버리고 `terminal snapshot -> GameEnd -> close` 순서를 socket close 전에 보장합니다. 각 payload write는 새 5초 context를 사용합니다.

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
      "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
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
  "PlayerId": "player_VuTsRqPoNmLkJiHgFeDcBa",
  "Result": "Win"
}
```

Field 이름은 Unity prototype과 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`, `Id`, `OwnerId`, `Pos`, `Dir`, `HP`, `IsDead`, `IsDestroyed`처럼 유지합니다.
단, match lifecycle field인 `Snapshot.status`와 `Snapshot.countdown`은 REST `room.status`와 맞춰 lowercase입니다. `starting`의 `countdown`은 client fake timer 기준값이며, server는 중간 countdown 값을 broadcast하지 않습니다.
HP가 0인 player가 생기면 server는 같은 tick의 snapshot을 먼저 보낸 뒤 player별 `GameEnd` event를 보냅니다. 일부 player만 사망하면 생존 player는 `Win`, 사망 player는 `Lose`이고 같은 tick에 모든 player가 사망하면 모두 `Draw`입니다. Solo/team도 현재 같은 player-survival fallback을 사용하며 mode별 elimination rule은 아직 없습니다. Server는 `GameEnd` 이후 room과 WebSocket connection을 정리합니다. Room TTL은 Store당 하나의 30초 janitor가 검사하며, create/matchmaking이 active room cap에 닿았을 때만 즉시 cleanup을 한 번 수행하고 생성도 한 번만 재시도합니다.

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
- `server-config/game-config.json`: server binary가 embed해서 room store와 simulation 기본값으로 쓰는 server-only config입니다. `tickRate`, `tile.size`, player type별 `maxAttackCharges`/`attackRechargeTicks`, player/projectile type별 runtime 값, `mode.default`와 `mode.catalog`, `map`을 포함합니다.

Client는 gameplay state를 여전히 서버 snapshot에서 받습니다. `HP`, speed, damage, tick rate, map과 attack charge 진행도는 server-only config/state나 Ready/snapshot message의 책임입니다.

## 기본 duel 2인 수동 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 응답의 secret-bearing `webSocketPath`를 client 내부에서만 사용해 WebSocket을 엽니다. Raw path/query를 log에 남기지 않습니다.
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

이 API는 development surface입니다. Player session 인증, debug Bearer guard, matchmaking rate limit, WebSocket heartbeat는 구현되어 있습니다. Account auth, production matchmaking, persistence, bot replacement, reconnect grace, respawn, score, dashboard, scheduler, Kubernetes는 없습니다.
