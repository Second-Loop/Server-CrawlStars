# API 문서화 기준

REST 계약은 OpenAPI, WebSocket message 계약은 AsyncAPI로 문서화합니다. 사람이 읽는 요약은 `ai-docs/api-reference.md`에 둡니다.

## 원칙

- REST endpoint와 bounded request/response는 `api/openapi.yaml`에 기록합니다.
- WebSocket channel과 message payload는 `api/asyncapi.yaml`에 기록합니다.
- `/rooms` debug API는 정식 gameplay contract가 아니라 development surface입니다.
- Debug OpenAPI operation 일곱 개에는 `DebugBearer` security와 401/disabled-default 404를 함께 기록합니다. WebSocket GET에는 이 scheme을 적용하지 않습니다.
- Secret 예시는 `token=<player-session-token>`처럼 가리고 실제 발급값을 문서에 붙여 넣지 않습니다.
- Debug API를 stable client contract로 승격하려면 별도 Linear issue와 문서 업데이트가 필요합니다.
- 계약 변경이 있으면 `make docs-build` 또는 `make ci` 결과를 PR에 남깁니다.

## Hosted docs

서버는 다음 path를 제공합니다.

```text
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
```

- `/openapi`: Swagger UI wrapper. `persistAuthorization: false`라 debug Bearer를 browser storage에 유지하지 않습니다.
- `/asyncapi`: repository-owned static HTML
- `*.yaml`: raw spec

Docs build는 `docs-ui`의 dependency-free Node script가 담당합니다. 생성물은 `internal/docs/api/`, `internal/docs/static/`에 만들어지고 Go `embed`로 binary에 포함됩니다. 생성물은 git에 commit하지 않습니다.

Source와 생성물의 책임은 다음처럼 나눕니다.

| 계약/예시 | 기준 source | 생성/서빙 위치 |
| --- | --- | --- |
| REST schema와 examples | `api/openapi.yaml` | `internal/docs/api/openapi.yaml`, `/openapi`, `/openapi.yaml` |
| WebSocket schema와 examples | `api/asyncapi.yaml` | `internal/docs/api/asyncapi.yaml`, `/asyncapi.yaml` |
| 사람이 읽는 docs UI 설명과 JSON examples | `docs-ui/scripts/build.mjs` | `internal/docs/static/asyncapi.html`, `/asyncapi` |

`docs-ui/scripts/build.mjs`의 examples는 source spec과 같은 human/bot identity를 보여야 합니다. Generated embed 파일은 검증 순서의 build 단계에서만 갱신하고 직접 수정하지 않습니다.

## 현재 REST surface

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

`POST /matchmaking/join`은 optional `gameMode`로 `duel_1v1`, `solo`, `team`을 선택하고, Unity client가 top-level `gameMode`, 같은 값의 `room.gameMode`, human `player`, `sessionToken`, tokenized `webSocketPath`를 한 번에 받을 수 있게 하는 simple connector입니다. Body가 없거나 빈 object이거나 `gameMode`가 빈 문자열이면 `duel_1v1`을 사용합니다. 선택 mode의 participant capacity는 duel 2명, solo/team 6명이며, 첫 human join의 `0 -> 1` 전이에서만 room-owned 10초 deadline을 시작하고 deadline은 남은 participant slot을 bot으로 채웁니다. 후속 join이나 partial manual bot 추가는 reset하지 않습니다. Timer와 late human join은 같은 matchmaking lock을 먼저 얻은 transition이 이기며, timer-first late join은 다른 waiting room을 찾거나 만들고 active cap이면 기존 `room_cap_reached` 409를 받습니다. Ready payload는 full participant를 담지만 human session만 attach/Ready ACK quorum에 들어가고 public `room.status: waiting`은 Ready/start 전까지 유지합니다. Production queue, rating, account auth, persistence는 없습니다.

Join raw body가 1024 bytes를 초과하거나 JSON이 잘못되면 400 `invalid_request`, 지원하지 않는 non-empty mode면 400 `invalid_game_mode`를 반환합니다.

Room/player ID는 random opaque pattern으로 문서화합니다. Raw player token은 발급 응답의 `sessionToken`과 tokenized `webSocketPath` 두 곳에 같은 secret으로 나타나며 inbound query로 다시 전달됩니다. Public Room/Player/list/detail/Ready/Snapshot/GameEnd schema에는 raw token이나 digest field를 두지 않습니다.

OpenAPI `Player`는 room participant schema라 required `isBot` boolean이 있고 human과 bot을 모두 허용합니다. `Room.players[]`는 이 generic schema를 사용합니다. Credential-bearing `MatchmakingJoin.player`와 `PlayerSessionResponse.player`는 `HumanPlayer`를 사용하며, `isBot`은 `const: false`입니다. Bot은 session token과 WebSocket path가 없고 public bot creation endpoint도 없습니다.

Join의 process-local per-IP token bucket은 store보다 먼저 평가합니다. OpenAPI 429에는 `rate_limited` JSON, 최소 1초 정수 `Retry-After`, 429가 409/500보다 우선하고 허용된 409/500 요청도 quota를 소비한다는 내용을 기록합니다. 같은 IP에서 6-client smoke를 실행할 때는 client가 `Retry-After` 뒤 재시도하거나 격리된 local 환경에서만 burst 6을 명시합니다.

Room debug API는 기본 비활성화되어 `404 not_found`를 반환합니다. 활성화하면 정확히 하나의 `Authorization: Bearer <DEBUG_API_TOKEN>`이 필요하고, missing/wrong/multiple credential은 route dispatch보다 먼저 `401 unauthorized`입니다. 올바른 credential 뒤에 기존 route 결과를 평가합니다. Matched 이후 matchmaking room에 debug player를 추가하면 409 `room_full`입니다. `DELETE /rooms`와 `DELETE /rooms/{roomID}`는 테스트 중 active room cap을 즉시 회복하기 위한 operation입니다. Room response에는 server simulation이 쓰는 `map` 데이터와 `latestSnapshot` summary가 포함됩니다. 외부 응답의 `map` row는 Base64 문자열이 아니라 JSON number array입니다. OpenAPI와 AsyncAPI의 `MapData` tile item enum은 `[0, 1, 2, 3, 4]`이며 각각 Ground, Wall, SpawnPoint, Bush, Water입니다. Player는 Wall/Water, projectile은 Wall에 충돌하고 map boundary는 둘 다 막습니다. Map config는 명시적 SpawnPoint와 passable fallback의 고유 좌표가 `map.maxPlayers` 이상이어야 합니다.

Match Ready event, ready ACK, 5초 server-internal countdown, matched/loading/starting disconnect cancel은 WebSocket 계약에서 다룹니다. 새 REST polling이나 SSE를 늘리지 않고 Ready event와 기존 gameplay WebSocket wrapper인 `Type: snapshot` 안의 `Snapshot.status`/`Snapshot.countdown`을 사용합니다. `starting`은 countdown 시작 신호로 1번만 보냅니다.

## 현재 WebSocket surface

AsyncAPI channel address:

```text
WS /rooms/{roomID}/players/{playerID}
```

AsyncAPI channel `address`는 query를 붙이지 않은 path-only 값으로 유지하고, WebSocket binding의 query object에서 43자 `token` 하나를 required로 선언합니다. Server security는 `playerSessionToken` httpApiKey를 참조합니다. 정상적인 extra query key는 허용하므로 `additionalProperties: false`를 두지 않지만, malformed query pair는 전체 query를 401로 거부한다고 설명합니다.

Handshake 순서는 room 404, player 404, token 401, live connection 또는 in-flight reservation 409입니다. Token credential은 room/player session이 남아 있는 동안 재사용할 수 있습니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 pre-start cancel로 room을 삭제합니다. Failed upgrade는 room을 취소하지 않아 같은 경로로 재시도할 수 있습니다. Raw token과 전체 query 문자열은 log에 남기지 않습니다.

AsyncAPI document dialect는 계속 `asyncapi: 3.0.0`이고, API 계약을 나타내는 `info.version`은 bot identity가 추가된 `0.4.0`입니다. `ReadyPlayer`는 required `IsBot`, `PlayerData`도 required `IsBot`을 가지며 모든 Ready/gameplay example의 모든 player object가 boolean `IsBot`을 정확히 한 번 포함합니다.

AsyncAPI에는 message schema뿐 아니라 연결 생명주기도 기록합니다. Participant capacity는 human+bot 합계이고, Ready payload는 full participant list를 포함하지만 human WebSocket session에만 전달합니다. Attach와 Ready ACK는 human-only quorum이며 bot은 WebSocket sender가 아닙니다. Server heartbeat는 30초 간격이고 Ping마다 90초 deadline을 사용합니다. 일반 gameplay snapshot만 client별 latest-only로 합치며, Ready/lifecycle/error control과 `terminal snapshot -> GameEnd -> close`는 reliable ordering을 유지합니다. Payload write는 매번 새 5초 context를 사용합니다. Ping/read/write failure는 같은 close-once 정책으로 현재 session만 해제하며 bot replacement와 reconnect grace는 범위 밖입니다.

Input field는 Unity prototype 이름을 따릅니다.

```json
{
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false
}
```

서버는 유한한 `MoveDir`의 크기가 `1` 이하이면 그대로 보존하고, 더 크면 unit vector로 clamp합니다. Zero가 아닌 유한한 `AttackDir`는 항상 unit vector로 정규화하며, NaN/Inf가 포함된 input은 적용하지 않습니다. Player별 attack budget은 server-only이며 기본 4 charge로 시작해 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. 사망한 player의 input과 zero 방향 또는 소진된 charge의 공격 요청은 거부합니다.

같은 tick의 input은 `PlayerID` 오름차순으로 stable sort해 적용합니다. AsyncAPI `ProjectileData` 설명은 selected mode rules에 따른 owner/dead 제외, Solo와 `friendlyFire=false` Team/Duel의 hit eligibility, join/배정 순서 target tie-break, death snapshot 이후 SL-89 경계를 함께 기록하며 wire field는 바꾸지 않습니다.

Server message wrapper:

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
        "Pos": { "x": -1.2, "y": 1.2 },
        "MoveDir": { "x": 0, "y": 0 },
        "AttackDir": { "x": 0, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 100,
        "PressedAttack": false,
        "IsDead": false
      },
      {
        "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
        "Team": "blue",
        "Slot": 0,
        "IsBot": true,
        "Pos": { "x": 1.2, "y": -1.2 },
        "MoveDir": { "x": -1, "y": 0 },
        "AttackDir": { "x": -1, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 100,
        "PressedAttack": true,
        "IsDead": false
      }
    ],
    "Projectiles": null
  }
}
```

Snapshot의 `Players[].PressedAttack`은 input echo가 아니라 방향, 생존 상태, 남은 charge를 검증한 뒤 서버가 해당 tick의 공격을 승인했는지 나타내는 transient 결과입니다.

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
    "map": [[1, 1, 1, 1, 1]]
  },
  "Players": [
    {
      "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
      "Team": "red",
      "Slot": 0,
      "IsBot": false,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    },
    {
      "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
      "Team": "blue",
      "Slot": 0,
      "IsBot": true,
      "SpawnPosition": { "x": 1.2, "y": -1.2 }
    }
  ]
}
```

Ready 예시는 human 한 명과 bot 한 명으로 채운 exact 2-participant duel cardinality와 5x5 fallback map 기준입니다. Ready는 human session에만 전달하지만 payload에는 full participant list가 들어갑니다. 실제 기본 runtime map은 server binary가 embed한 `server-config/game-config.json`의 20x20 map이며 client SL-79에서 merge된 `Map_0`과 exact grid가 같습니다. Spawn은 `TileSpawnPoint(2)`를 먼저 쓰고 부족하면 Wall/Water를 제외한 Ground/Bush fallback candidate를 사용합니다.

Match ready ACK:

```json
{
  "Type": "ready"
}
```

| mode | Ready `Players` 길이 | 필요한 WebSocket/ACK |
| --- | ---: | --- |
| `duel_1v1` | 2 | Room 내 human participant 전원 |
| `solo` | 6 | Room 내 human participant 전원 |
| `team` | 6 | Room 내 human participant 전원 |

Human participant가 0명이면 attach/ACK quorum은 성립하지 않습니다. Bot ID 발급이 하나라도 실패하면 participant를 부분 추가하지 않고 ID 예약을 rollback한 뒤 `bot_fill_failed` structured log event를 한 번 기록하며 retry하지 않습니다. 일반 delete/clear/cancel은 room lock 아래에서 timer resource를 detach한 뒤 모든 core lock 밖에서 ticker `Stop`과 stop channel close를 수행합니다. 일반 cleanup은 worker join을 기다리지 않고, `workerWG.Wait`는 Shutdown만 추가로 수행합니다. ClientTick/ACK 확장은 SL-94 범위라 이 계약에는 추가하지 않습니다.

Solo는 `solo-1`부터 `solo-6`까지 각 slot 0을 사용합니다. Team은 join 순서대로 `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2`를 사용합니다. Ready spawn과 첫 gameplay snapshot position은 같은 room-local `PlayerAssignments` 결과입니다. Fallback map에서는 player collision과 같은 기준으로 Wall과 Water를 spawn candidate에서 제외하고 Ground와 Bush를 허용합니다.

Countdown snapshot:

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

## Stability marker

Development API에는 spec과 설명 문서에서 다음을 명시합니다.

- `x-stability: e1-debug`
- debug 또는 development surface라는 설명
- Unity client가 장기 의존해도 되는 계약인지 여부

## Validation

```sh
rtk node docs-ui/scripts/validate.mjs
REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_UPDATE_NOTICE=true rtk npx --yes --package @redocly/cli@2.38.0 redocly lint --extends=minimal api/openapi.yaml
rtk npx --yes --package @asyncapi/cli@6.0.2 asyncapi validate api/asyncapi.yaml --fail-severity=error
rtk node docs-ui/scripts/build.mjs
rtk mise exec -- env GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod" go test ./internal/docs -count=1
```

순서는 source-level custom validator → pinned Redocly/AsyncAPI official validator → docs build → generated embed를 읽는 Go docs test입니다. `docs-ui/scripts/validate.mjs`는 raw spec의 필수 version, stability marker, security count, opaque pattern, error/status marker, tokenized path, path-only binding, public DTO secret 부재와 exhaustive player example을 확인합니다. 이 marker validator는 YAML parser가 아니므로 pinned official CLI의 OpenAPI/AsyncAPI parse와 schema validation을 생략할 수 없습니다. 전체 validation은 이어서 `make ci`로 확인합니다.
