# 프로토콜

현재 protocol surface는 E2 client-server integration을 위한 development 계약입니다. Production gameplay protocol은 아직 아닙니다.

## 현재 구현

- `duel_1v1`, `solo`, `team`을 선택하는 same-mode matchmaking join
- room/player WebSocket
- server-authoritative snapshot stream
- client SL-79 `Map_0` 기반 static map movement
- player Wall/Water/boundary collision과 projectile Wall/boundary destroy
- Bush는 둘 다 통과하고 projectile은 Water도 통과
- projectile hit, HP, death snapshot
- GameEnd Win/Lose/Draw event와 종료 room 정리
- matchmaking Ready event/ready ACK/countdown/start
- start 전 match cancel
- opaque room/player ID와 player session WebSocket 인증
- 기본 비활성화된 debug REST Bearer guard
- matchmaking join IP별 token-bucket rate limit
- 30초 WebSocket heartbeat와 Ping별 90초 deadline
- client별 snapshot coalescing과 reliable control/terminal delivery
- Store당 30초 cleanup janitor와 cap-pressure 단일 cleanup/retry
- client build용 shared game config artifact

아직 구현하지 않은 것:

- bot replacement와 별도 reconnect grace
- respawn, score
- production matchmaking queue

## Simulation 계약

```text
internal/simulation.State.Step(inputs []InputCommand) Snapshot
```

이 계약은 transport와 분리되어 Go unit test에서 직접 검증합니다. REST, WebSocket, room lifecycle, matching queue는 simulation package 안으로 들어오지 않습니다.

`Step` 순서:

1. 모든 player의 transient `PressedAttack`을 `false`로 초기화
2. 최대 charge보다 적은 player의 attack recharge tick 진행
3. 기존 projectile 이동
4. projectile의 Wall/boundary 충돌 destroy와 player hit 처리
5. live player의 유한한 input만 적용하고 방향 벡터 검증
6. movement는 X축, Y축 순서로 player의 Wall/Water/boundary collision 검사
7. 공격 입력, non-zero 방향, 남은 charge가 모두 유효하면 projectile 생성
8. tick 증가
9. snapshot 반환

현재 값:

- `TickRate = 30`
- `TileSize = 1.2`
- `DefaultPlayerSpeed = 2`
- `DefaultPlayerRadius = 0.5`
- `DefaultPlayerHP = 100`
- `MaxAttackCharges = 4`
- `AttackRechargeTicks = 30`
- `DefaultProjectileSpeed = 13`
- `DefaultProjectileDamage = 10`
- `DefaultProjectileRadius = 0.3`
- tile 값은 `0=Ground`, `1=Wall`, `2=SpawnPoint`, `3=Bush`, `4=Water`
- Player는 Wall/Water/boundary, projectile은 Wall/boundary에 충돌
- `StaticMapFixture().MaxPlayers = 6`
- Player spawn은 map의 `TileSpawnPoint(2)`를 join 순서대로 먼저 사용합니다. SpawnPoint가 부족하면 map에서 유도한 fallback candidate를 사용하되 player blocking policy와 같은 기준으로 Wall과 Water를 제외합니다. Ground와 Bush는 후보가 될 수 있습니다. Config 검증은 명시적 SpawnPoint와 passable fallback의 고유 좌표가 `map.maxPlayers` 이상인지 확인하므로, 정상 room의 spawn은 서로 겹치지 않습니다.
- server mode catalog는 `duel_1v1`, `solo`, `team`이고 body가 선택을 생략하면 default `duel_1v1`입니다.

Config artifact는 client 공유용과 server runtime용을 분리합니다.

`client-config/game-config.json`은 Unity client가 build 때 sparse checkout해서 runtime asset 경로로 복사하는 공유 config입니다.

- `tileSize`
- `playerRadius`
- `playerTypes`
- `projectileRadius`
- `projectileTypes`

`server-config/game-config.json`은 server binary가 embed해서 room store와 simulation 기본값으로 쓰는 server-only config입니다.

- `tickRate`
- `tile.size`
- player type별 `id/radius/hp/speed/maxAttackCharges/attackRechargeTicks`
- projectile type별 `id/radius/damage/speed`
- `mode.default`
- `mode.catalog[].id`, `mode.catalog[].playersPerMatch`, `mode.catalog[].teams`, `mode.catalog[].rules`
- `map`: client SL-79에서 merge된 `Map_0`과 값이 같은 20x20 exact grid

Client는 여전히 최종 gameplay state를 서버 snapshot에서 받습니다. `HP`, speed, damage, tick rate, map은 server snapshot이나 Ready event로 받거나 서버만 판단하므로 client 공유 config에 넣지 않습니다.
Mode/team rule도 server-only입니다. REST join response와 Room은 선택된 mode ID만 `gameMode`로 노출하고 `friendlyFire`, `teamBehavior`, 전체 catalog는 노출하지 않습니다. `friendlyFire`는 아직 metadata이며 projectile 판정 활성화는 별도 issue 범위입니다. SL-86은 WebSocket message shape를 바꾸지 않습니다.
Attack charge와 recharge 진행도도 server-only state이며 client config나 snapshot에 새 field로 노출하지 않습니다.

## WebSocket 계약

```text
WS /rooms/{roomID}/players/{playerID}?token=<player-session-token>
```

연결 조건:

- room이 존재해야 합니다.
- player가 room에 속해야 합니다.
- 정확히 한 개의 non-empty `token` query가 발급된 player session과 일치해야 합니다.
- 정상적인 extra query key는 허용하지만 어느 query pair든 malformed하면 401입니다.
- 같은 room/player의 live connection 또는 in-flight reservation은 409로 거부합니다.
- 검증 우선순위는 room 404, player 404, token 401, connection/reservation 409입니다.
- waiting room도 연결과 input은 허용합니다.
- gameplay snapshot은 room이 started가 된 뒤에만 broadcast합니다.
- payload write는 client별 writer가 수행하며 매번 새 5초 context를 사용합니다.

Token은 일회용 credential이 아니며 room/player session이 존재하는 동안 재사용할 수 있습니다. 다만 matchmaking matched/loading/starting 단계의 실제 disconnect는 pre-start cancel로 room을 삭제하므로 그 뒤에는 reconnect할 수 없습니다. Started room도 all-disconnected 5분 TTL과 hard 1시간 lifetime 안에서만 남습니다. Failed HTTP-to-WebSocket upgrade는 reservation만 rollback해 같은 발급 경로로 재시도할 수 있습니다.

발급 JSON의 `sessionToken`, tokenized `webSocketPath`, inbound query는 모두 같은 raw secret을 담습니다. Raw token과 전체 query 문자열을 log나 telemetry에 남기지 않습니다. Ready/Snapshot/GameEnd payload에는 token이나 digest가 없습니다.

Server는 각 connection에 snapshot writer와 독립적인 30초 heartbeat ticker를 둡니다. 각 Ping은 90초 context로 제한하며 error/timeout은 read/write failure와 같은 close-once 경로로 현재 session만 해제합니다. 오래된 heartbeat가 늦게 실패해도 expected-session identity가 다르면 reconnect된 connection을 제거하지 않습니다. Pre-start에서는 match cancel, started room에서 마지막 client가 사라지면 disconnected TTL을 시작합니다. Bot replacement나 reconnect grace는 만들지 않습니다.

일반 gameplay snapshot만 크기 1 latest-only slot에서 coalescing합니다. `Ready`, `starting`, `started`, `error`는 reliable control queue에서 FIFO를 유지합니다. 종료 시 남아 있던 일반 snapshot을 폐기하고, 이미 수락한 control 뒤에 `terminal snapshot -> GameEnd -> close`를 순서대로 실행합니다. Queue overflow, marshal/write failure도 해당 session을 close/release합니다.

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
    "status": "started",
    "Tick": 1,
    "Players": [],
    "Projectiles": []
  }
}
```

Matchmaking room은 선택 mode의 matched player가 모두 WebSocket에 연결되면 먼저 `Ready` event를 보냅니다. 이 event는 client가 map을 렌더하고 player spawn을 배치하는 기준 데이터입니다.

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
    },
    {
      "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
      "Team": "blue",
      "Slot": 0,
      "SpawnPosition": { "x": 1.2, "y": -1.2 }
    }
  ]
}
```

예시는 exact 2-player duel cardinality와 5x5 fallback map 기준입니다. 실제 기본 runtime map은 `server-config/game-config.json`의 20x20 map입니다. SpawnPoint를 먼저 쓰고 부족하면 Wall/Water를 제외한 Ground/Bush fallback candidate를 사용하므로 실제 위치는 예시와 다를 수 있습니다.

Ready ACK:

```json
{
  "Type": "ready"
}
```

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

Invalid input은 connection을 끊지 않습니다.

GameEnd event:

```json
{
  "Type": "GameEnd",
  "PlayerId": "player_VuTsRqPoNmLkJiHgFeDcBa",
  "Result": "Win"
}
```

HP가 0인 player가 생기면 server는 같은 tick의 마지막 snapshot을 먼저 보낸 뒤 player별 `GameEnd` event를 보냅니다. 일부 player만 사망하면 생존 player는 `Win`, 사망 player는 `Lose`이고 모든 player가 사망하면 모두 `Draw`입니다. GameEnd calculator에는 room-local selected config를 전달하지만 SL-86은 solo/team 전용 elimination 규칙을 새로 추가하지 않고 기존 player-survival fallback을 유지합니다. Server는 `GameEnd` 전송 후 room-local ticker와 WebSocket connection을 정리합니다.

## Field 의미

`AttackDir`와 `PressedAttack`은 분리합니다.

- input `AttackDir`: 현재 조준 방향. zero가 아닌 유한한 값은 서버가 unit vector로 정규화합니다.
- input `PressedAttack`: 이번 tick의 발사 요청
- snapshot `PressedAttack`: 방향과 charge 검증을 통과해 서버가 이번 tick 공격을 승인했는지 나타내는 transient 결과

`AttackDir != zero`로 공격을 추론하면 조준 유지 중 매 tick 발사될 수 있습니다. 그래서 input 계약에서는 `PressedAttack`을 유지합니다.

`MoveDir`은 크기가 `1` 이하면 아날로그 입력을 그대로 보존하고, 더 크면 서버가 크기 `1`로 clamp합니다. 각 player는 4 charge로 시작하고 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. Zero `AttackDir`, 소진된 charge, 사망한 player input은 projectile을 만들지 않습니다.

`IsDead`는 `HP <= 0`에서 유도할 수 있지만 snapshot에는 유지합니다. Client가 death rule을 재해석하지 않아도 되고, future state를 추가하기 쉽기 때문입니다.

이 변경은 기존 input/snapshot field를 그대로 사용하므로 public WebSocket schema를 바꾸지 않습니다. Attack charge 자체를 client에 노출하는 변경은 별도 schema issue에서 다룹니다.

## Matchmaking 계약

현재:

```text
POST /matchmaking/join
```

Request body는 optional입니다.

```json
{"gameMode":"solo"}
```

Canonical mode ID는 `duel_1v1`, `solo`, `team`입니다. Body 없음, `{}`, `{"gameMode":""}`는 default `duel_1v1`로 normalize합니다. 지원하지 않는 non-empty ID는 400 `invalid_game_mode`, malformed JSON과 trailing JSON value는 400 `invalid_request`입니다.

응답:

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

Top-level `gameMode`와 nested `room.gameMode`는 항상 같습니다. Store는 요청 mode를 catalog에서 canonical config로 한 번 선택하고 같은 `gameMode`의 waiting room만 재사용합니다. 새 room은 이 selected config를 immutable하게 소유하며 이후 Store default가 바뀌어도 capacity, team/slot, Ready, simulation, tick interval, GameEnd가 room-local config를 사용합니다.

Room/player ID는 각각 16 random bytes 기반 22자 Raw URL Base64 payload와 `room_`/`player_` prefix를 사용합니다. Session token은 32 random bytes 기반 43자 Raw URL Base64 value입니다. Private room state에는 raw token 대신 SHA-256 digest만 저장하고 public Room/Player payload에는 token/digest field를 넣지 않습니다.

Join handler는 `client IP resolve → token-bucket quota 평가/소비 → body decode와 mode 검증 → store join` 순서로 실행합니다. 기본값은 process-local per-IP 10 requests/minute, burst 4입니다. Quota가 없으면 429 `rate_limited`와 최소 1초 정수 `Retry-After`가 malformed/unknown mode 400, room cap 409, `internal_error` 500보다 먼저 나갑니다. Store에서 409/500으로 끝난 허용 요청도 quota를 소비하며, non-POST 405는 소비하지 않습니다.

Immediate peer가 `TRUSTED_PROXY_CIDRS`에 속하고 `CF-Connecting-IP`가 정확히 하나의 valid IP일 때만 forwarded client IP를 씁니다. Header가 absent/malformed/multiple이면 peer bucket으로 fallback하고 `X-Forwarded-For`는 무시합니다.

선택 mode의 required player 수가 차도 `room.status`는 `waiting`입니다. 해당 room은 matchmaking match로 잠겨 late join 대상에서 빠집니다.

Selected mode의 required player 2명 또는 6명이 각각 서로 다른 WebSocket에 연결하면 모든 connection이 같은 `Type: "Ready"` event를 받습니다. Ready event에는 JSON number array 형태의 `Map.map`과 room-local assignment의 `Players[].Team`, `Slot`, `SpawnPosition`이 들어갑니다. Duel은 서로 다른 2명, Solo/Team은 서로 다른 6명의 human player session이 `{"Type":"ready"}`를 보내야 합니다. 같은 player의 중복 ACK는 idempotent하며 quorum을 늘리거나 countdown을 다시 시작하지 않습니다. Quorum 뒤 server는 `Snapshot.status: "starting"`과 `Snapshot.countdown: 5`를 connection당 1번 보내고, 5초를 내부에서 센 뒤 `Snapshot.status: "started"`를 1번 보낸 다음 room-local simulation ticker 하나를 시작합니다. Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 없습니다.

첫 번째 player만 연결된 상태에서는 room이 `waiting`이라 WebSocket input은 저장되지만 gameplay snapshot은 오지 않습니다. 1명으로 테스트하려면 debug API `POST /rooms/{roomID}/start`를 호출해야 합니다.

Room response와 Ready event의 `map`은 서버 simulation이 collision에 쓰는 tile grid입니다. `map` row는 Base64 문자열이 아니라 JSON number array로 직렬화합니다. 기본 map source는 server binary가 embed한 `server-config/game-config.json`의 `map`입니다. 서버가 이 config 로드나 검증에 실패하면 `StaticGameConfig()`의 5x5 map으로 fallback합니다. `internal/simulation/fixtures/default-map.json`은 runtime source가 아니라 테스트와 legacy 호환 확인용 fixture입니다.

`room.maxPlayers`와 `room.map.maxPlayers`는 map/debug room capacity를 뜻하며 runtime map과 5x5 fallback map 모두 6입니다. Matchmaking required players는 room-local selected mode의 `playersPerMatch`입니다. `duel_1v1`은 2명, `solo`와 `team`은 6명이며 다른 mode끼리는 waiting room을 공유하지 않습니다. Solo team 값은 `solo-1`부터 `solo-6`, team mode assignment는 `red/0, blue/0, red/1, blue/1, red/2, blue/2`입니다.

`SL-58`에서는 당시 `POST /matchmaking/join` response shape를 유지한 채 WebSocket state message를 추가했습니다. `SL-81` Stack 3은 transport credential을 위해 `sessionToken`과 tokenized `webSocketPath`를 발급합니다. REST polling이나 SSE는 늘리지 않습니다.

## Room cleanup

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 5분
- hard room lifetime: 1시간
- connected client가 있으면 idle/all-disconnected TTL cleanup을 막습니다.

Store는 30초 janitor ticker 하나로 registry snapshot을 짧게 얻은 뒤 room별 expiry를 검사합니다. Gameplay tick, 일반 GET, input 경로는 전체 registry cleanup을 수행하지 않습니다. Debug create와 matchmaking create가 active room cap에 닿았을 때만 즉시 cleanup을 정확히 한 번 수행하고 생성도 한 번만 재시도합니다. 아직 만료되지 않은 room만 남으면 `409 room_cap_reached`를 유지합니다.

현재 WebSocket close는 connection과 pending input을 제거합니다. matchmaking start 전 close는 match cancel로 처리해 room과 남은 connection을 정리합니다. started room에서 GameEnd가 발생하면 결과 event를 보낸 뒤 room과 연결을 정리합니다.

디버그 테스트 중 즉시 비워야 하면 REST debug API를 명시적으로 활성화하고 Bearer credential로 호출합니다. 기본 상태에서는 debug route와 method fallback이 `404 not_found`이며, 활성화 후 missing/wrong/multiple credential은 route result보다 먼저 `401 unauthorized`입니다. WebSocket GET은 debug Bearer 예외입니다.

```text
DELETE /rooms
DELETE /rooms/{roomID}
```

삭제는 in-memory room, room-local ticker, WebSocket connection을 정리합니다. Persistence는 아직 없으므로 DB 삭제는 없습니다.

## Duel 2인 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 secret-bearing `webSocketPath`로 연결하되 raw path/query를 log에 남기지 않습니다.
3. 두 connection이 같은 `Type: "Ready"` event를 받고, 이 event의 `Map.map` row가 숫자 배열이어야 합니다.
4. 두 client가 `{"Type":"ready"}`를 보내면 `starting` 신호를 1번 받고, 중간 countdown broadcast 없이 5초 뒤 `started`를 받아야 합니다.
5. 한 player가 movement input을 보내면 두 connection이 같은 `Snapshot.Tick`과 player `Pos`를 받아야 합니다.
6. Red와 blue spawn은 Ready event의 `Players[].SpawnPosition`으로 확인합니다.
7. Hit tick에서 projectile은 `IsDestroyed: true`, target은 HP 감소로 보여야 합니다.
8. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
9. HP가 0이 된 tick의 snapshot 이후 player별 `GameEnd`를 받아야 합니다.
10. `GameEnd` 이후 해당 room은 정리되어야 합니다.
11. invalid JSON 이후에도 다음 snapshot stream은 유지되어야 합니다.

## Solo/Team 6인 검증 시나리오

1. 같은 `gameMode`로 `POST /matchmaking/join`을 6번 호출하고 여섯 응답의 `room.id`와 `gameMode`가 같은지 확인합니다.
2. 여섯 secret-bearing `webSocketPath`로 서로 다른 WebSocket connection을 열고 raw token/query를 log에 남기지 않습니다.
3. 다섯 connection만 attach된 동안 internal `matchStatus`는 `matched`, public `room.status`는 `waiting`이며 Ready event와 countdown은 시작하지 않습니다.
4. 여섯 번째 connection이 attach되면 여섯 connection 모두 같은 `Ready` event를 받습니다.
5. `solo`는 `solo-1/0`부터 `solo-6/0`, `team`은 `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2`를 확인합니다.
6. Ready의 six spawn이 room-local `PlayerAssignments`와 같고 fallback 사용 시 Wall/Water가 아니며 서로 다른지 확인합니다. Ground와 Bush는 passable candidate입니다.
7. 서로 다른 다섯 player만 ready ACK를 보내면 `loading`을 유지하고 countdown ticker가 없어야 합니다.
8. 이미 ready인 player가 ACK를 반복해도 quorum은 5로 유지됩니다.
9. 여섯 번째 서로 다른 player가 ACK하면 `starting/countdown: 5`를 connection당 한 번 받고 countdown ticker는 하나여야 합니다.
10. 5초 뒤 `started`를 connection당 한 번 받고 gameplay ticker는 하나여야 합니다.
11. 첫 gameplay tick은 `Tick: 1`, `Players` 길이 6이고 여섯 connection에서 같은 payload여야 합니다.

Ready timeout, reconnect grace, reconnect participant replacement, bot fill은 이 흐름에 없습니다. Start 전 실제 disconnect는 기존 pre-start cancel로 room과 남은 connection을 정리합니다.

자동 검증은 `go test ./internal/rooms`와 `go test ./internal/simulation`이 담당합니다.

## 문서 위치

- REST: `api/openapi.yaml`
- WebSocket: `api/asyncapi.yaml`
- 사람이 읽는 API: `ai-docs/api-reference.md`
- 문서화 기준: `ai-docs/api-docs.md`

후속 protocol message는 Linear issue에서 scope와 acceptance criteria를 먼저 정한 뒤 구현합니다.
