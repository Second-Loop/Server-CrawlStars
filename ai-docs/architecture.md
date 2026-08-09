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

Process log와 HTTP server error log는 stdout의 JSON `slog`로 기록합니다. Process event 이름은 `msg`에, room lifecycle과 WebSocket event 이름은 `event`와 `msg`에 기록합니다. Room/WebSocket log는 `roomID`, 필요한 경우 `playerID`와 bounded category/status만 추가합니다. `websocket_disconnected`는 최초 종료 원인, connection generation, 종료 시 match phase, session duration, 마지막 전송 gameplay tick을 함께 기록합니다. 종료 원인은 고정 enum만 허용하고 raw close reason, transport error, query/token은 기록하지 않습니다. Logger와 Observer callback은 Store를 다시 호출하지 않는 bounded pure sink입니다. Mutation 함수가 반환되면 해당 transition의 log와 metric publication도 끝난 상태입니다.

Private listener는 정확한 `GET /metrics`에서 다음 process-local Prometheus series만 제공합니다.

- `crawlstars_active_rooms`
- `crawlstars_connected_clients`
- `crawlstars_tick_duration_seconds`
- `crawlstars_websocket_closes_total{cause="<bounded-cause>"}`

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

`internal/rooms/bot.go`는 직전 snapshot에서 가장 가까운 live enemy를 고르고 공통 `InputCommand`만 만듭니다. 같은 거리는 `PlayerID` 오름차순, 같은 좌표의 방향은 `+X`로 고정합니다. Room은 bot별 마지막 승인 tick에서 room-local next-attack tick을 계산해 첫 gameplay activation은 즉시 허용하고 이후에는 해당 room config의 `normalAttack.rechargeTicks` 경계 전까지 `PressedAttack`만 억제합니다. Cooldown 중에도 MoveDir/AttackDir은 계속 생성하며 실제 charge 소비·승인과 Colt scheduled burst는 `internal/simulation.State.Step`에 남깁니다. Pending map의 key가 human command의 authoritative `PlayerID`이며, bot key로 들어온 외부 command는 `ClientTick: 0`인 pure controller 결과로 대체합니다. Room은 stale/duplicate 양수 input을 Step 전에 줄이는 admission guard이고, `internal/simulation.State`가 player별 `LastProcessedClientTick`과 `SkillReadyTick`의 최종 소유자입니다. Movement, projectile, hit, HP/death, attack charge, skill approval/cooldown과 processed input ACK는 계속 `internal/simulation.State.Step`만 변경합니다.

### SL-83 일반 공격 소유권

server config v5가 일반 공격 실행의 source of truth입니다. 각 player type의 `normalAttack`이 kind, hit당 damage, tile range, `3/3/2` max charge, 30 tick recharge와 projectile schedule을 소유하고, projectile type catalog는 radius/speed를 소유합니다. Client config v3는 조준·cooldown UI와 로컬 bot 입력 보조값만 제공하며 authoritative combat stat을 대체하지 않습니다.

`internal/rooms`는 canonical `CharacterType`, room-local config, human/bot input을 production `State.Step`에 전달하고 authoritative snapshot으로 기존 GameEnd 계산기를 호출합니다. Room은 캐릭터별 피해나 test-only damage branch를 갖지 않습니다. 실제 room regression도 Ready/countdown/spawn 뒤 production input으로 Colt projectile death와 reciprocal 1100-HP Lily Draw를 검증합니다.

`internal/simulation`은 activation을 승인하고 캐릭터별 실행기를 고릅니다. Shelly는 같은 activation tick에 5발 spread, Colt는 `A+[0,6,12,18,24,30]` burst와 `A+31` non-overlap, Lily는 wall/boundary로 자른 2.2 tile centerline의 same-tick batched damage를 수행합니다. 이 책임 분리는 기존 InputMessage, PlayerData, ProjectileData, Snapshot wire shape를 바꾸지 않습니다.

### SL-84 Skill cooldown 소유권

Server config v5의 player type별 `skill.cooldownTicks`가 Shelly/Colt/Lily `360/390/330`을 소유합니다. SL-84는 SL-99에서 도입한 Client config v3를 바꾸지 않습니다. `internal/rooms.InputMessage`는 optional `PressedSkill`을 strict boolean으로 decode해 missing은 false로 두고 present null/wrong type은 `invalid_input`으로 거부합니다. `AttackDir`은 같은 command에서 재사용하지만 그 자체가 skill을 trigger하지 않으며, cooldown-blocked attempt는 queue하지 않습니다. 유효한 양수 command라면 skill이 cooldown에 막혀도 processed ACK는 진행합니다.

`internal/simulation.PlayerData.SkillReadyTick`이 persistent canonical absolute state이고 별도 cooldown map을 만들지 않습니다. `Snapshot.Tick >= SkillReadyTick`이면 ready이며 tick `A` 승인 시 cooldown `C`를 더해 `A + C`를 기록하고 exact `A + C` tick도 허용합니다. `PressedSkill`은 각 Step 시작에 false로 reset하고 승인 tick에만 true인 transient server approval pulse입니다. 초기 player state는 `false/0`입니다.

Skill-ready와 non-zero direction이면 normal attack보다 먼저 승인하고 attack charge를 보존합니다. Cooldown 또는 zero direction이면 기존 normal attack 판정으로 fall through합니다. Public AsyncAPI는 `0.7.0`으로 올리고 gameplay `PlayerData.PressedSkill`/`SkillReadyTick`을 required로 두지만, REST OpenAPI와 starting/started control의 `Players: null`, `Projectiles: null`은 유지합니다. 실제 skill effect와 bot skill use는 SL-85 범위입니다.

핵심 값:

- `TickRate = 30`
- `TileSize = 1.2`
- character catalog/HP = `0=Shelly/4000`, `1=Colt/3100`, `2=Lily/4100`; speed/radius = `2`, `0.5`
- player normal attack charge/recharge = Shelly `3/30`, Colt `3/30`, Lily `2/30`
- projectile speed/radius = `13`, `0.3`; damage/type은 공격 owner의 `normalAttack`에서 projectile snapshot으로 복사
- default map source = server binary가 embed한 `server-config/game-config.json`의 Client PR #28 `Map_0` exact 40x40 grid (`index=0`, `maxPlayers=6`, spawn tile 2 정확히 6개)
- map drift guard = client `Map_0` 값을 고정한 exact-grid Go regression
- production config load/validation failure = listener를 열기 전에 process startup 실패. `StaticGameConfig()`의 5x5 map은 명시적 test/dev helper에서만 사용
- `internal/simulation/fixtures/default-map.json`은 테스트용 fixture로만 사용
- player spawn = map의 `TileSpawnPoint(2)`를 join 순서대로 먼저 사용하고, 부족하면 Wall/Water를 제외한 fallback candidate 사용. Ground/Bush는 유지하고 config 단계에서 `map.maxPlayers`명분의 고유 좌표와 max supported character radius 기준 원형 spawn 비겹침을 검증함

Movement:

- `MoveDir * Speed * TickDuration`으로 이동합니다.
- 유한한 `MoveDir`의 크기가 `1` 이하면 그대로 보존하고, `1`보다 크면 unit vector로 clamp합니다.
- X축과 Y축을 분리해 player의 Wall/Water/boundary collision을 검사합니다.
- blocking tile rectangle에 닿거나 map 밖으로 나가면 해당 axis movement를 무시합니다.
- 같은 tick의 live player 후보를 축별로 함께 계산하고 swept circle이 접촉하거나 통과하면 충돌한 두 player의 해당 축 이동을 모두 취소합니다. 입력/PlayerID 우선순위나 밀어내기는 없습니다.
- 이미 겹친 live player는 해당 축의 separation을 엄격히 늘리고 중간에 더 깊어지지 않는 이동만 허용합니다. overlap 유지·심화 이동은 취소하고 dead player는 blocking 대상에서 제외합니다.
- non-finite input은 무시합니다.

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

### SL-104 Map collision candidate traversal

Map collision의 결과 계약은 바꾸지 않고, tile 순회 범위만 query의 축 정렬 bounding box에서 유도합니다.

- Player circle과 projectile circle은 먼저 기존 map boundary 검사를 수행한 뒤, circle AABB를 map의 row/column index로 변환합니다. 각 index 범위는 map 경계로 clamp하고, 부동소수점 접점과 `±epsilon` 경계를 놓치지 않도록 보수적으로 한 칸을 포함합니다.
- Player blocking tile은 `Wall`/`Water`, projectile blocking tile은 `Wall`이라는 기존 정책을 그대로 사용합니다. radius `0`, default radius, 큰 radius, custom `tileSize`, mapless state의 fallback도 같은 boundary/tangent semantics를 유지합니다.
- Lily의 centerline은 segment AABB를 같은 방식으로 row/column 후보로 줄입니다. map boundary의 first-contact `t`, wall AABB의 tangent 접촉, wall과 target이 같은 contact인 경우의 wall 우선 규칙은 바꾸지 않습니다.
- `internal/simulation/map_collision_test.go`의 exhaustive oracle은 test-only 기준 구현입니다. deterministic property-style 차분 테스트가 player Wall/Water, projectile Wall, boundary/tangent, Lily wall segment 결과를 최적화 경로와 비교합니다. Production code는 oracle을 호출하지 않습니다.
- 재현 가능한 비교 benchmark는 `BenchmarkCollidesWithMap{Optimized,Exhaustive}`, `BenchmarkFirstBlockingSegmentT{Optimized,Exhaustive}`, `BenchmarkStateStepWithLargeMap`이며 다음처럼 실행합니다.

  ```sh
  go test ./internal/simulation -run '^$' -bench 'Benchmark(CollidesWithMap|FirstBlockingSegmentT|StateStep)' -benchmem -count=3
  ```

  Exhaustive benchmark는 변경 전 순회 비용의 proxy이고, 큰 map의 `State.Step` benchmark는 실제 room tick 경로의 성능 경계를 확인합니다. Benchmark 수치는 CPU와 Go version에 따라 달라지므로 correctness 판정은 차분 테스트, 성능 판단은 같은 command의 optimized/exhaustive 쌍으로 합니다.

### SL-111 production Map_0 동기화와 6인 30Hz 경계

- Production embedded source는 `server-config/game-config.json`이며 `width=40`, `height=40`, `index=0`, `maxPlayers=6`, spawn tile `2` 정확히 6개를 제공합니다. `StaticMapFixture()`와 `internal/simulation/fixtures/default-map.json`은 작은 test/dev·legacy fixture로 유지합니다.
- 승인된 Client source는 `Second-Loop/Client-CrawlStars`의 `CrawlStars/Assets/StreamingAssets/Maps/Map_0.json`입니다. Current `main` SHA는 `50f10c27a575c2bc8f53c7e7b3385de69876184c`, Map_0 last-changing commit은 `4f3292603e6809e918f609e5be8dd03d3ded8988`, Git blob SHA는 `89228cead52df257a0489101d045b3d288634e27`, raw SHA-256은 `babb748ff60827499992d7020ec296bc72afa32928ecf5642b3c4e82d943cf00`, `jq -S -c` canonical semantic SHA-256은 `b1729488ec19efb433d19df112b88f1fd1b33a1f39f15fb1cb4df0f93d9f8e60`입니다. 40x40 grid는 Linear SL-100 본문의 승인 JSON과 exact 비교합니다.
- `internal/simulation`의 `TestProductionMapSixPlayersAt30HzSmoke`는 production map에서 6명의 solo assignment를 만들고 30 tick(30Hz 기준 1초)을 이동시켜 player 수·생존 상태를 확인합니다. `BenchmarkProductionMapSixPlayersAt30Hz`는 같은 setup을 30 tick 단위로 측정합니다.

재현 command:

```sh
go test ./internal/simulation -run TestProductionMapSixPlayersAt30HzSmoke -count=1
go test ./internal/simulation -run '^$' -bench '^BenchmarkProductionMapSixPlayersAt30Hz$' -benchmem -count=3
```

2026-08-07 Apple M5 Pro/arm64, Go 1.26.4의 3회 측정은 `47,268–48,641 ns/op` per 30-tick batch, `616,766–634,674 ticks/s`, `94,800 B/op`, `300 allocs/op`였습니다. 같은 실행의 SL-104 micro comparison은 circle optimized `21.18–21.44 ns/op` 대 exhaustive `24,120–24,647 ns/op`, Lily segment optimized `521.2–526.9 ns/op` 대 exhaustive `7,536–7,583 ns/op`, 4-player large-map `State.Step` `1,088–1,091 ns/op`였습니다. 이 수치는 correctness threshold가 아니라 재현 가능한 macro/micro baseline입니다. 최적화 전 exhaustive oracle과의 결과 동일성은 exact-grid·collision differential test가 판단하며, 40x40 map 변경은 map source/assignment contract만 바꾸고 collision policy·SL-102 fail-fast startup policy는 유지합니다.

Attack/projectile:

- zero가 아닌 유한한 `AttackDir`는 항상 unit vector로 정규화합니다.
- 같은 tick의 input은 caller slice를 바꾸지 않고 `PlayerID` 오름차순으로 stable sort한 뒤 적용합니다.
- Shelly/Colt/Lily는 각각 `3/3/2` attack charge로 시작하고, 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다.
- `PressedAttack = true`, 정규화한 `AttackDir != zero`, 남은 charge가 모두 충족될 때만 charge 1개를 소비하고 projectile emission 또는 Lily melee intent를 승인합니다.
- snapshot의 `PressedAttack`은 그 tick에 서버가 공격을 승인했을 때만 `true`입니다.
- 새 projectile은 이동 후 player 위치에서 생성됩니다.
- 기존 projectile은 tick마다 `Dir * Speed * TickDuration`으로 이동합니다.
- Wall 또는 boundary에 닿으면 `IsDestroyed = true`가 되고 Bush와 Water는 통과합니다.
- destroyed projectile은 더 움직이지 않으며, destroyed가 된 snapshot tick을 `D`라고 할 때 `D..D+29`의 30개 gameplay snapshot에만 기존 `IsDestroyed = true` tombstone으로 남습니다. `D+30` snapshot 전에 Server canonical state와 snapshot에서 제거합니다.
- `ProjectileData` wire field/event/ACK는 추가하지 않습니다. 느린 writer의 capacity-1 latest-only coalescing은 특정 tombstone snapshot을 건너뛸 수 있으므로, Client absent-ID reconciliation은 별도 shared 계약이며 이번 Server 범위에 Client 수정은 포함하지 않습니다.

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

`cmd/server`는 시작할 때 embed된 `server-config/game-config.json`의 JSON value를 정확히 하나만 `simulation.LoadGameConfig`로 로드해 `rooms.StoreConfig`로 주입합니다. trailing value/garbage, decode, version, catalog, map, spawn capacity 또는 max-radius spawn 비겹침 검증이 실패하면 application과 listener를 만들지 않고 process startup을 실패시킵니다. 명시된 nonzero invalid config는 rooms/simulation/assignment helper에서도 silent fallback하지 않습니다. `internal/simulation.StaticGameConfig()`의 5x5 map은 config를 생략한 명시적 test/dev helper에만 남습니다. Resolved `GameConfig`는 `ModeCatalog` 전체와 default로 고른 `SelectedMode`를 가집니다.

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
- Bot 생성은 기존 `0=Shelly`, `1=Colt`, `2=Lily` 중 하나를 각 bot마다 균등·독립적으로 선택하며 room 내 중복을 허용합니다. Manual add와 timer fill이 같은 chooser를 사용하고, participant에 저장된 값은 match 동안 REST/Ready/Snapshot과 simulation에 고정됩니다.
- Match가 완성된 room은 debug player 추가도 409 `room_full`로 닫아 Ready/player cardinality를 고정합니다.
- Match 완성 시 strict 30초 room-owned human attach ticker를 arm합니다. `now >= deadline`인 reserve/attach는 callback 지연과 무관하게 거부하고, expiry는 pre-start room 전체와 player/session identity를 제거합니다.
- 모든 human current session이 attach되면 ticker를 detach하고 Loading/Ready로 전이합니다. Bot-fill match의 human이 이미 붙어 있으면 즉시 전이하고 all-bot debug room에는 ticker를 arm하지 않습니다.
- Full participant gate 뒤 room 내 human participant의 current WebSocket session이 모두 attach되면 human session에만 Ready를 보냅니다. Payload의 participant list에는 bot도 포함됩니다. Human participant가 0명이면 attach/ACK quorum은 성립하지 않습니다.
- Room 내 human participant 각각의 ready ACK가 모이면 countdown을 한 번 시작합니다. Bot은 session과 ACK가 없습니다.
- `readyPlayers map[string]bool`이 player identity별 quorum을 소유하므로 duplicate ACK는 idempotent합니다.
- `attachClientSession`은 `room.mu` 아래 `matchStatus == matched && allMatchClientsAttached()`일 때만 loading/Ready로 전이합니다. `markClientReady`도 current expected session을 확인하고 `matchStatus == loading && allMatchPlayersReady()`일 때만 countdown을 호출합니다.
- `allMatchClientsAttached`, `allMatchPlayersReady`, `startMatchCountdownLocked`는 자체 잠금이나 재진입 guard가 없으므로 caller가 `room.mu`와 위 상태 조건을 보장합니다. Countdown worker는 current ticker identity와 `starting`을 다시 확인합니다.
- `startRoomLocked`도 `room.mu` 보유를 전제로 하며 `room.state == nil`, `room.ticker == nil` guard로 simulation state와 gameplay ticker를 room당 하나만 만듭니다.
- SL-91 timer는 deadline에 selected mode의 남은 slot을 원자적으로 채웁니다. Bot ID 발급이 실패하면 모든 예약 ID를 rollback하고 partial participant 없이 `bot_fill_failed` structured log event를 한 번 기록하며 retry하지 않습니다. SL-105 attach expiry 뒤 retry는 새 REST join과 새 identity이며 `Idempotency-Key` replay를 추가하지 않습니다. Ready ACK timeout, pre-start reconnect grace, reconnect participant replacement는 추가하지 않습니다. Started match의 비의도적 transport disconnect는 10초 room-owned reconnect grace를 사용하고, expiry는 per-player goroutine 없이 다음 gameplay tick에서 batch 처리합니다.
- 일반 delete/clear/TTL cleanup/debug start/matched pre-start cancel은 room lock 아래에서 timer resource만 detach하고, 모든 core lock을 푼 뒤 ticker `Stop`과 stop channel close를 수행합니다. 일반 cleanup은 worker join을 기다리지 않으며 `workerWG.Wait`는 Shutdown에서만 추가로 수행합니다.
- response는 top-level `gameMode`, 같은 값의 nested `room.gameMode`, `player`, `sessionToken`, tokenized `webSocketPath`를 포함합니다.
- Join 전에 process-local per-IP token bucket을 평가합니다. 기본은 10 requests/minute, burst 4이며 quota가 없으면 429가 store의 409/500보다 먼저 나갑니다. 허용된 409/500 요청도 quota를 소비합니다.
- Immediate peer가 trusted CIDR이고 `CF-Connecting-IP`가 정확히 하나의 valid IP일 때만 그 값을 client IP로 씁니다. 그 외에는 peer로 fallback하고 `X-Forwarded-For`는 무시합니다.

`map.maxPlayers = 6`과 REST `room.maxPlayers`는 계속 map/debug room capacity입니다. Matchmaking size는 room-local selected mode가 소유하며 duel은 2명, solo와 team은 6명입니다.

Mode/team rule:

- `internal/simulation.GameConfig.ModeCatalog`가 default와 세 canonical mode를, `SelectedMode`가 해당 room의 mode id, match size, team 목록, friendly-fire/team behavior metadata를 가집니다.
- `internal/simulation.PlayerAssignments`는 player id 순서와 resolved `GameConfig`를 받아 team/slot/spawn을 계산합니다. SpawnPoint를 먼저 쓰고 fallback candidate에서 `tileBlocksPlayer`가 true인 Wall/Water를 제외하며 Ground/Bush는 유지합니다. `ResolveMapData`는 두 후보 집합의 고유 좌표 수가 `map.maxPlayers`보다 작으면 config를 거부합니다.
- `internal/rooms`는 room lifecycle과 transport adapter로 남고, match capacity와 team/slot/spawn 발급 규칙은 `room.gameConfig`에서 읽습니다. `StoreConfig.BotCharacterChooser`로 bot 캐릭터 선택을 주입할 수 있으며 production 기본 chooser는 ID/session 발급용 `Store.random`과 분리된 `crypto/rand` source를 사용합니다.
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
- 각 client는 독립 writer를 가지며 payload마다 새 5초 write context를 사용합니다.
- 일반 non-terminal gameplay snapshot은 client별 capacity-1 latest-only slot에서 coalescing합니다. 어느 player라도 `PressedSkill: true`이면 해당 snapshot을 reliable control 경로로 승격합니다. PressedSkill approval은 reliable approval exception으로 size-8 reliable control FIFO에서 전달합니다. 승격 전에 older pending normal snapshot과 기존 deferred normal snapshot을 버리고 reliable approval로 전환합니다. 후속 normal은 reliable approval pending이 모두 drain될 때까지 session별 deferred latest 하나만 보관합니다. multiple approval은 FIFO로 전달합니다. reliable approval write가 성공해 pending이 모두 drain된 뒤 최신 일반 snapshot 하나를 flush합니다. flush는 approval -> latest 순서로 실행합니다. accepted approval은 terminal보다 먼저 drain합니다. accepted approval을 모두 drain한 뒤 terminal snapshot -> GameEnd -> close 순서로 실행합니다. deferred normal snapshot은 종료 시 버립니다. queue overflow/write failure는 해당 session close/release의 fail-closed로 처리합니다. 무한히 느린 session 유지나 application-level ACK/replay를 보장하지 않습니다. PressedAttack: true-only snapshot은 계속 latest-only로 전달합니다. 새 wire field/event를 추가하지 않습니다. AsyncAPI dialect 3.0.0과 info 0.7.0을 유지합니다. Control snapshot의 `Players: null`과 `Projectiles: null`을 유지하고 gameplay entity를 넣지 않습니다. SL-85 effect는 이번 범위에서 제외합니다. SL-99 client config v3/server config v5 경계를 유지합니다.
- `Ready`, `starting`, `started`, `error`는 같은 size-8 reliable control FIFO에서 순서를 보존합니다.
- 각 client는 writer와 독립적인 30초 heartbeat ticker를 가지며 Ping마다 90초 context를 사용합니다. Ping/read/write failure는 `clientSession.close`의 close-once 경로와 expected-session 비교를 통해 현재 connection만 해제합니다. `clientSession`은 첫 close cause만 보존하고 lifecycle publication이 종료 log와 `crawlstars_websocket_closes_total`을 정확히 한 번 반영합니다. 이전 generation의 늦은 종료도 자신의 generation/context로 기록되어 reconnect session에 귀속되지 않습니다.
- malformed JSON과 음수 `ClientTick`은 invalid input error만 보내고 연결은 유지합니다. Stale/duplicate 양수 tick은 error/control frame 없이 무시합니다.

Token credential은 room/player session이 남아 있는 동안 재사용할 수 있습니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 pre-start cancel로 room을 삭제합니다. Started room은 all-disconnected TTL과 hard lifetime을 따릅니다. 같은 started match에 reconnect하면 simulation state의 processed input ACK를 이어 쓰고 새 match는 `0`에서 시작합니다. Failed upgrade는 reservation만 rollback해 같은 경로로 retry할 수 있습니다. `sessionToken`, tokenized `webSocketPath`, inbound query와 전체 query 문자열은 secret으로 취급하고 log에 남기지 않습니다.

동시성 소유권은 계층으로 나눕니다. `mutationMu`는 외부 mutation과 shutdown quiescing 경계를, `matchmakingMu`는 waiting room find-or-create와 bot-fill/attach-deadline/human join 경쟁을, `Store.mu`는 room registry와 Store 전체 active client session lifecycle을, `room.mu`는 한 room의 participant, bot-fill 및 match-attach ticker/stop channel, 직전 snapshot과 applied ACK, pending/bot input, simulation state, client/countdown 및 close barrier session set을, `clientSession`은 outbox와 writer/heartbeat 종료를 보호합니다. Lock 순서는 `mutationMu -> matchmakingMu -> Store.mu -> room.mu`입니다. `room.mu` 아래 input admission은 양수 `ClientTick`을 `lastPlayers[].LastProcessedClientTick`과 positive pending에 비교해 더 큰 command만 저장합니다. Legacy `0`은 last-write-wins로 positive pending도 덮을 수 있고 음수는 invalid, stale/duplicate 양수는 ignored disposition입니다. Timer resource는 room lock 아래에서 detach만 하고 ticker `Stop`과 stop channel close는 모든 core lock을 푼 뒤 실행합니다. deadline worker join(`workerWG.Wait`)은 Shutdown에서만 추가로 수행합니다. Attach는 Store close 판정, strict deadline 판정, active session 등록, room close barrier 등록을 원자적으로 처리합니다. Session lifecycle monitor는 transport `closeDone` 뒤 room barrier에서 해당 generation을 제거하고, writer와 heartbeat 종료까지 계속 추적합니다.

`addBots`는 먼저 room 상태를 빠르게 확인한 뒤 `Store.mu -> room.mu` 순서로 bot ID를 예약하고 같은 room identity, lifecycle, 남은 capacity를 다시 검증합니다. 그 뒤 전체 bot character batch를 선택하고 모두 성공한 경우에만 append합니다. 검증, ID 예약, chooser가 실패하면 예약한 모든 ID를 Store registry에서 rollback하고, room에는 partial bot을 남기지 않습니다. Bot은 credential/session map을 만들지 않습니다. Room tick은 `room.mu` 아래 직전 snapshot과 input을 한 번 소비해 `State.Step`을 정확히 한 번 호출하고, simulation이 처리한 player별 ACK를 포함한 새 snapshot copy를 다시 보관합니다. Receipt나 pending 저장만으로 ACK를 앞당기지 않습니다.

Registry lookup의 짧은 read lock 뒤에는 Store lock을 놓고 fanout과 network I/O를 수행합니다. Logger/Observer pure sink callback도 core lock 밖에서 실행합니다. Stale room/session은 expected pointer identity가 다르면 replacement를 삭제하지 않습니다.

## Cleanup

Room store는 in-memory라 TTL이 중요합니다.

- waiting idle TTL: 10분
- started all-disconnected TTL: 5분
- hard lifetime: 1시간
- connected client가 있으면 idle/all-disconnected cleanup을 막습니다.
- matchmaking matched/loading/starting 단계의 WebSocket close는 match cancel로 room과 남은 connection을 정리합니다.
- Unmatched human disconnect는 bot-fill deadline과 credential을 유지합니다. matched/loading/starting disconnect는 기존 pre-start cancel로 bot-fill resource도 함께 회수합니다.
- Matched attach deadline expiry는 room 전체, 남은 connection, player ID와 credential을 회수하고 `matchmaking_transition`의 bounded `cancelled/attach_deadline_expired` cause를 남깁니다. Secret/query/raw error는 기록하지 않습니다.
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
- reconnect grace

Gameplay config는 client 공유용과 server runtime용을 분리합니다. `client-config/game-config.json`은 Client CI가 sparse checkout해 Unity runtime asset 경로로 복사하는 client config v3 artifact입니다. Stable `type 0/1/2`, Unity world unit의 `normalAttackDistance`·`skillAttackDistance`, 초 단위 `normalAttackCoolDown`·`skillAttackCoolDown`, client charge 표현용 `maxBullets`를 담습니다. Server의 Go parser가 canonical artifact를 검증하고 Client 소비 계약은 필수 field와 version을 build/runtime 양쪽에서 거부하도록 요구합니다. `server-config/game-config.json` v5는 server binary가 embed해서 room store와 simulation 기본값으로 사용하는 canonical runtime config이며 tick rate, speed `2`, radius `0.5`, HP `4000/3100/4100`, 캐릭터별 `normalAttack`과 `skill.cooldownTicks`, server-only bot tuning, `mode.default`와 `mode.catalog`, map을 담습니다. 실제 hit/range/charge와 스킬 승인 결과는 server-authoritative state와 snapshot이 최종 truth이며 client 설정으로 gameplay를 재판정하지 않습니다. Skill cooldown의 public 경계는 gameplay `PlayerData.SkillReadyTick`이며, 이 client artifact 변경은 public WebSocket field를 추가하지 않습니다.

## SL-82 CharacterType ownership

Client config v3의 `characters[].type`과 API 계약이 `0=Shelly`, `1=Colt`, `2=Lily` stable identity mapping을 공유합니다. Server config v5는 같은 ID에 대한 HP `4000/3100/4100`, `3/3/2` attack charge, runtime combat stat과 `skill.cooldownTicks`를 소유하고 simulation의 canonical `PlayerData.SkillReadyTick`으로 다음 사용 가능 시점을 공개합니다. `internal/rooms`는 join 선택을 canonical participant에 저장하고 REST/Ready/Snapshot transport casing으로 변환합니다. `internal/simulation`은 이미 저장된 type의 stat을 적용합니다. 따라서 join parsing, participant identity, simulation stat 적용을 서로 다른 owner가 다시 선택하지 않습니다.

## SL-110 Bot CharacterType ownership

Bot character choice는 server participant creation 책임입니다. Manual `addBots`와 first-human 10초 timer fill은 남은 bot 수만큼 chooser를 호출해 fixed catalog `Shelly/Colt/Lily`를 한 번씩 독립적으로 고릅니다. Catalog size 3에 대한 production `crypto/rand.Int` rejection sampling은 각 선택을 균등하게 만들고 duplicate를 막지 않습니다.

Chooser는 ID/session token 발급 stream인 `Store.random`을 읽지 않습니다. 모든 값을 먼저 선택한 뒤 append하므로 chooser/ID 오류는 partial participant 없이 예약 ID를 rollback하고 timer fill은 기존 `bot_fill_failed` one-shot/no-retry 정책을 유지합니다. Injected test chooser는 deterministic sequence와 실패를 재현합니다.

선택 결과는 `playerResponse.CharacterType`의 canonical participant state입니다. REST room response, Ready projection, `simulation.PlayerData`, gameplay Snapshot은 이 값을 복사하며 match 중 재추출하지 않습니다. Human join/default policy, existing character catalog, basic bot controller와 simulation rules는 바꾸지 않습니다.

## SL-116 결정적 Bot controller와 A* ownership

- Room이 `room-owned controller state`와 bot별 next-attack cadence를 `room.mu` 아래 소유합니다. State에는 `exploreEpoch`, 목적지, `(start, goal)` path cache와 next direction을 보관하고 bot 제거/room cleanup 때 cadence와 함께 폐기합니다.
- Room은 이전 authoritative `Players`와 `Projectiles`를 observation으로 사용합니다. 한 gameplay tick의 `all bots read the same previous snapshot`을 보장하고, 각 입력을 만든 뒤 human pending input과 함께 one PlayerID-sorted merged State.Step을 정확히 한 번 호출합니다.
- 이동 priority는 `dodge -> explore -> retreat -> chase`입니다. Projectile 위협이 없고 live enemy가 탐지 범위에 없으면 explore, HP 비율이 경계 이하면 retreat, 그 밖에는 chase를 선택합니다. `attack decision is independent of movement`이며 dodge/retreat 중에도 공격 조건을 따로 평가합니다. Bot command는 `ClientTick: 0`, `PressedSkill: false`입니다.
- 탐지 범위는 `detectionRangeWorld = 15`이고 경계값을 포함합니다. Retreat는 `retreatHPRatio = 0.2` 이하일 때 target 반대 방향의 `retreatDistanceWorld = 6` raw goal을 map-valid tile로 backoff합니다.
- Explore는 `exploreArrivalDistanceWorld = 0.25` 안에 도착하면 다음 tick에 목적지를 다시 고릅니다. Passable 후보를 row-major로 정렬하고 현재 tile을 제외한 뒤, 길이 prefix가 붙은 room ID·bot PlayerID·big-endian epoch의 canonical byte sequence를 SHA-256해 첫 8 bytes를 후보 index로 사용합니다. 목적지 선택과 path failure마다 epoch을 증가시키며 실패 시 다음 tick에 다시 선택합니다.
- Dodge는 self-owned/destroyed/ally projectile을 제외하고 전방 `projectileLookAheadWorld = 8`, `dodgeMarginWorld = 0.35` 안의 hostile ray만 사용합니다. Threat를 `ProjectileID` 오름차순으로 합성하고 상쇄 시 nearest forward distance와 `+90°`, `-90°` 후보 순서를 사용하며 둘 다 막히면 zero movement입니다.
- A*는 4방향(상·하·좌·우)만 사용하고 Wall과 Water를 blocked로 봅니다. `G=1`, Manhattan `H`, open-set 동률은 `F -> H -> y -> x` 오름차순입니다. Invalid/blocked start·goal, disconnected map, open set 소진은 path failure이며 explore는 목적지를 폐기하고 chase/retreat는 그 tick 이동을 zero로 둡니다.
- Projectile의 owner/target 판정은 simulation의 공통 `CanPlayerDamage`를 사용해 mode별 friendly-fire 규칙과 dodge threat가 실제 hit 규칙에서 drift하지 않게 합니다. Room controller는 공격 성공이나 피해를 확정하지 않습니다.
- Room cadence는 snapshot의 실제 승인 결과에만 반응합니다. only an approved snapshot with `PressedAttack: true` updates cadence; 거절된 시도나 Colt burst 진행은 next-attack tick을 앞당기지 않습니다. Movement, attack charge, projectile, HP/death와 final snapshot은 계속 `State.Step`이 소유합니다.
- `server-config/game-config.json` v5는 server-only bot tuning을 exact 값으로 보유합니다: `detectionRangeWorld=15`, `exploreArrivalDistanceWorld=0.25`, `retreatHPRatio=0.2`, `retreatDistanceWorld=6`, `projectileLookAheadWorld=8`, `dodgeMarginWorld=0.35`. Client config v3와 REST/OpenAPI/AsyncAPI field/event shape is unchanged하며, AsyncAPI info version `0.7.0`을 유지합니다.
