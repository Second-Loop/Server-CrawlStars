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

새 client는 `duel_1v1`, `solo`, `team` 중 하나를 보내거나 field를 생략하고, stable `characterType` `0=Shelly`, `1=Colt`, `2=Lily`를 명시합니다. SL-82에서 legacy characterType 생략만 Shelly `0`으로 보정하고 structured warning을 한 번 기록합니다. explicit `null`, non-integer, string/bool/object/array, 지원하지 않는 integer는 400 `invalid_character_type`이며 SL-98에서 required로 전환합니다.

```http
POST /matchmaking/join
Content-Type: application/json

{"gameMode":"solo","characterType":1}
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
        "slot": 0,
        "isBot": false,
        "characterType": 1
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
    "slot": 0,
    "isBot": false,
    "characterType": 1
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

```json
{"error":{"code":"invalid_character_type","message":"invalid character type"}}
```

Room/player ID는 16 random bytes를 Raw URL Base64로 인코딩한 22자 payload에 `room_`/`player_` prefix를 붙입니다. Session token은 32 random bytes를 같은 방식으로 인코딩한 43자 opaque value입니다. Raw token은 발급 JSON의 `sessionToken`과 tokenized `webSocketPath` 두 곳에 같은 secret으로 나타나고, 이후 inbound WebSocket query로 다시 전달됩니다. 서버 private state에는 SHA-256 digest만 저장합니다.

`sessionToken`, tokenized `webSocketPath`, inbound query는 모두 secret-bearing surface입니다. Raw 값이나 전체 query 문자열을 log, telemetry, 문서에 남기지 않습니다. Public Room/Player/list/detail/Ready/Snapshot/GameEnd payload에는 raw token이나 digest가 없습니다.

Player identity의 bot 표시는 transport에 따라 casing이 다릅니다.

| Surface | Field | 의미 |
| --- | --- | --- |
| REST `Room.players[]` | `isBot` | Generic `Player`이므로 human은 `false`, server-owned bot은 `true`입니다. |
| REST `MatchmakingJoin.player`, `PlayerSessionResponse.player` | `isBot` | Credential wrapper인 `HumanPlayer`라 항상 `false`입니다. |
| WebSocket Ready `Players[]` | `IsBot` | Full participant list에서 human/bot을 구분합니다. |
| WebSocket Snapshot `Players[]` | `IsBot` | 같은 participant identity를 gameplay snapshot까지 유지합니다. |

두 casing 모두 required boolean이며 human의 `false`도 생략하지 않습니다.

CharacterType은 REST `Player.characterType`의 required lower camel과 Ready/Snapshot `CharacterType`의 required PascalCase로 같은 canonical identity를 전달합니다. Bot/debug participant는 Shelly `0`입니다.

Bot은 room participant지만 player session이 없으므로 `sessionToken`이나 `webSocketPath`를 발급받지 않습니다. Bot을 만드는 public REST endpoint도 없습니다. `Room.players[]`에는 bot이 포함될 수 있지만 credential-bearing wrapper의 `player`는 `HumanPlayer`만 반환합니다.

Join 요청은 `client IP resolve → quota 평가/소비 → body decode와 mode 검증 → store join` 순서입니다. 기본 limiter는 process-local per-IP token bucket이며 10 requests/minute, burst 4입니다. Join error priority는 조건부입니다: `429 rate_limited`가 항상 먼저이고, JSON framing/body shape 오류는 Store 진입 전 400 `invalid_request`입니다. 문법적으로 유효한 request는 closed Store면 semantic mode/character 해석보다 먼저 500 `internal_error`를 반환합니다. Store가 열린 경우에만 semantic 순서는 400 `invalid_game_mode` 다음 400 `invalid_character_type`입니다. Store에서 409/500으로 끝난 허용 요청도 quota 1개를 이미 소비합니다. POST가 아닌 method의 405는 quota를 소비하지 않습니다.

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 6

{"error":{"code":"rate_limited","message":"rate limit exceeded"}}
```

`Retry-After`는 필요한 대기 시간을 올림한 최소 1초의 delta-seconds 정수입니다.

같은 IP에서 Solo/Team 6-client smoke를 한꺼번에 실행하면 기본 burst 4 때문에 첫 네 요청 뒤 429가 날 수 있습니다. 실제 client는 `Retry-After` 뒤 재시도해야 하며, 격리된 local smoke에서만 필요하면 `MATCHMAKING_JOIN_BURST=6`을 명시합니다.

Client IP는 immediate peer를 기본값으로 씁니다. Peer가 `TRUSTED_PROXY_CIDRS`에 속하고 `CF-Connecting-IP`가 정확히 하나의 valid IP일 때만 그 값을 신뢰합니다. Header가 없거나 malformed/multiple이면 요청을 거부하지 않고 peer IP bucket으로 fallback합니다. `X-Forwarded-For`는 항상 무시합니다. Cloudflare Tunnel loopback peer를 trust하지 않으면 public client가 하나의 loopback bucket을 공유할 수 있으므로 배포 설정은 `ai-docs/deployment.md`를 따릅니다.

시뮬레이션 시작 gate:

| gameMode | Participant 정원 | WebSocket/Ready ACK | team/slot |
| --- | ---: | --- | --- |
| `duel_1v1` | 2 | Room 내 human participant 전원의 WebSocket 연결과 ACK | `red/0`, `blue/0` |
| `solo` | 6 | Room 내 human participant 전원의 WebSocket 연결과 ACK | `solo-1/0`부터 `solo-6/0` |
| `team` | 6 | Room 내 human participant 전원의 WebSocket 연결과 ACK | `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2` |

- Human과 bot을 합친 participant가 mode 정원을 채워야 full participant gate를 통과합니다. 그전까지 `room.status`는 `waiting`입니다.
- Human participant가 0명이면 attach/ACK quorum은 성립하지 않습니다.
- 정원을 채운 뒤 human participant의 WebSocket session이 모두 연결되면 human connection만 같은 `Type: "Ready"` event를 받습니다. Payload의 `Players`는 bot을 포함한 full participant list입니다.
- Ready의 `Players[].Team`, `Slot`, `IsBot`, `SpawnPosition`은 room이 선택한 mode config의 assignment 결과입니다.
- Ready ACK quorum도 human participant만 셉니다. Human 한 명이라도 ACK하지 않으면 countdown을 시작하지 않고, 연결된 human 전원이 ACK하면 `starting/countdown: 5`를 한 번 보냅니다.
- 같은 player의 중복 ACK는 quorum을 늘리지 않고 countdown이나 gameplay ticker를 다시 만들지 않습니다.
- Server는 5초를 내부에서 센 뒤 `started`를 한 번 보내고 room-local 30Hz snapshot을 시작합니다.
- 첫 human matchmaking join의 `0 -> 1` 전이에서만 room-owned 10초 deadline을 시작합니다. 후속 join과 partial manual bot 추가는 reset하지 않습니다.
- deadline을 먼저 획득하면 selected mode의 남은 slot을 bot으로 원자적으로 채웁니다. Timer-first late join은 다른 waiting room을 찾거나 만들고 active-room cap이면 `room_cap_reached` 409를 반환합니다.
- Bot ID 발급이 하나라도 실패하면 모든 예약 ID를 rollback하고 partial participant를 남기지 않으며 `bot_fill_failed`를 한 번 기록하고 retry하지 않습니다.
- Ready timeout, reconnect grace, participant replacement는 없습니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 pre-start cancel로 room을 삭제합니다.
- 1명으로 디버그할 때는 인증된 debug API `POST /rooms/{roomID}/start`를 호출합니다. 이 operation은 기본 비활성화되어 있으며 활성화 후 Bearer credential이 필요합니다.

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
      "slot": 0,
      "isBot": false,
      "characterType": 1
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

Map config는 `map.maxPlayers`명 모두에게 서로 다른 spawn을 줄 수 있어야 합니다. 명시적 SpawnPoint와 Wall/Water를 제외한 fallback 좌표의 합집합이 `map.maxPlayers`보다 작으면 config를 거부합니다.

`latestSnapshot`은 마지막으로 생성된 snapshot의 요약입니다. 아직 room이 started 전이거나 첫 tick 전이면 `tick: 0`입니다.

`POST /rooms/{roomID}/players`의 인증된 debug 응답도 matchmaking과 같은 player session을 발급합니다. 다만 matchmaking room이 selected mode 정원을 채워 matched/loading/starting/started lifecycle로 잠긴 뒤에는 map 정원이 남아 있어도 409 `room_full`을 반환합니다.

```json
{
  "player": {
    "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
    "team": "red",
    "slot": 0,
    "isBot": false,
    "characterType": 0
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
- `invalid_character_type`
- `unauthorized`
- `rate_limited`
- `internal_error`
- `player_not_found` (WebSocket upgrade 전 검증에서 반환)
- `player_already_connected` (WebSocket upgrade 전 검증에서 반환)

### 409 room cap 회복

Active room cap은 5개입니다. 테스트 중 `room_cap_reached`가 나오면 debug API를 명시적으로 활성화하고 올바른 Bearer credential로 일반 room을 비울 수 있습니다. GameEnd close barrier에 들어간 ending room은 hard TTL과 debug clear/delete가 제거하지 않습니다.

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

삭제 시 해당 일반 room의 ticker와 WebSocket connection도 함께 닫습니다. Ending room은 normal GameEnd cleanup 또는 forced Shutdown이 소유합니다. 서버가 in-memory room만 사용하므로 persistence 삭제는 없습니다.

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

Token은 일회용 credential이 아니며 room/player session이 존재하는 동안 재사용할 수 있습니다. 다만 matchmaking의 matched/loading/starting 단계에서 실제 연결이 끊기면 pre-start cancel로 room이 삭제되어 reconnect할 수 없습니다. Started room도 all-disconnected 5분 TTL과 hard 1시간 lifetime 안에서만 남습니다. 같은 match의 started room에 reconnect하면 authoritative simulation state에 남은 `LastProcessedClientTick`부터 이어서 다음 양수 tick을 보내고, 새 match의 ACK는 `0`에서 시작합니다. HTTP-to-WebSocket upgrade 자체가 실패하면 reservation만 rollback하고 room을 취소하지 않으므로 같은 발급 path로 재시도할 수 있습니다.

Server는 연결마다 snapshot fanout과 독립적인 heartbeat를 30초마다 실행하고, 각 Ping에 90초 deadline을 둡니다. Ping error/timeout은 read/write failure와 같은 idempotent close 경로로 현재 session만 한 번 해제합니다. Unmatched disconnect는 credential과 deadline을 유지하고 matched/loading/starting disconnect만 기존 cancel 정책을 적용하며, started room의 마지막 client가 사라지면 5분 disconnected TTL을 시작합니다. Bot replacement나 별도 reconnect grace는 없습니다.

일반 gameplay snapshot은 client별 크기 1 latest-only slot에서 합쳐 느린 client가 room tick이나 다른 client를 막지 않게 합니다. `Ready`, `starting`, `started`, `error`는 reliable control queue에서 순서를 보존합니다. 종료 시에는 남은 일반 snapshot을 버리고 `terminal snapshot -> GameEnd -> close` 순서를 socket close 전에 보장합니다. 각 payload write는 새 5초 context를 사용합니다.

Client input:

```json
{
  "ClientTick": 12,
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false
}
```

`ClientTick`은 optional `int64`이고 `0` 이상입니다. 누락하거나 `0`을 보내면 legacy input으로 처리합니다. 양수 tick은 해당 player의 마지막 processed input ACK와 현재 positive pending tick보다 클 때만 command 전체를 저장합니다. 이미 처리했거나 더 높은 pending이 있는 stale/duplicate 양수 tick은 error frame 없이 조용히 무시합니다. Legacy `0`은 기존 last-write-wins를 유지해 양수 pending도 덮을 수 있지만 `LastProcessedClientTick`은 바꾸지 않습니다. 음수는 `invalid_input`이고 기존 pending을 보존합니다.

서버는 유한한 `MoveDir`의 크기가 `1` 이하이면 그대로 보존하고, 더 크면 unit vector로 clamp합니다. Zero가 아닌 유한한 `AttackDir`는 항상 unit vector로 정규화하며, NaN/Inf가 포함된 input은 적용하지 않습니다. Shelly/Colt/Lily는 server-only `3/3/2` attack charge로 시작하고 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. `PressedAttack: true`여도 player가 사망했거나 방향이 zero이거나 charge가 소진됐으면 공격을 거부합니다. Live player의 유한한 양수 input은 Wall 충돌, zero attack 방향, charge 소진처럼 눈에 보이는 효과가 없어도 처리한 것으로 ACK합니다. Unknown/dead player, non-finite, 음수, stale/duplicate input은 ACK하지 않습니다.

Input `PressedAttack`은 server config v3의 캐릭터별 일반 공격 activation 요청입니다. Shelly는 같은 tick에 5발, Colt는 activation tick `A` 기준 `A+[0,6,12,18,24,30]`에 6발, Lily는 2.2 tile centerline melee를 실행합니다. Colt의 후속 emission은 새 activation이 아니므로 snapshot `PressedAttack`은 activation tick에만 `true`입니다.

같은 gameplay `State.Step`의 input은 caller slice를 바꾸지 않고 `PlayerID` 오름차순으로 stable sort한 뒤 적용합니다. 이 순서는 room의 pending input map 순회 순서와 무관한 input 결정성 기준이며 projectile hit target의 순서와는 별개입니다.

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
      "IsBot": false,
      "CharacterType": 1,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    },
    {
      "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
      "Team": "blue",
      "Slot": 0,
      "IsBot": true,
      "CharacterType": 0,
      "SpawnPosition": { "x": 1.2, "y": -1.2 }
    }
  ]
}
```

이 예시는 human 한 명과 bot 한 명으로 정원을 채운 exact 2-participant duel payload입니다. Ready는 human session에만 전달되지만 `Players`에는 bot을 포함한 full participant list가 들어갑니다. Solo/Team Ready는 같은 schema에서 `Players`가 정확히 6개이며, fallback spawn은 Wall/Water를 제외하고 Ground/Bush를 허용합니다.

Server snapshot:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "started",
    "Tick": 1,
    "Players": [
      {
        "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
        "Team": "red",
        "Slot": 0,
        "IsBot": false,
        "CharacterType": 1,
        "Pos": { "x": -1.2, "y": 1.2 },
        "MoveDir": { "x": 0, "y": 0 },
        "AttackDir": { "x": 0, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 3100,
        "PressedAttack": false,
        "IsDead": false,
        "LastProcessedClientTick": 12
      },
      {
        "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
        "Team": "blue",
        "Slot": 0,
        "IsBot": true,
        "CharacterType": 0,
        "Pos": { "x": 1.2, "y": -1.2 },
        "MoveDir": { "x": -1, "y": 0 },
        "AttackDir": { "x": -1, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 4000,
        "PressedAttack": true,
        "IsDead": false,
        "LastProcessedClientTick": 0
      }
    ],
    "Projectiles": [
      {
        "Id": "projectile-1",
        "OwnerId": "player_AbCdEfGhIjKlMnOpQrStUv",
        "Pos": { "x": 1.2, "y": -1.2 },
        "Dir": { "x": -1, "y": 0 },
        "Speed": 13,
        "Damage": 280,
        "Radius": 0.3,
        "Type": "default",
        "IsDestroyed": false
      }
    ]
  }
}
```

Snapshot의 `Players[].PressedAttack`은 input echo가 아니라 방향, 생존 상태, 남은 charge를 검증한 뒤 서버가 해당 tick의 공격을 승인했는지 나타내는 transient 결과입니다.

Snapshot `Projectiles[]`는 기존 `Damage`와 `Type` wire field를 그대로 씁니다. `Damage`는 owner 캐릭터의 `normalAttack.damagePerHit`, `Type`은 `normalAttack.projectile.type`에서 복사합니다. Projectile은 configured range endpoint까지 clamp한 위치에서 Wall/boundary 충돌, player hit, range 만료 순서로 판정하므로 endpoint tangent hit도 포함됩니다. Lily 피해는 `Projectiles`가 아니라 같은 gameplay snapshot의 `Players[].HP/IsDead`로 나타납니다.

Snapshot의 `Players[].IsBot`은 Ready의 participant identity를 그대로 유지합니다. Bot도 human과 같은 `PlayerData`로 simulation에 들어가며 별도 bot snapshot schema를 만들지 않습니다.

Snapshot의 `Players[].LastProcessedClientTick`은 WebSocket 수신이나 pending 저장이 아니라 authoritative `State.Step`이 실제 처리한 마지막 양수 input을 나타내는 processed input ACK입니다. Player마다 독립적으로 단조 증가하고 input이 없는 tick에도 유지됩니다. Bot command에는 `ClientTick`을 넣지 않으므로 bot ACK는 `0`입니다. Match 시작용 Ready ACK와 이 processed input ACK는 서로 다른 계약입니다.

Projectile hit은 room이 시작할 때 고정한 selected mode rules를 사용합니다. 모든 mode에서 owner와 이미 사망한 player를 제외하고, Solo는 나머지 live player를 모두 적으로 봅니다. 현재 `friendlyFire=false`인 Team/Duel은 ally를 통과하고 enemy만 hit합니다. 같은 tick에 여러 eligible target이 겹치면 player의 join/배정 순서에서 첫 target만 피해를 받고 projectile이 destroy됩니다.

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

Started signal:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "started",
    "Tick": 0,
    "Players": null,
    "Projectiles": null
  }
}
```

`starting`과 `started` control snapshot에는 player ACK를 넣지 않습니다. 첫 gameplay snapshot인 `Tick: 1`부터 모든 `Players[]`에 `LastProcessedClientTick` key가 있습니다.

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

Malformed JSON, 음수 `ClientTick` 같은 invalid input은 위 error를 보내고 connection을 유지합니다. 이미 처리했거나 더 높은 positive pending에 뒤처진 stale/duplicate 양수 tick은 error를 보내지 않습니다.

GameEnd event:

```json
{
  "Type": "GameEnd",
  "PlayerId": "player_VuTsRqPoNmLkJiHgFeDcBa",
  "Result": "Win"
}
```

Field 이름은 Unity prototype과 맞춰 `ClientTick`, `MoveDir`, `AttackDir`, `PressedAttack`, `Id`, `IsBot`, `OwnerId`, `Pos`, `Dir`, `HP`, `IsDead`, `LastProcessedClientTick`, `IsDestroyed`처럼 유지합니다.
단, match lifecycle field인 `Snapshot.status`와 `Snapshot.countdown`은 REST `room.status`와 맞춰 lowercase입니다. `starting`의 `countdown`은 client fake timer 기준값이며, server는 중간 countdown 값을 broadcast하지 않습니다.

GameEnd wire field와 enum은 그대로이고 판정만 room-local mode를 따릅니다.

| Mode | 결과 |
| --- | --- |
| `duel_1v1` | 한 player가 죽으면 survivor Win/dead player Lose, 같은 tick 동시 사망은 둘 다 Draw입니다. |
| `solo` | Solo 중간 탈락 player는 Lose와 close를 받고 나머지는 계속합니다. 마지막 생존자는 Win입니다. 전원 사망은 새로 결과가 확정되는 player에게 Draw입니다. |
| `team` | Team 일부 사망은 계속합니다. 한 team 전멸은 패배 team 3명은 Lose, 상대 team 3명은 Win입니다. 양 team이 같은 tick에 전멸하면 6명 모두 Draw입니다. |

Player별 첫 결과는 바뀌지 않습니다. 예를 들어 Solo 이전 Lose는 유지되고, 뒤의 전원 사망 tick에서는 아직 결과가 없던 player만 Draw를 받습니다. 중간 탈락 connection에는 `terminal snapshot -> GameEnd -> close`를 보내지만 room ticker는 계속 실행합니다.

Bot도 mode별 GameEnd result ledger 계산에는 포함됩니다. 다만 bot에는 WebSocket session이 없으므로 terminal snapshot, `GameEnd`, transport close는 human session에만 전달합니다.

Room terminal decision에서는 ticker를 terminal decision 즉시 중단하고 tick observer, encode, enqueue를 진행합니다. 각 terminal session의 connected-client observer는 session close callback에서 반영되어 transport `closeDone`보다 먼저일 수 있습니다. Normal cleanup은 current terminal session과 앞서 결과가 확정되어 기억한 session의 `closeDone`을 모두 기다립니다. 따라서 current client map에서 이미 빠진 Solo prior loser도 barrier에 남습니다. 그 뒤 registry를 분리하고 active-room observer를 반영한 다음 player ID를 release하고 `room_ended` log와 resource close를 수행하며 cleanup success signal은 마지막에 닫습니다. Hard TTL과 debug removal은 ending room을 제거하지 않습니다.

`Shutdown`은 forced-teardown 예외입니다. `closeDone` 전에 registry/player ID를 detach할 수 있지만 GameEnd cleanup worker와 모든 session close/writer/heartbeat/lifecycle을 join합니다. 이 takeover는 normal cleanup signal을 닫지 않고 `room_ended`를 기록하지 않습니다.

Room TTL은 Store당 하나의 30초 janitor가 검사하며, create/matchmaking이 active room cap에 닿았을 때만 즉시 cleanup을 한 번 수행하고 생성도 한 번만 재시도합니다.

## 현재 gameplay 값

- tick rate: 30Hz
- tile size: 1.2
- player speed: 2
- player radius: 0.5
- character catalog: `0=Shelly`, `1=Colt`, `2=Lily`; HP는 각각 `4000/3100/4100`
- attack charges: Shelly/Colt/Lily `3/3/2`
- attack recharge: 캐릭터별 최대치보다 적을 때 30 tick마다 1 charge
- projectile speed: 13
- projectile damage: owner의 `normalAttack.damagePerHit` (`280/340`; Lily `1100`은 melee)
- projectile radius: 0.3
- map/debug room max players: 6

Gameplay config artifact는 client 공유용과 server runtime용을 분리합니다.

- `client-config/game-config.json`: client build가 sparse checkout해서 가져가는 공유 config입니다. legacy `playerTypes: ["default"]` mirror와 함께 v2 `characters` catalog (`0/1/2 = shelly/colt/lily`)를 포함합니다. radius `0.5`, speed `2`, HP `4000/3100/4100`의 canonical runtime mapping은 server config가 소유합니다.
- `server-config/game-config.json`: server binary가 embed해서 room store와 simulation 기본값으로 쓰는 server-only v3 config입니다. `tickRate`, `tile.size`, player type별 `normalAttack`, player/projectile type별 runtime 값, `mode.default`와 `mode.catalog`, `map`을 포함합니다. Charge/recharge는 Shelly `3/30`, Colt `3/30`, Lily `2/30`입니다.

Client는 gameplay state를 여전히 서버 snapshot에서 받습니다. `HP`, speed, damage, tick rate, map과 attack charge 진행도는 server-only config/state나 Ready/snapshot message의 책임입니다.

## 기본 duel 2인 수동 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 응답의 secret-bearing `webSocketPath`를 client 내부에서만 사용해 WebSocket을 엽니다. Raw path/query를 log에 남기지 않습니다.
3. 두 연결이 같은 `Type: "Ready"` event를 받아야 합니다.
4. Ready event의 `Map.map` row는 숫자 배열이어야 하고, `Players[].SpawnPosition`이 있어야 합니다.
5. 두 client가 `{"Type":"ready"}`를 보내면 `starting` 신호를 1번 받고, 중간 countdown broadcast 없이 5초 뒤 `started`를 받아야 합니다.
6. 한 client가 양수 `ClientTick`과 movement input을 보내면 ACK는 수신 직후가 아니라 다음 gameplay `State.Step`의 snapshot에서 올라가고 두 연결이 같은 값을 받아야 합니다.
7. 더 낮거나 같은 양수 `ClientTick`을 다시 보내면 error 없이 무시되고 ACK와 gameplay state가 되돌아가지 않아야 합니다.
8. 다른 player의 processed input ACK는 독립적으로 유지되어야 하며 bot의 ACK는 `0`이어야 합니다.
9. 공격이 target에 닿으면 두 연결에서 projectile `IsDestroyed: true`, target `HP` 감소가 보여야 합니다.
10. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
11. HP가 0이 된 tick의 snapshot 이후 player별 `GameEnd`를 받아야 합니다.
12. `duel_1v1` 한 player 사망은 Win/Lose, 같은 tick 동시 사망은 둘 다 Draw여야 합니다.
13. Terminal close가 끝난 뒤 해당 room registry와 player ID가 정리되어야 합니다.
14. 잘못된 JSON과 음수 `ClientTick`은 `invalid_input` error를 보내고 snapshot stream은 계속되어야 합니다.

자동 회귀는 `go test ./internal/rooms`가 담당합니다.

## SL-82 CharacterType 계약

`POST /matchmaking/join`의 optional lower-camel `characterType`은 stable ID `0=Shelly`, `1=Colt`, `2=Lily`를 받습니다. 새 client는 값을 명시하고, SL-82에서는 legacy field 생략만 Shelly `0`으로 보정하며 structured warning을 한 번 기록합니다. explicit `null`, non-integer, string/bool/object/array, 지원하지 않는 정수는 400 `invalid_character_type`이고 SL-98에서 request field를 required로 전환합니다.

REST `Player.characterType`은 required이며 top-level `player`와 nested `room.players[]`가 같은 값을 반환합니다. WebSocket Ready와 gameplay Snapshot은 required PascalCase `CharacterType`으로 canonical participant identity를 보존합니다. Bot/debug participant는 Shelly `0`입니다. Config v2는 identity/render catalog를 유지하고 server config v3의 현재 stats는 Shelly `4000`, Colt `3100`, Lily `4100` HP와 `3/3/2` attack charge, 공통 30 tick recharge입니다.

## 제약

이 API는 development surface입니다. Player session 인증, debug Bearer guard, matchmaking rate limit, WebSocket heartbeat는 구현되어 있습니다. Account auth, production matchmaking, persistence, bot replacement, reconnect grace, respawn, score, dashboard, scheduler, Kubernetes는 없습니다.
