# 프로젝트 맵

이 문서는 `Server-CrawlStars`를 다시 볼 때 가장 먼저 읽는 지도입니다. 자세한 계약은 `api/`, `ai-docs/protocol.md`, `ai-docs/api-reference.md`를 따릅니다.

## 한 줄 요약

클라이언트는 `POST /matchmaking/join`의 optional `gameMode`로 `duel_1v1`, `solo`, `team`을 고르고, 같은 mode의 `room`, human `player`, `sessionToken`, tokenized `webSocketPath`를 받습니다. 서버는 optional human `ClientTick`과 sessionless bot command를 공통 `InputCommand`로 합쳐 room-local selected config의 tick에서 `State.Step(inputs) -> Snapshot`을 정확히 한 번 실행하고, player별 processed input ACK와 Solo 마지막 생존자·Team elimination을 authoritative snapshot으로 판정합니다.

## 현재 상태

SL-82 config v2 catalog는 stable `characterType` `0=Shelly`, `1=Colt`, `2=Lily`이고 server HP는 `4000/3100/4100`입니다. Join의 lower-camel field는 migration 동안 optional이며, canonical participant가 Ready/Snapshot의 PascalCase `CharacterType`까지 값을 보존합니다. SL-83 일반 공격은 server config v3의 player type별 `normalAttack`과 production `State.Step`이 소유합니다.

되는 것:

- health check와 server-hosted API docs
- optional mode 선택과 same-mode waiting pool을 지원하는 matchmaking join
- room/player debug REST API
- debug room 전체/개별 삭제
- room/player WebSocket 연결
- SL-79 client `Map_0`과 Ground/Wall/SpawnPoint/Bush/Water tile 계약
- server-side movement/attack direction 검증과 player Wall/Water/boundary collision
- 캐릭터별 `3/3/2 charge`, 30 tick recharge
- Shelly 5-shot spread, Colt 6-shot scheduled burst, Lily 2.2 tile centerline melee
- projectile 생성·이동·configured range, Wall/boundary destroy와 Bush/Water 통과
- selected mode rules를 따르는 projectile hit, 결정적 target 선택, HP 감소, `IsDead` snapshot
- dead player의 같은 tick input 차단
- `duel_1v1` 2명과 `solo`/`team` 6명의 mode별 matchmaking pool
- Duel 2 WebSocket, 2 Ready ACK, 1회 countdown/start regression
- Solo/Team 6 WebSocket, 6 human Ready ACK, 1회 countdown/start regression
- SL-90 internal server-owned bot participant와 credential/ACK 없는 lifecycle
- SL-91 첫 human join 기준 10초 room-owned bot fill, timer/human join first-lock-wins, failure rollback/no-retry
- Full participant capacity 뒤 human-only attach/ACK, bot을 포함한 Ready/Snapshot
- 직전 snapshot 기반 결정적 basic controller와 human/bot input의 shared one-Step 처리
- optional `ClientTick`, positive stale/duplicate silent drop, legacy zero last-write-wins
- gameplay `PlayerData.LastProcessedClientTick`의 player별 monotonic processed input ACK와 bot ACK `0`
- room-local mode config 기반 team/slot/spawn과 Wall/Water-safe fallback assignment
- unmatched 연결 종료 시 deadline/credential 유지, matched/loading/starting WebSocket close cancel
- `duel_1v1` 호환, Solo 중간 Lose/마지막 생존자, Team elimination을 따르는 GameEnd Win/Lose/Draw
- Player별 immutable 결과와 terminal close barrier 뒤 room/player ID cleanup
- client build용 shared game config artifact
- room 책임별 파일 분리와 `ServeMux` pattern routing
- random opaque room/player ID와 player session WebSocket 인증
- 기본 비활성화된 debug REST Bearer guard
- matchmaking join IP별 token-bucket rate limit과 trusted proxy 경계
- room별 lock, client별 async writer, latest-only snapshot coalescing
- reliable Ready/lifecycle/error와 terminal snapshot → GameEnd → close 전달
- 30초 WebSocket heartbeat와 Ping별 90초 deadline
- Store당 30초 janitor와 cap-pressure 단일 cleanup/retry
- JSON room/WebSocket lifecycle log와 process-local Prometheus metrics
- application HTTP와 private metrics의 coordinated graceful shutdown

아직 안 되는 것:

- bot replacement와 별도 reconnect grace
- pathfinding, 회피, 시야 판정 같은 advanced bot AI
- respawn, score
- production matchmaking queue, rating, account auth, persistence

## 레포 구조

```text
cmd/server
  main.go                  application/metrics 이중 HTTP server와 process lifecycle

internal/health            /health

internal/docs              OpenAPI/AsyncAPI raw spec과 docs UI embed

internal/observability     process-local Prometheus registry와 handler

internal/rooms
  handler.go       ServeMux REST/WebSocket route와 JSON fallback
  store.go         room/player/match lifecycle
  websocket.go     connection, ClientTick admission, tick, snapshot delivery
  messages.go      REST/WebSocket DTO 변환
  cleanup.go       in-memory TTL, GameEnd close barrier, Shutdown teardown
  rate_limit.go    client IP 해석과 matchmaking token bucket
  rooms.go         공통 status, timeout, clock/ticker
  errors.go        lifecycle sentinel error
  game_end.go      GameEnd 결과 계산
  bot.go           직전 snapshot 기반 pure targeting/InputCommand 생성과 bot-key input merge

internal/simulation
  transport를 모르는 gameplay core
  State.Step(inputs) -> Snapshot
  server runtime game config와 mode/team/spawn assignment model
  map, input 검증과 PlayerID 정렬, processed input ACK, movement, projectile, attack charge, mode별 hit, HP/death rule

api
  openapi.yaml
  asyncapi.yaml

docs-ui                   docs validation/build scripts
scripts/deploy            pinned release/checksum VM pull과 no-network 회귀 테스트
ai-docs                   사람이 읽는 운영/설계 문서
```

## 요청 흐름

### 1. 서버 시작

`cmd/server/main.go`는 application 하나에서 `rooms.Store`와 `observability.Metrics`를 각각 하나씩 만듭니다. `SERVER_ADDR` 기본값은 `127.0.0.1:8080`, `METRICS_ADDR` 기본값은 `127.0.0.1:9090`입니다. Metrics 주소는 loopback IP literal만 허용합니다. Application listener에는 `/health`, docs route, `/matchmaking/join`, `/rooms`, `/rooms/`를 mount하고, private listener에는 정확한 `GET /metrics`만 mount합니다. 두 listener를 모두 먼저 bind한 뒤 serve를 시작합니다. Active room cap은 5개입니다. Debug REST는 기본 비활성화이며, rate/burst/trusted proxy 환경 변수는 시작할 때 검증합니다.

Process와 HTTP server error는 JSON `slog`로 stdout에 기록합니다. SIGINT/SIGTERM이나 어느 한 HTTP server 종료는 Store와 두 HTTP server의 coordinated shutdown을 시작합니다. Process 내부 grace는 10초이고 systemd `TimeoutStopSec`는 15초입니다.

### 2. 매칭 요청

`POST /matchmaking/join`은 production queue가 아니라 단순 connector입니다.

1. Client IP를 resolve하고 token-bucket quota를 평가합니다. 허용 요청은 여기서 quota를 소비합니다.
2. Optional request body의 `gameMode`를 catalog의 canonical config로 선택합니다. Body 없음, 빈 object, 빈 문자열은 default `duel_1v1`입니다.
3. 같은 selected mode의 여유 waiting room 탐색과 없을 때의 생성을 하나의 serialized find-or-create transition으로 처리합니다.
4. 새 room은 selected config를 소유합니다. Cap에 닿았을 때만 만료 room을 한 번 즉시 정리하고 생성도 한 번 재시도합니다.
5. player와 session token을 발급합니다.
6. 첫 human의 `0 -> 1` 전이에서만 room-owned 10초 deadline을 시작합니다. 후속 join과 partial manual bot 추가는 reset하지 않습니다.
7. deadline과 human join은 matchmaking lock을 먼저 얻은 transition이 이깁니다. Timer-first fill 뒤 late join은 다른 waiting room으로 가며 cap이면 기존 `room_cap_reached` 409를 받습니다.
8. Human과 internal bot을 합친 participant가 room-local mode capacity를 채우면 matchmaking room으로 잠그고 late join을 막습니다.
9. top-level `gameMode`, 같은 값의 nested `room.gameMode`, `player`, `sessionToken`, tokenized `webSocketPath`를 반환합니다.

Server runtime config는 default `duel_1v1`과 `solo`, `team` catalog를 가집니다. Duel은 2명, solo와 team은 6명이며 `map.maxPlayers = 6`과 REST `room.maxPlayers`는 별도의 map/debug room capacity로 유지합니다.

Room/player ID는 16 random bytes 기반 opaque value이고 player session token은 32 random bytes 기반 43자 value입니다. Private room state에는 token SHA-256 digest만 저장합니다. Team/slot은 room-local selected config에서 발급합니다. Duel은 `red/0, blue/0`, solo는 `solo-1/0`부터 `solo-6/0`, team은 red/blue slot 0부터 2까지 교대로 배정합니다.

Join quota는 store보다 먼저 실행하므로 429가 room cap 409와 `internal_error` 500보다 우선하고, 허용된 409/500 요청도 quota를 소비합니다. Default는 process-local per-IP 10 requests/minute, burst 4입니다. `CF-Connecting-IP`는 immediate peer가 trusted CIDR이고 header가 정확히 하나의 valid IP일 때만 사용하며 `X-Forwarded-For`는 무시합니다.

Participant capacity를 채워도 REST `room.status`는 `waiting`입니다. 그 뒤 room 내 human participant의 WebSocket session이 모두 연결되면 human connection에만 같은 `Ready` event를 보내며, payload에는 bot을 포함한 full participant list를 넣습니다. Human participant의 ready ACK가 모두 모이면 `starting/countdown: 5`를 한 번 broadcast합니다. Duplicate ACK는 player identity별 quorum을 늘리지 않고 bot은 session이나 ACK가 없습니다. Human participant가 0명이면 quorum은 성립하지 않습니다. 5초 뒤 `started`를 한 번 보내고 room-local gameplay ticker 하나를 시작합니다.

SL-91 timer는 10초 deadline에 selected mode의 남은 capacity를 bot으로 원자적으로 채웁니다. Bot ID 발급이 하나라도 실패하면 participant와 ID registry를 이전 상태로 돌리고 `bot_fill_failed`를 한 번 기록하며 retry하지 않습니다. Public REST endpoint나 bot credential은 없습니다. Unmatched disconnect는 credential과 timer를 유지하고, matched/loading/starting 실제 disconnect는 기존 match cancel로 room과 timer resource를 정리합니다. Ready timeout, reconnect grace, participant replacement는 없습니다.

첫 번째 player만 있는 waiting room은 WebSocket input을 받을 수 있지만 gameplay snapshot을 broadcast하지 않습니다. 1명으로 수동 검증하려면 `POST /rooms/{roomID}/start`를 호출합니다.

`room_cap_reached` 409가 나면 debug 환경에서는 API를 명시적으로 활성화하고 Bearer credential로 `DELETE /rooms` 또는 `DELETE /rooms/{roomID}`를 호출합니다. Debug guard는 disabled 404 → enabled unauthenticated 401 → authenticated route result 순서입니다. 일반 room 삭제는 room-local ticker와 WebSocket connection도 닫지만 GameEnd ending room은 건드리지 않습니다.

### 3. WebSocket 연결

Client는 다음 path에 연결합니다.

```text
WS /rooms/{roomID}/players/{playerID}?token=<player-session-token>
```

서버는 upgrade 전에 room 존재 여부, player 소속 여부, 정확히 한 개의 non-empty token, live connection/in-flight reservation을 순서대로 확인합니다. 실패 status는 404, 404, 401, 409입니다. 정상 extra query key는 허용하지만 malformed query pair는 401입니다. Waiting room도 연결과 input 수신은 허용하지만, started 전 gameplay tick은 돌리지 않습니다.

Token은 room/player session이 존재하는 동안 재사용할 수 있습니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 pre-start cancel로 room을 삭제합니다. Started room은 TTL/hard lifetime을 따릅니다. 같은 started match에 reconnect하면 snapshot의 processed input ACK 다음 양수 tick부터 이어 보내고 새 match는 `0`에서 시작합니다. Failed upgrade는 room을 취소하지 않아 같은 발급 path로 retry할 수 있습니다. `sessionToken`, tokenized `webSocketPath`, inbound query를 log에 남기지 않습니다.

Matchmaking room WebSocket 상태:

1. Human과 bot을 합친 participant가 selected mode capacity 2명 또는 6명을 채웁니다.
2. Room 내 human participant의 current WebSocket session이 모두 연결되면 human connection만 같은 `{"Type":"Ready","Map":...,"Players":[...]}`를 받습니다. `Players`에는 bot을 포함한 full participant가 들어갑니다.
3. Human client는 이 map과 `Players[].SpawnPosition`으로 렌더 준비를 끝낸 뒤 `{"Type":"ready"}`를 보냅니다. Bot은 WebSocket sender가 아닙니다.
4. Room 내 human player identity가 모두 ready가 되면 `Snapshot.status: "starting"`과 `Snapshot.countdown: 5`가 human connection당 1번 broadcast됩니다. Duplicate ACK는 idempotent합니다.
5. 중간 countdown broadcast 없이 5초 뒤 `Snapshot.status: "started"`가 옵니다. `starting`과 이 `started` control은 `Tick: 0`, `Players: null`이고, 다음 gameplay `Tick: 1`부터 모든 player에 processed input ACK가 있습니다.
6. Unmatched human WebSocket close는 deadline과 credential을 유지합니다. matched/loading/starting close는 match cancel로 처리하며 room과 남은 connection을 정리합니다.

### 4. Room start

Room은 두 경로로 시작합니다.

- debug: 기본 비활성화된 API를 활성화하고 Bearer credential을 붙여 `POST /rooms/{roomID}/start`
- matchmaking: full participant capacity 뒤 room 내 human WebSocket session이 각각 Ready ACK를 보내고 server 내부 countdown이 끝난 시점

Start 시점에는 room이 고정해서 소유한 `gameConfig`로 `simulation.NewStateWithConfig` state를 만들고 같은 config의 tick rate로 ticker를 시작합니다. 현재 catalog는 모두 30Hz입니다. 이 loop는 room-local gameplay loop이지 범용 scheduler나 runner가 아닙니다.

### 5. Input 수집

WebSocket input:

```json
{
  "ClientTick": 12,
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": true
}
```

`ClientTick`은 optional `int64`이며 누락/`0`은 legacy input입니다. Room은 직전 processed ACK와 positive pending보다 큰 양수 command만 저장합니다. Stale/duplicate 양수는 error 없이 무시하고, legacy `0`은 기존 last-write-wins로 positive pending도 덮을 수 있지만 ACK를 변경하지 않습니다. 음수는 `invalid_input`이고 기존 pending을 보존합니다.

Pending map key가 authoritative `PlayerID`라 payload에 섞인 ID를 신뢰하지 않습니다. Bot key로 들어온 외부 input은 버리고 `ClientTick: 0`인 pure controller 결과로 대체합니다. Human command의 `ClientTick`은 보존한 채 human/bot command를 `PlayerID` 오름차순으로 합쳐 같은 `State.Step`에 전달합니다. 잘못된 JSON과 음수 tick은 `invalid_input` error message를 보내고 snapshot stream은 유지합니다.

### 6. Tick 처리

Started room의 tick 흐름:

1. `room.mu`가 보호하는 직전 authoritative `lastPlayers` snapshot과 player별 processed ACK를 읽습니다.
2. Pure bot controller가 nearest live enemy를 골라 공통 `InputCommand`를 만듭니다. 같은 거리는 `PlayerID`, 같은 좌표 방향은 `+X`로 결정합니다.
3. Applied ACK와 positive pending을 통과한 human command의 `ClientTick`을 보존하고 bot key input을 tick 0 controller 결과로 대체한 뒤 `PlayerID` 오름차순으로 정렬합니다.
4. Pending map을 비우고 `room.state.Step(inputs)`를 정확히 한 번 호출합니다.
5. Simulation이 처리한 `LastProcessedClientTick`을 포함한 반환 snapshot을 다음 tick의 `lastPlayers`로 복사하고 Room REST detail/list의 `latestSnapshot` summary를 바로 갱신합니다.
6. Room-local mode helper로 새 player 결과와 terminal 여부를 계산하고, player별 첫 결과만 immutable ledger에 확정합니다.
7. Room terminal이면 `ending`을 예약하고 ticker를 terminal decision 즉시 중단합니다. Tick observer, encode, enqueue는 ticker stop 뒤에 실행합니다.
8. `{"Type":"snapshot","Snapshot":...}`을 tick당 한 번 marshal합니다. Human survivor는 latest-only slot, 새로 결과가 확정된 human은 `terminal snapshot -> GameEnd -> close` handoff를 받습니다. Bot도 ledger 결과는 계산하지만 transport session은 없습니다.
9. Solo 중간 탈락이면 loser human session만 닫고 survivor room tick은 계속합니다. Room terminal이면 모든 captured human close가 끝난 뒤 normal cleanup을 실행합니다.

Bot controller는 이동이나 피해를 직접 계산하지 않습니다. Movement, projectile, hit, HP/death, attack charge와 processed input ACK는 모두 `internal/simulation.State.Step`이 계속 소유합니다. Receipt나 pending 저장만으로 ACK하지 않습니다.

기본 runtime map은 client SL-79에서 merge된 `Map_0`과 값이 같은 20x20 grid입니다. Player spawn은 map의 `TileSpawnPoint(2)`를 join 순서대로 사용합니다. SpawnPoint가 부족하면 player blocking policy를 재사용해 Wall/Water를 제외한 fallback candidate를 쓰며 Ground/Bush는 유지합니다. Map config는 명시적 SpawnPoint와 passable fallback의 고유 좌표가 `map.maxPlayers` 이상이어야 하므로 정상 room의 spawn은 겹치지 않습니다.

`internal/simulation.State.Step` 순서:

1. `PressedAttack` transient state 초기화와 attack charge recharge 진행
2. 기존 projectile 이동
3. projectile을 configured range endpoint까지 clamp해 이동하고 Wall/boundary 충돌, selected mode별 hit, range 만료 순서로 처리
4. 현재 tick의 Colt scheduled emission을 수집
5. input을 `PlayerID` 오름차순으로 stable sort하고 live player, 유한한 방향, non-negative/stale `ClientTick`을 검증
6. 유효한 양수 input의 processed ACK를 visible effect 판정보다 먼저 갱신하고 legacy 0은 ACK 유지
7. `MoveDir` clamp와 `AttackDir` 정규화 뒤 X축, Y축 순서로 player의 Wall/Water/boundary collision 검사
8. 공격 요청, non-zero 방향, 캐릭터별 남은 charge가 유효하면 projectile emission 또는 Lily melee intent 승인
9. Lily same-tick batched damage 적용 후 projectile을 owner ID/ordinal 순서로 생성
10. tick 증가와 ACK/HP/death/projectile snapshot clone 반환

새 projectile은 생성된 tick에는 owner 위치에 보이고 다음 tick부터 이동합니다.

Shelly는 activation tick에 `-12,-6,0,6,12`도 5발을 동시에 생성합니다. Colt는 activation tick `A` 기준 `A+[0,6,12,18,24,30]`에 발사하고 마지막 emission과 새 activation을 겹치지 않아 `A+31`부터 재공격할 수 있습니다. Lily는 wall/boundary까지 잘린 2.2 tile centerline의 첫 eligible target을 고르고 모든 melee intent를 입력 전 player snapshot에 대해 계산한 뒤 same-tick batched damage를 적용합니다.

이 실행기는 기존 `PressedAttack`, `Damage`, `Type`, HP/death snapshot과 room GameEnd 계산기를 재사용합니다. 새 wire field, client parser는 아직 범위 밖이고 final balancing도 후속 작업입니다.

Projectile hit은 owner와 이미 사망한 player를 제외합니다. Solo는 나머지 live player를 모두 적으로 보고, 현재 `friendlyFire=false`인 Team/Duel은 ally를 통과해 enemy만 hit합니다. 여러 eligible target이 겹치면 join/배정 순서의 첫 target만 피해를 받습니다. 이 target tie-break는 input의 `PlayerID` 정렬과 별개입니다.

### 7. Snapshot 필드 의미

`AttackDir`와 `PressedAttack`은 분리합니다.

- input `AttackDir`: 현재 조준 방향이며, 서버가 non-zero 유한 벡터를 unit vector로 정규화합니다.
- input `PressedAttack`: 이번 tick의 캐릭터 일반 공격 activation 요청
- snapshot `PressedAttack`: 서버가 방향과 캐릭터별 charge를 검증해 이번 tick activation을 승인했는지 나타내는 transient 결과

`AttackDir != zero`만으로 발사를 추론하면 조준 방향을 유지하는 동안 매 tick 발사될 수 있습니다. 그래서 input의 `PressedAttack`은 유지합니다.

`MoveDir`은 크기 `1` 이하의 아날로그 입력을 보존하고 그보다 큰 값만 unit vector로 clamp합니다. Shelly/Colt/Lily는 server-only `3/3/2` charge로 시작하고 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. Dead player input은 position, direction, projectile/melee 결과를 바꾸지 않습니다.

`LastProcessedClientTick`은 WebSocket 수신이나 pending 저장이 아니라 simulation이 실제 처리한 마지막 양수 `ClientTick`입니다. Player별로 단조 증가하고 input이 없는 gameplay tick에도 유지됩니다. Wall 충돌, zero attack 방향, charge 소진처럼 visible effect가 없는 유효 input도 ACK하지만 unknown/dead/non-finite/negative/stale input은 ACK하지 않습니다. Bot ACK는 `0`입니다. Match 시작용 Ready ACK와 이 processed input ACK는 별개입니다.

`IsDead`는 `HP <= 0`에서 유도할 수 있지만 snapshot에 명시합니다. Client가 death rule을 재해석하지 않아도 되고, 나중에 respawn, down, invulnerable 같은 상태로 확장하기 쉽습니다.

Attack charge와 recharge 진행도는 `simulation.State` 내부에만 있습니다. `PressedAttack` 의미는 승인 결과로 유지하고, SL-94가 추가한 public field는 optional input `ClientTick`과 required gameplay `LastProcessedClientTick`뿐입니다.

Room REST response와 Ready event의 `map`은 서버 simulation이 쓰는 `MapData`입니다. Tile은 `0=Ground`, `1=Wall`, `2=SpawnPoint`, `3=Bush`, `4=Water`이고 `map` row는 Base64 문자열이 아니라 JSON number array로 직렬화합니다. Player는 Wall/Water, projectile은 Wall에 충돌하며 map boundary는 둘 다 막습니다. Bush는 둘 다 통과하고 projectile은 Water도 통과합니다.

Gameplay config는 두 파일로 나눕니다. `client-config/game-config.json`은 Unity CI가 서버 repo의 `client-config`만 sparse checkout한 뒤 `Assets/StreamingAssets/GameConfig` 같은 client runtime 경로로 복사하는 공유 config입니다. legacy `playerTypes: ["default"]` mirror와 v2 `characters` catalog (`0/1/2 = shelly/colt/lily`)를 함께 제공합니다. `server-config/game-config.json` v3는 server binary가 embed해서 room store와 simulation 기본값으로 쓰는 canonical runtime config이며 speed `2`, radius `0.5`, HP `4000/3100/4100`, player type별 `normalAttack`, tickRate, `mode.default`와 `mode.catalog`, map을 담습니다. Store는 catalog/default source이고 각 room은 생성 때 선택한 config를 immutable하게 소유합니다. Runtime map은 client SL-79 `Map_0`의 exact grid를 값 기준으로 복사하고 Go regression으로 drift를 막습니다. Client는 최종 gameplay state를 여전히 서버 snapshot에서 받습니다.

### 8. Cleanup

Room store는 in-memory입니다.

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 5분
- hard room lifetime: 1시간
- connected client가 있으면 idle/all-disconnected cleanup을 막습니다.

현재 WebSocket close는 client connection과 pending input을 제거합니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 match cancel로 room을 제거합니다. Started room에서 모든 client가 나가면 disconnected TTL을 시작합니다.

각 connection은 snapshot fanout과 독립적인 30초 heartbeat를 실행하고 Ping마다 90초 deadline을 사용합니다. 실패는 read/write failure와 같은 close-once 경로로 현재 session만 해제합니다. Attach된 session generation은 `room.mu`가 보호하는 close barrier set에도 등록되며 lifecycle monitor가 transport `closeDone` 뒤 제거합니다. Store당 하나의 30초 janitor가 TTL을 검사하고, cap-pressure create/matchmaking만 cleanup/retry를 한 번 즉시 수행합니다.

외부 mutation의 lock 순서는 `mutationMu -> matchmakingMu -> Store.mu -> room.mu`입니다. `matchmakingMu`는 같은 mode의 동시 첫 join이 여러 room을 만들지 않도록 find-or-create 전체를 직렬화합니다. Logger와 Observer callback은 core lock을 놓은 뒤 동기 실행하는 bounded pure sink라서 Store method나 publication을 다시 호출하면 안 됩니다. Mutation 함수가 반환되면 그 transition의 log와 metrics publication도 끝난 상태입니다.

각 terminal session의 connected-client observer는 session close callback에서 반영되어 transport `closeDone`보다 먼저일 수 있습니다. Normal GameEnd cleanup은 current terminal session, 앞서 결과가 확정되어 기억한 session, reconnect 전에 current map에서 빠졌지만 아직 close가 끝나지 않은 historical session generation의 `closeDone`을 모두 기다립니다. Solo prior loser와 ordinary reconnect predecessor 모두 barrier에 남습니다. 그 뒤 registry, active-room observer, player ID, `room_ended` log, resources를 정리하고 cleanup success signal을 마지막에 닫습니다. Hard TTL과 debug clear/delete는 ending room을 제거하지 않습니다.

Shutdown은 새 mutation을 막고 janitor, room ticker, WebSocket writer/heartbeat를 정리합니다. Client에는 `1000 / server shutting down` close를 보냅니다. GameEnd close barrier의 forced-teardown 예외로서 close 전에 registry/player ID를 detach할 수 있고, deadline에는 WebSocket accept에서 캡처한 underlying `net.Conn`을 직접 닫아 진행 중인 graceful close를 중단합니다. 그래도 cleanup worker와 session lifecycle을 join합니다. 이 takeover는 normal cleanup signal을 닫거나 `room_ended`를 기록하지 않으며 최종 active room/client gauge가 0으로 반영될 때까지 기다립니다.

GameEnd wire는 `Type: "GameEnd"`, `PlayerId`, `Result: Win|Lose|Draw` 그대로입니다. `duel_1v1` 결과는 유지하고, Solo 중간 탈락은 Lose 후 survivor가 계속하며 마지막 생존자는 Win입니다. 이전 Lose는 이후 Draw로 덮지 않습니다. Team 일부 사망은 계속하고 한 team 전멸은 3 Lose/3 Win, 양 team 같은 tick 전멸은 6 Draw입니다.

## Linear 흐름

완료된 큰 흐름:

- `SL-38`: simulation `Step(inputs) -> Snapshot`
- `SL-39`: map, movement, wall collision
- `SL-40`: attack/projectile skeleton
- `SL-41`: room REST debug lifecycle
- `SL-42`: WebSocket snapshot broadcast
- `SL-43`: room TTL cleanup과 invalid input regression
- `SL-47`, `SL-51`, `SL-52`: API docs hosting/build
- `SL-49`: simple `/matchmaking/join`
- `SL-53`: projectile movement와 wall collision
- `SL-54`: hit, HP, death snapshot
- `SL-55`: 2-player WebSocket sync regression
- `SL-56`: protocol validation docs

현재 E2 흐름:

- `SL-12`: user matchmaking parent
  - `SL-49`: server simple matchmaking
  - `SL-50`: client matchmaking
- `SL-58`: server ready/loading/starting signal/cancel
- `SL-14`: client prototype logic server migration
  - server child issues `SL-53` to `SL-56`
  - `SL-57`: client logic split
- `SL-30`: shared constants/data management
- `SL-81` Stack 1: simulation input 무결성과 server-authoritative attack charge
- `SL-81` Stack 2: rooms 책임 분리, typed error, `ServeMux` pattern routing
- `SL-81` Stack 3: opaque ID/session token, debug guard, matchmaking rate limit, trusted proxy 경계
- `SL-81` Stack 4: room/client 동시성, janitor, snapshot coalescing, reliable terminal delivery, heartbeat
- `SL-81` Stack 5: JSON lifecycle log, private Prometheus metrics, coordinated graceful shutdown, HTTP timeout
- `SL-81` Stack 6: latest 1회 tag 고정, 안전한 asset 이름, checksum 선검증, 배포 회귀 테스트
- `SL-86`: duel/solo/team mode catalog, same-mode waiting pool, room-local selected config, REST `gameMode`
- `SL-87`: mode별 2/6-player Ready quorum, 6 human ACK, single countdown/start, safe spawn
- `SL-88`: room-local mode rules 기반 projectile eligibility와 결정적 target/input 순서
- `SL-89`: mode별 GameEnd, immutable result ledger, terminal close barrier와 Shutdown 예외
- `SL-90`: internal bot participant, 결정적 basic controller, human-only Ready quorum, shared one-Step integration
- `SL-91`: first-lock-wins 10초 automatic bot fill, human-only Ready quorum, lifecycle cleanup
- `SL-94`: optional ClientTick, monotonic processed input ACK, legacy zero compatibility, stale/duplicate silent drop
- `SL-82`: config v2 CharacterType `0/1/2` join-to-Ready/Snapshot contract and docs drift validation

각 issue의 최신 상태는 Linear를 확인합니다. 이 문서는 상태판이 아니라 흐름 복구용 지도입니다.

## 다음 추천 작업

1. `SL-98`: CharacterType request required 전환
   - SL-82 legacy missing Shelly fallback/warning을 제거하기 전 client rollout을 확인
   - stable IDs `0=Shelly`, `1=Colt`, `2=Lily`와 REST lower camel/WebSocket PascalCase는 유지

2. `SL-14` closeout
   - `SL-57` client PR 상태 확인
   - server/client acceptance criteria가 모두 닫히면 parent issue 정리

## 자주 쓰는 명령

```sh
make docs-build
make ci
make deploy-test
go test ./internal/simulation
go test ./internal/rooms
go run ./cmd/server
curl http://127.0.0.1:9090/metrics
```

## 다음에 읽을 문서

- `ai-docs/workflow.md`: 작업 방식
- `ai-docs/architecture.md`: package와 runtime 책임
- `ai-docs/protocol.md`: protocol 경계
- `ai-docs/api-reference.md`: API shape
- `ai-docs/decisions.md`: 왜 이렇게 정했는지
