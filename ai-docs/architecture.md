# 아키텍처

서버는 아직 작게 유지합니다. E1 core loop는 들어왔고, 현재는 E2 client-server integration을 위해 필요한 표면만 issue 단위로 추가합니다.

## Package 책임

```text
cmd/server
  process entrypoint와 route wiring

internal/health
  /health model과 handler

internal/docs
  OpenAPI/AsyncAPI raw spec과 docs UI embed

internal/observability
  process-local Prometheus registry와 metrics handler

internal/rooms
  handler.go       ServeMux pattern과 JSON fallback
  store.go         in-memory room/player/match lifecycle
  websocket.go     connection, ClientTick admission, 30Hz tick/delivery
  messages.go      REST/WebSocket DTO와 변환
  cleanup.go       TTL, GameEnd close barrier, Shutdown forced teardown
  rate_limit.go    matchmaking client IP와 token bucket
  rooms.go         status, timeout, clock/ticker adapter
  errors.go        lifecycle sentinel error
  game_end.go      GameEnd 결과 계산
  bot.go           직전 snapshot 기반 pure targeting과 InputCommand merge

internal/simulation
  transport-independent gameplay core
  State.Step(inputs) -> Snapshot
  server runtime game config와 mode/team/spawn assignment model
  map, input 검증과 processed ACK, movement, collision, 캐릭터별 normal attack, projectile/melee, HP/death rule
  default map fixture loader
```

`internal/simulation`은 HTTP, WebSocket, room lifecycle, matchmaking을 모릅니다. `internal/rooms`가 REST/WebSocket transport와 room state를 맡고, tick마다 simulation을 호출합니다.

## Runtime

```text
GitHub Actions
  -> linux/amd64 binary package
  -> GitHub Release asset

Oracle VM
  -> latest를 commit SHA 기반 tag로 1회 해석
  -> 같은 tag의 package + SHA256SUMS pull
  -> checksum 검증 뒤에만 package 추출
  -> /opt/crawl-stars-server/releases/<sha>
  -> current symlink 전환
  -> systemd restart

Cloudflare Tunnel
  -> api-crawlstars.tolerblanc.com -> 127.0.0.1:8080
  -> tolerblanc.com                -> 127.0.0.1:8081 Caddy hello

Go server process
  -> application HTTP  127.0.0.1:8080
  -> private metrics   127.0.0.1:9090
```

Go server는 production에서도 application HTTP를 `127.0.0.1:8080`, metrics를 `127.0.0.1:9090`에 bind합니다. Public HTTPS edge는 Cloudflare Tunnel이며 metrics listener는 tunnel이나 public firewall에 연결하지 않습니다. Caddy는 apex hello page용 local service입니다. Rate limiter가 public client IP를 쓰려면 loopback cloudflared peer를 `TRUSTED_PROXY_CIDRS`로 명시해야 하며, `X-Forwarded-For`는 신뢰하지 않습니다.

VM pull deployment는 `latest` redirect를 각 asset마다 따라가지 않습니다. 시작 시 GitHub API 응답의 non-`latest` tag를 한 번 고정하고 package와 `SHA256SUMS`를 같은 tag에서 받은 뒤, 요청 asset과 정확히 일치하는 checksum record를 검증해야만 압축 해제와 systemd restart로 넘어갑니다. `ASSET_NAME`은 안전한 basename 문자만 허용해 root 실행 시 임시 디렉터리 밖 경로를 덮어쓰지 못하게 합니다.

## Application과 observability 경계

`cmd/server`의 application 하나가 `rooms.Store` 하나, process-local `observability.Metrics` 하나, HTTP server 두 개를 함께 소유합니다. Application listener와 metrics listener를 모두 먼저 bind한 뒤에만 serve를 시작하므로, 한쪽 bind 실패로 반쪽짜리 process가 남지 않습니다. Metrics listener의 `METRICS_ADDR`는 `127.0.0.0/8` 또는 `::1`의 IP literal과 숫자 port만 허용하며 hostname, wildcard, private/Tailscale IP를 거부합니다.

```text
SIGINT/SIGTERM 또는 어느 한 HTTP server 종료
  -> 새 application mutation 차단
  -> rooms.Store + application HTTP + metrics HTTP 병렬 shutdown
  -> 최대 10초 graceful drain
  -> 남은 HTTP transport 강제 close
```

Systemd의 `TimeoutStopSec=15s` 안에서 application 자체 10초 grace를 사용합니다. `rooms.Store.Shutdown`은 janitor와 room ticker를 멈추고, WebSocket에 `1000 / server shutting down` close를 보낸 뒤 writer와 heartbeat까지 join합니다. 이미 GameEnd close barrier에 들어간 room에도 Shutdown은 forced-teardown 예외로 동작합니다. Registry/player ID를 `closeDone` 전에 detach할 수 있지만 GameEnd cleanup worker와 session close/writer/heartbeat/lifecycle은 모두 join합니다. Deadline에는 WebSocket accept 때 캡처한 underlying `net.Conn`을 직접 닫아 이미 진행 중인 graceful close도 중단합니다. 이 takeover는 normal GameEnd cleanup signal과 `room_ended` log를 만들지 않습니다. Application HTTP는 `ReadHeaderTimeout=5s`, `IdleTimeout=60s`를 사용합니다. WebSocket과 streaming response를 자르지 않도록 server-wide `WriteTimeout`은 두지 않습니다.

Process log와 HTTP server error log는 stdout의 JSON `slog`로 기록합니다. Process event 이름은 `msg`에, room lifecycle과 WebSocket event 이름은 `event`와 `msg`에 기록합니다. Room/WebSocket log는 `roomID`, 필요한 경우 `playerID`와 bounded category/status만 추가합니다. Logger와 Observer callback은 Store를 다시 호출하지 않는 bounded pure sink입니다. Mutation 함수가 반환되면 해당 transition의 log와 metric publication도 끝난 상태입니다.

Private listener는 정확한 `GET /metrics`에서 다음 process-local Prometheus series만 제공합니다.

- `crawlstars_active_rooms`
- `crawlstars_connected_clients`
- `crawlstars_tick_duration_seconds`

Application HTTP의 `/metrics`와 private listener의 다른 method/path는 노출하지 않습니다. 이 운영 endpoint는 REST/WebSocket product contract가 아니므로 OpenAPI/AsyncAPI에는 포함하지 않습니다.

## Simulation core

현재 계약:

```text
State.Step(inputs []InputCommand) Snapshot
```

Server-owned bot도 별도 simulation을 만들지 않습니다. 한 room tick은 다음 단일 흐름입니다.

```text
직전 authoritative snapshot
  -> applied ACK와 positive pending을 비교하는 human input admission
  -> pure bot controller
  -> ClientTick을 보존한 human pending과 ClientTick 0 bot input의 key-authoritative merge
  -> PlayerID 오름차순 정렬
  -> State.Step 1회
  -> LastProcessedClientTick을 포함한 authoritative snapshot 1개
```

`internal/rooms/bot.go`는 직전 snapshot에서 가장 가까운 live enemy를 고르고 공통 `InputCommand`만 만듭니다. 같은 거리는 `PlayerID` 오름차순, 같은 좌표의 방향은 `+X`로 고정합니다. Pending map의 key가 human command의 authoritative `PlayerID`이며, bot key로 들어온 외부 command는 `ClientTick: 0`인 pure controller 결과로 대체합니다. Room은 stale/duplicate 양수 input을 Step 전에 줄이는 admission guard이고, `internal/simulation.State`가 player별 `LastProcessedClientTick`의 최종 소유자입니다. Movement, projectile, hit, HP/death, attack charge와 processed input ACK는 계속 `internal/simulation.State.Step`만 변경합니다.

### SL-83 일반 공격 소유권

server config v3가 일반 공격 실행의 source of truth입니다. 각 player type의 `normalAttack`이 kind, hit당 damage, tile range, `3/3/2` max charge, 30 tick recharge와 projectile schedule을 소유하고, projectile type catalog는 radius/speed를 소유합니다. Client config v2는 raw-byte compatible identity/render catalog로 유지합니다.

`internal/rooms`는 canonical `CharacterType`, room-local config, human/bot input을 production `State.Step`에 전달하고 authoritative snapshot으로 기존 GameEnd 계산기를 호출합니다. Room은 캐릭터별 피해나 test-only damage branch를 갖지 않습니다. 실제 room regression도 Ready/countdown/spawn 뒤 production input으로 Colt projectile death와 reciprocal 1100-HP Lily Draw를 검증합니다.

`internal/simulation`은 activation을 승인하고 캐릭터별 실행기를 고릅니다. Shelly는 같은 activation tick에 5발 spread, Colt는 `A+[0,6,12,18,24,30]` burst와 `A+31` non-overlap, Lily는 wall/boundary로 자른 2.2 tile centerline의 same-tick batched damage를 수행합니다. 이 책임 분리는 기존 InputMessage, PlayerData, ProjectileData, Snapshot wire shape를 바꾸지 않습니다.

핵심 값:

- `TickRate = 30`
- `TileSize = 1.2`
- character catalog/HP = `0=Shelly/4000`, `1=Colt/3100`, `2=Lily/4100`; speed/radius = `2`, `0.5`
- player normal attack charge/recharge = Shelly `3/30`, Colt `3/30`, Lily `2/30`
- projectile speed/radius = `13`, `0.3`; damage/type은 공격 owner의 `normalAttack`에서 projectile snapshot으로 복사
- default map source = server binary가 embed한 `server-config/game-config.json`의 client SL-79 `Map_0` exact 20x20 grid
- map drift guard = client `Map_0` 값을 고정한 exact-grid Go regression
- config load/validation failure fallback = `StaticGameConfig()`의 5x5 static map, max players `6`
- `internal/simulation/fixtures/default-map.json`은 테스트용 fixture로만 사용
- player spawn = map의 `TileSpawnPoint(2)`를 join 순서대로 먼저 사용하고, 부족하면 Wall/Water를 제외한 fallback candidate 사용. Ground/Bush는 유지하고 config 단계에서 `map.maxPlayers`명분의 고유 좌표를 검증함

Movement:

- `MoveDir * Speed * TickDuration`으로 이동합니다.
- 유한한 `MoveDir`의 크기가 `1` 이하면 그대로 보존하고, `1`보다 크면 unit vector로 clamp합니다.
- X축과 Y축을 분리해 player의 Wall/Water/boundary collision을 검사합니다.
- blocking tile rectangle에 닿거나 map 밖으로 나가면 해당 axis movement를 무시합니다.
- non-finite input은 무시합니다.
- player-player collision은 아직 없습니다.

양수 `ClientTick`은 live player와 유한한 방향, stale 여부를 통과하면 movement collision이나 attack effect 판정보다 먼저 ACK합니다. 그래서 Wall 충돌, zero attack 방향, charge 소진처럼 visible effect가 없는 유효 input도 ACK하고 unknown/dead/non-finite/negative/stale input은 ACK하지 않습니다. Legacy `ClientTick: 0`은 gameplay에는 적용할 수 있지만 기존 ACK를 유지합니다.

Tile collision은 circle-vs-tile 기하와 boundary 계산을 공유하고 entity별 blocking predicate만 나눕니다.

| Tile | 값 | Player | Projectile |
| --- | ---: | --- | --- |
| Ground | 0 | 통과 | 통과 |
| Wall | 1 | 충돌 | 충돌 |
| SpawnPoint | 2 | 통과 | 통과 |
| Bush | 3 | 통과 | 통과 |
| Water | 4 | 충돌 | 통과 |
| Map boundary | - | 충돌 | 충돌 |

Attack/projectile:

- zero가 아닌 유한한 `AttackDir`는 항상 unit vector로 정규화합니다.
- 같은 tick의 input은 caller slice를 바꾸지 않고 `PlayerID` 오름차순으로 stable sort한 뒤 적용합니다.
- Shelly/Colt/Lily는 각각 `3/3/2` attack charge로 시작하고, 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다.
- `PressedAttack = true`, 정규화한 `AttackDir != zero`, 남은 charge가 모두 충족될 때만 charge 1개를 소비하고 projectile을 만듭니다.
- snapshot의 `PressedAttack`은 그 tick에 서버가 공격을 승인했을 때만 `true`입니다.
- 새 projectile은 이동 후 player 위치에서 생성됩니다.
- 기존 projectile은 tick마다 `Dir * Speed * TickDuration`으로 이동합니다.
- Wall 또는 boundary에 닿으면 `IsDestroyed = true`가 되고 Bush와 Water는 통과합니다.
- destroyed projectile은 snapshot에 남지만 더 움직이지 않습니다.

Hit/death:

- Hit eligibility는 State가 소유한 room-local selected mode rules를 사용하며 owner와 이미 사망한 player는 항상 제외합니다.
- Solo는 owner가 아닌 모든 live player를 적으로 보고, 현재 `friendlyFire=false`인 Team/Duel은 ally를 통과해 enemy만 hit합니다.
- 한 projectile이 여러 eligible target과 겹치면 `players`의 join/배정 순서에서 첫 target만 hit합니다. 이 target 순서는 input의 `PlayerID` 정렬과 별개입니다.
- hit projectile은 destroyed가 됩니다.
- target HP는 projectile damage만큼 감소합니다.
- HP가 0 이하가 되면 `HP = 0`, `IsDead = true`입니다.
- projectile 이동에서 먼저 사망한 player의 같은 tick input은 position, direction, projectile을 바꾸지 않으며 `PressedAttack = false`입니다.
- Death snapshot 이후 `duel_1v1`, Solo, Team의 elimination/GameEnd는 room-local mode rule을 사용합니다. Player별 첫 결과는 immutable하게 유지합니다.
- respawn, score는 아직 없습니다.

## Room과 WebSocket

`rooms.Handler`는 Go `ServeMux`의 method pattern과 `PathValue`로 REST/WebSocket 경로를 연결합니다. 알려진 path의 HEAD와 지원하지 않는 method는 explicit JSON fallback이 처리해 기존 404/405 body를 유지합니다. `ServeMux`가 dot segment나 중복 slash를 301로 정규화하기 전에는 얇은 preflight가 기존 JSON 오류 계약으로 돌려보냅니다.

REST debug API:

- `GET /rooms`
- `POST /rooms`
- `DELETE /rooms`
- `GET /rooms/{roomID}`
- `DELETE /rooms/{roomID}`
- `POST /rooms/{roomID}/players`
- `POST /rooms/{roomID}/start`

이 일곱 operation과 관련 method fallback은 기본 비활성화되어 JSON `404 not_found`를 반환합니다. `ENABLE_DEBUG_API=true`일 때는 정확히 하나의 `Authorization: Bearer <DEBUG_API_TOKEN>`을 먼저 검사합니다. Missing/wrong/multiple credential은 존재하지 않는 room이나 원래 405인 method보다 먼저 `401 unauthorized`입니다. 올바른 credential 뒤에야 route별 2xx/404/405/409/500을 평가합니다. WebSocket GET은 이 debug guard를 거치지 않습니다.

Room response에는 서버 simulation이 쓰는 `map` 데이터와 마지막 tick의 `latestSnapshot` summary가 포함됩니다. 외부 응답의 `map` row는 Base64 문자열이 아니라 JSON number array로 직렬화합니다. `DELETE` debug API는 in-memory room을 삭제하고 room-local ticker와 WebSocket connection을 닫습니다.

Room/player ID는 16 random bytes를 Raw URL Base64로 바꾼 22자 payload와 prefix를 사용합니다. Player session token은 32 random bytes 기반 43자이며, 발급 응답의 `sessionToken`과 tokenized `webSocketPath`에 같은 raw secret으로 나타납니다. Room private state는 SHA-256 digest만 저장합니다. Public Room/Player/Ready/Snapshot/GameEnd DTO에는 raw token이나 digest가 없습니다.

`cmd/server`는 시작할 때 embed된 `server-config/game-config.json`을 `simulation.LoadGameConfig`로 로드해 `rooms.StoreConfig`로 주입합니다. config를 읽지 못하거나 검증에 실패하면 `internal/simulation.StaticGameConfig()`의 5x5 map fallback을 사용합니다. Resolved `GameConfig`는 `ModeCatalog` 전체와 default로 고른 `SelectedMode`를 가집니다.

Mode config 소유권은 다음 한 방향으로 흐릅니다.

```text
Store GameConfig.ModeCatalog/default
  -> join request의 gameMode를 canonical config로 선택
  -> 같은 mode waiting pool 탐색 또는 room 생성
  -> immutable room.gameConfig
  -> capacity/team-slot/Ready/State/tick/GameEnd
```

Store의 config는 catalog와 새 room의 default source일 뿐, 생성된 room의 gameplay 판단에 다시 사용하지 않습니다. Room은 생성 시 selected config를 고정하고 lifecycle 전체에서 같은 config를 사용합니다.

Simple matchmaking:

- `POST /matchmaking/join`
- Optional body의 `gameMode`로 `duel_1v1`, `solo`, `team`을 선택합니다.
- Body 없음, 빈 object, 빈 문자열은 default `duel_1v1`로 처리합니다.
- 같은 mode의 waiting room 탐색과 없을 때의 room 생성을 하나의 serialized find-or-create transition으로 처리합니다. 동시 첫 join도 같은 pool을 재사용합니다.
- player를 발급합니다.
- 첫 human의 `0 -> 1` 전이에서만 room-owned one-shot 10초 ticker를 arm합니다. 후속 human join과 partial manual bot 추가는 deadline을 reset하지 않습니다.
- Timer worker와 human join은 `matchmakingMu`를 먼저 얻은 transition이 이깁니다. Timer-first late join은 다른 waiting room을 찾거나 만들고 cap이 차면 기존 409 `room_cap_reached`를 반환합니다.
- Room은 생성 시 selected `GameConfig`를 고정하고 required participant 수, team/slot/spawn, Ready quorum, simulation start가 모두 이 config를 사용합니다.
- Human과 bot을 합친 participant가 `duel_1v1`은 2명, `solo`와 `team`은 6명인 capacity를 채우면 같은-mode match를 완성합니다.
- Match가 완성된 room은 debug player 추가도 409 `room_full`로 닫아 Ready/player cardinality를 고정합니다.
- Full participant gate 뒤 room 내 human participant의 current WebSocket session이 모두 attach되면 human session에만 Ready를 보냅니다. Payload의 participant list에는 bot도 포함됩니다. Human participant가 0명이면 attach/ACK quorum은 성립하지 않습니다.
- Room 내 human participant 각각의 ready ACK가 모이면 countdown을 한 번 시작합니다. Bot은 session과 ACK가 없습니다.
- `readyPlayers map[string]bool`이 player identity별 quorum을 소유하므로 duplicate ACK는 idempotent합니다.
- `attachClientSession`은 `room.mu` 아래 `matchStatus == matched && allMatchClientsAttached()`일 때만 loading/Ready로 전이합니다. `markClientReady`도 current expected session을 확인하고 `matchStatus == loading && allMatchPlayersReady()`일 때만 countdown을 호출합니다.
- `allMatchClientsAttached`, `allMatchPlayersReady`, `startMatchCountdownLocked`는 자체 잠금이나 재진입 guard가 없으므로 caller가 `room.mu`와 위 상태 조건을 보장합니다. Countdown worker는 current ticker identity와 `starting`을 다시 확인합니다.
- `startRoomLocked`도 `room.mu` 보유를 전제로 하며 `room.state == nil`, `room.ticker == nil` guard로 simulation state와 gameplay ticker를 room당 하나만 만듭니다.
- SL-91 timer는 deadline에 selected mode의 남은 slot을 원자적으로 채웁니다. Bot ID 발급이 실패하면 모든 예약 ID를 rollback하고 partial participant 없이 `bot_fill_failed` structured log event를 한 번 기록하며 retry하지 않습니다. Ready timeout, pre-start reconnect grace, reconnect participant replacement는 추가하지 않습니다.
- 일반 delete/clear/TTL cleanup/debug start/matched pre-start cancel은 room lock 아래에서 timer resource만 detach하고, 모든 core lock을 푼 뒤 ticker `Stop`과 stop channel close를 수행합니다. 일반 cleanup은 worker join을 기다리지 않으며 `workerWG.Wait`는 Shutdown에서만 추가로 수행합니다.
- response는 top-level `gameMode`, 같은 값의 nested `room.gameMode`, `player`, `sessionToken`, tokenized `webSocketPath`를 포함합니다.
- Join 전에 process-local per-IP token bucket을 평가합니다. 기본은 10 requests/minute, burst 4이며 quota가 없으면 429가 store의 409/500보다 먼저 나갑니다. 허용된 409/500 요청도 quota를 소비합니다.
- Immediate peer가 trusted CIDR이고 `CF-Connecting-IP`가 정확히 하나의 valid IP일 때만 그 값을 client IP로 씁니다. 그 외에는 peer로 fallback하고 `X-Forwarded-For`는 무시합니다.

`map.maxPlayers = 6`과 REST `room.maxPlayers`는 계속 map/debug room capacity입니다. Matchmaking size는 room-local selected mode가 소유하며 duel은 2명, solo와 team은 6명입니다.

Mode/team rule:

- `internal/simulation.GameConfig.ModeCatalog`가 default와 세 canonical mode를, `SelectedMode`가 해당 room의 mode id, match size, team 목록, friendly-fire/team behavior metadata를 가집니다.
- `internal/simulation.PlayerAssignments`는 player id 순서와 resolved `GameConfig`를 받아 team/slot/spawn을 계산합니다. SpawnPoint를 먼저 쓰고 fallback candidate에서 `tileBlocksPlayer`가 true인 Wall/Water를 제외하며 Ground/Bush는 유지합니다. `ResolveMapData`는 두 후보 집합의 고유 좌표 수가 `map.maxPlayers`보다 작으면 config를 거부합니다.
- `internal/rooms`는 room lifecycle과 transport adapter로 남고, match capacity와 team/slot/spawn 발급 규칙은 `room.gameConfig`에서 읽습니다.
- `internal/simulation.State.Step`은 전달받은 `PlayerData.Team`과 `Slot`을 state data로 보존할 뿐 matchmaking이나 room 구성 제한을 적용하지 않습니다.
- Projectile eligibility는 selected config의 server-only `friendlyFire`와 `teamBehavior`를 사용합니다. GameEnd는 selected mode ID와 configured teams로 Duel/Solo/Team 판정을 선택합니다. Room이 생성 때 고정한 config가 lifecycle 전체의 기준입니다.

WebSocket:

- `WS /rooms/{roomID}/players/{playerID}?token=<player-session-token>`
- 발급된 room/player와 정확히 한 개의 non-empty session token만 연결할 수 있습니다.
- 정상 extra query key는 허용하지만 malformed query pair는 401입니다.
- 검증 순서는 room 404, player 404, token 401, live connection 또는 in-flight reservation 409입니다.
- waiting room은 input을 받을 수 있지만 snapshot을 보내지 않습니다.
- matchmaking ready 단계는 `Type: Ready` event로 렌더 준비 데이터를 보내고, starting 단계는 `Type: snapshot` wrapper 안에서 lowercase `Snapshot.status`와 `Snapshot.countdown: 5`를 1번 보냅니다.
- starting과 started control snapshot은 `Tick: 0`, `Players: null`을 유지하고, 첫 gameplay `Tick: 1`부터 required `LastProcessedClientTick`을 포함합니다.
- started room은 `Snapshot.status: started`와 함께 30Hz gameplay snapshot을 broadcast합니다.
- GameEnd 판정 계산은 `internal/rooms`의 순수 helper가 room-local selected config를 받아 처리하고, WebSocket delivery는 player별 `GameEnd` message 변환만 맡습니다. Wire의 `Type`, `PlayerId`, `Result`, `Win|Lose|Draw`는 바뀌지 않습니다.
- `duel_1v1`은 기존 Win/Lose와 동시 사망 Draw를 유지합니다.
- Solo 중간 탈락은 해당 player의 Lose를 처음 결과로 확정하고 그 session만 닫아 survivor tick을 계속합니다. 마지막 생존자는 Win입니다. 이전 Lose는 유지되며 나중에 전원 사망하면 아직 결과가 없던 player만 Draw입니다.
- Team 일부 사망은 계속합니다. 한 team 전멸은 3 Lose/3 Win이고 양 team 같은 tick 전멸은 6 Draw입니다.
- 각 client는 독립 writer를 가지며 payload마다 새 5초 write context를 사용합니다. 일반 gameplay snapshot은 크기 1 latest-only slot에서 coalescing해 느린 client가 room tick이나 다른 client를 막지 않습니다.
- `Ready`, `starting`, `started`, `error`는 크기 8 reliable control queue에서 순서를 보존합니다. Terminal handoff는 이미 수락한 control을 비운 뒤 `terminal snapshot -> GameEnd -> close`를 실행합니다.
- 각 client는 writer와 독립적인 30초 heartbeat ticker를 가지며 Ping마다 90초 context를 사용합니다. Ping/read/write failure는 `clientSession.close`의 close-once 경로와 expected-session 비교를 통해 현재 connection만 해제합니다.
- malformed JSON과 음수 `ClientTick`은 invalid input error만 보내고 연결은 유지합니다. Stale/duplicate 양수 tick은 error/control frame 없이 무시합니다.

Token credential은 room/player session이 남아 있는 동안 재사용할 수 있습니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 pre-start cancel로 room을 삭제합니다. Started room은 all-disconnected TTL과 hard lifetime을 따릅니다. 같은 started match에 reconnect하면 simulation state의 processed input ACK를 이어 쓰고 새 match는 `0`에서 시작합니다. Failed upgrade는 reservation만 rollback해 같은 경로로 retry할 수 있습니다. `sessionToken`, tokenized `webSocketPath`, inbound query와 전체 query 문자열은 secret으로 취급하고 log에 남기지 않습니다.

동시성 소유권은 계층으로 나눕니다. `mutationMu`는 외부 mutation과 shutdown quiescing 경계를, `matchmakingMu`는 waiting room find-or-create와 timer/human join 경쟁을, `Store.mu`는 room registry와 Store 전체 active client session lifecycle을, `room.mu`는 한 room의 participant, bot-fill ticker/stop channel, 직전 snapshot과 applied ACK, pending/bot input, simulation state, client/countdown 및 close barrier session set을, `clientSession`은 outbox와 writer/heartbeat 종료를 보호합니다. Lock 순서는 `mutationMu -> matchmakingMu -> Store.mu -> room.mu`입니다. `room.mu` 아래 input admission은 양수 `ClientTick`을 `lastPlayers[].LastProcessedClientTick`과 positive pending에 비교해 더 큰 command만 저장합니다. Legacy `0`은 last-write-wins로 positive pending도 덮을 수 있고 음수는 invalid, stale/duplicate 양수는 ignored disposition입니다. Timer resource는 room lock 아래에서 detach만 하고 ticker `Stop`과 stop channel close는 모든 core lock을 푼 뒤 실행합니다. bot-fill worker join(`workerWG.Wait`)은 Shutdown에서만 추가로 수행합니다. Attach는 Store close 판정, active session 등록, room close barrier 등록을 원자적으로 처리합니다. Session lifecycle monitor는 transport `closeDone` 뒤 room barrier에서 해당 generation을 제거하고, writer와 heartbeat 종료까지 계속 추적합니다.

`addBots`는 먼저 room 상태를 빠르게 확인한 뒤 `Store.mu -> room.mu` 순서로 bot ID를 예약하고 같은 room identity, lifecycle, 남은 capacity를 다시 검증합니다. 검증이나 ID 예약이 실패하면 예약한 모든 ID를 Store registry에서 rollback하고, room에는 partial bot을 남기지 않습니다. Bot은 credential/session map을 만들지 않습니다. Room tick은 `room.mu` 아래 직전 snapshot과 input을 한 번 소비해 `State.Step`을 정확히 한 번 호출하고, simulation이 처리한 player별 ACK를 포함한 새 snapshot copy를 다시 보관합니다. Receipt나 pending 저장만으로 ACK를 앞당기지 않습니다.

Registry lookup의 짧은 read lock 뒤에는 Store lock을 놓고 fanout과 network I/O를 수행합니다. Logger/Observer pure sink callback도 core lock 밖에서 실행합니다. Stale room/session은 expected pointer identity가 다르면 replacement를 삭제하지 않습니다.

## Cleanup

Room store는 in-memory라 TTL이 중요합니다.

- waiting idle TTL: 10분
- started all-disconnected TTL: 5분
- hard lifetime: 1시간
- connected client가 있으면 idle/all-disconnected cleanup을 막습니다.
- matchmaking matched/loading/starting 단계의 WebSocket close는 match cancel로 room과 남은 connection을 정리합니다.
- Unmatched human disconnect는 bot-fill deadline과 credential을 유지합니다. matched/loading/starting disconnect는 기존 pre-start cancel로 bot-fill resource도 함께 회수합니다.
- Solo 중간 탈락은 해당 session만 terminal close하고 room과 ticker를 유지합니다.
- Room terminal decision은 `ending`을 예약하고 ticker를 즉시 중단한 뒤 tick observer, encode, enqueue를 수행합니다. 이 상태에서는 새 mutation과 추가 tick을 받지 않습니다.
- 각 terminal session의 connected-client observer는 session close callback에서 반영되어 transport `closeDone`보다 먼저일 수 있습니다. Normal GameEnd cleanup은 current terminal session, 앞서 결과가 확정되어 기억한 session, reconnect 전에 current map에서 빠졌지만 transport close가 끝나지 않은 historical session generation의 `closeDone`을 모두 기다립니다. Solo prior loser와 ordinary reconnect predecessor 모두 room-owned barrier에 남으며, lifecycle monitor가 각 `closeDone` 뒤 제거합니다. 그 뒤 room registry, active-room observer, player ID, `room_ended` log, 남은 resources를 정리합니다. Cleanup success signal은 모든 정상 작업이 성공한 마지막에만 닫습니다.
- Hard TTL janitor와 debug clear/delete는 ending room을 제거하지 않습니다.
- Shutdown은 close barrier의 forced-teardown 예외입니다. Registry/player ID를 먼저 detach할 수 있고 deadline에는 captured underlying transport를 직접 abort하지만, cleanup worker와 session lifecycle을 join하며 normal cleanup signal과 `room_ended` log는 만들지 않습니다.
- Store당 하나의 30초 janitor가 TTL을 검사하며, `Store.Close`는 room에서 이미 분리된 terminal session까지 포함해 connection close, writer, heartbeat 종료를 기다립니다.
- Active room cap에 닿은 create/matchmaking만 즉시 cleanup을 한 번 수행하고 생성도 한 번 재시도합니다. Non-expired room만 남으면 409를 유지합니다.

## 의도적으로 없는 것

- production matchmaking queue/rating
- persistence/database/account auth
- generic scheduler/runner/orchestration
- dashboard
- Kubernetes
- respawn, score
- bot replacement
- pathfinding, 회피, 시야 판정 같은 advanced bot AI
- reconnect grace

Gameplay config는 client 공유용과 server runtime용을 분리합니다. `client-config/game-config.json`은 Client CI가 sparse checkout해 Unity runtime asset 경로로 복사하는 작은 공유 config이며 legacy `playerTypes: ["default"]` mirror와 v2 `characters` catalog (`0/1/2 = shelly/colt/lily`)를 함께 담습니다. `server-config/game-config.json` v3는 server binary가 embed해서 room store와 simulation 기본값으로 사용하는 canonical runtime config이며 tick rate, speed `2`, radius `0.5`, HP `4000/3100/4100`, 캐릭터별 `normalAttack`, `mode.default`와 `mode.catalog`, map을 담습니다. Attack charge 상태와 mode rule metadata는 server-only이고 이 두 정보 때문에 추가되는 public WebSocket field는 없습니다.

## SL-82 CharacterType ownership

Config v2 catalog가 `0=Shelly`, `1=Colt`, `2=Lily` identity mapping의 source of truth이며 current HP는 `4000/3100/4100`입니다. Server config v3는 같은 ID에 대한 `3/3/2` attack charge와 runtime combat stat을 소유합니다. `internal/rooms`는 join 선택을 canonical participant에 저장하고 REST/Ready/Snapshot transport casing으로 변환합니다. `internal/simulation`은 이미 저장된 type의 stat을 적용합니다. 따라서 join parsing, participant identity, simulation stat 적용을 서로 다른 owner가 다시 선택하지 않습니다.
