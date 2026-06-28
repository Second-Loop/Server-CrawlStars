# 결정 기록

## ADR-0001: 최소 Go HTTP Server로 시작

상태: 승인됨

맥락: Gameplay system이 존재하기 전에 CI로 검증 가능한 code가 필요합니다.

결정: 작은 Go module, `cmd/server` entrypoint, `/health`를 노출하는 `internal/health` package로 시작합니다.

결과:

- CI가 format, vet, tests, build를 즉시 검증할 수 있습니다.
- Gameplay architecture를 확정하지 않아도 server executable이 생깁니다.
- 향후 networking decision은 열린 상태로 남깁니다.

## ADR-0002: Symphony 차용 범위는 워크플로 규칙으로 제한

상태: 승인됨

맥락: Project는 orchestration infrastructure를 만들지 않고 issue-driven, review-gated collaboration을 원합니다.

결정: issue를 작업 기준으로 삼고, acceptance criteria, validation, PR review, repository workflow docs만 차용합니다. Scheduler, runner, polling daemon, web dashboard, automatic merge loop는 만들지 않습니다.

결과:

- Process가 명시적이고 versioned 상태로 repo에 남습니다.
- Automation은 나중에 Linear-scoped work로 정당화될 때만 추가할 수 있습니다.

## ADR-0003: 초기 Oracle VM Runtime은 systemd 기반 VM Pull CD 사용

상태: 승인됨

맥락: SL-6은 `main` update 이후 Go server를 위한 작은 deployment path가 필요합니다. VM에는 SSH access와 passwordless sudo가 있지만 Docker, Cloudflare Tunnel, Tailscale, nginx, caddy, required public app port가 없습니다. OCI Security List와 NSG 변경은 issue scope 밖입니다.

결정: GitHub Actions가 linux/amd64 tarball을 build하고 workflow artifact와 GitHub Release asset을 모두 publish합니다. Oracle VM은 최신 release asset을 pull하고, `/opt/crawl-stars-server/releases/<sha>/` 아래에 install하고, `/opt/crawl-stars-server/current`를 전환하고, systemd service를 restart한 뒤 `http://127.0.0.1:8080/health`를 확인합니다.

결과:

- Deployment를 위해 inbound application port가 필요하지 않습니다.
- GitHub Release asset은 public repo 기준 VM pull path를 단순하게 유지합니다.
- Server process는 Docker, PM2, Kubernetes 대신 systemd로 관리합니다.
- Rollback은 `/opt/crawl-stars-server/previous`로 symlink를 되돌리고 systemd restart를 실행하는 방식입니다.

## ADR-0004: HTTPS는 Cloudflare Tunnel로 노출

상태: 승인됨

맥락: SL-35는 Go server를 VM 내부 private 상태로 유지하면서 public HTTPS hostname을 필요로 합니다. 현재는 OCI public inbound 변경을 피해야 하므로 direct Caddy `80/tcp`, `443/tcp` ingress는 선택하지 않습니다.

결정: Go server를 `127.0.0.1:8080`에 유지하고 VM에서 Cloudflare Tunnel connector를 실행합니다. Cloudflare는 `api-crawlstars.tolerblanc.com`을 `http://127.0.0.1:8080`으로 route합니다. Apex `tolerblanc.com`은 local Caddy `http://127.0.0.1:8081`로 route하며, Caddy는 최소 hello response를 반환합니다. Public HTTPS는 Cloudflare edge가 소유하고, 이 tunnel-backed setup에서 Caddy는 local-only입니다.

결과:

- OCI public inbound는 이 경로에서 application `80/tcp` 또는 `443/tcp`를 필요로 하지 않습니다. Connector가 Cloudflare로 outbound connection을 만듭니다.
- Go server port는 VM firewall, OCI Security Lists, NSG에 열면 안 됩니다.
- Go server가 WebSocket endpoint를 구현하면 WebSocket traffic도 같은 Cloudflare Tunnel hostname을 사용할 수 있습니다.
- Caddy는 systemd로 실행되지만 apex hello page를 위해 `127.0.0.1:8081`에서만 listen합니다.

## ADR-0005: REST는 OpenAPI로, WebSocket Message는 AsyncAPI로 문서화

상태: 승인됨

맥락: E1에는 room lifecycle, client input, server snapshot flow를 위한 작은 contract surface가 필요합니다. REST endpoint는 읽고 수동 호출하기 쉬워야 하지만, WebSocket gameplay traffic은 Swagger UI가 잘 모델링하지 못하는 bidirectional stream입니다.

결정: REST API는 OpenAPI 3.x를 사용하고, interactive REST page를 추가할 때 server-hosted UI를 사용합니다. WebSocket channel과 message payload는 AsyncAPI를 사용합니다. OpenAPI는 `ws://` 또는 `wss://` server URL을 참조할 수 있지만, WebSocket input과 snapshot stream의 기준은 AsyncAPI입니다.

결과:

- REST와 WebSocket contract는 필요한 경우 schema vocabulary를 공유하면서도 독립적으로 발전할 수 있습니다.
- E1 debug API는 승격 전까지 unstable 및 E1-only로 명확히 표시해야 합니다.
- 처음 spec file을 추가하는 implementation issue는 OpenAPI와 AsyncAPI document validation을 함께 추가해야 합니다.
- SL-47 기준 hosted path는 `/openapi`, `/asyncapi`, `/openapi.yaml`, `/asyncapi.yaml`입니다.

## ADR-0006: Simulation Core는 Transport-Independent Step Contract로 시작

상태: 승인됨

맥락: E1 server work는 REST/WebSocket contract surface를 열기 전에 server-authoritative core loop skeleton을 unit test로 고정해야 합니다. SL-38은 room lifecycle, WebSocket, matching 없이 domain model과 `Step(inputs) -> Snapshot` 경계를 먼저 정의합니다.

결정: `internal/simulation` package에 최소 domain vocabulary와 `State.Step(inputs []InputCommand) Snapshot` 계약을 둡니다. 이 package는 HTTP, WebSocket, room manager, matching queue를 import하지 않습니다. SL-38에서는 tick 증가와 snapshot 생성만 고정하고, movement/collision, attack skeleton, REST room lifecycle, WebSocket runner는 후속 E1 하위 티켓에서 같은 계약 위에 얹습니다.

결과:

- Core simulation은 WebSocket 없이 Go unit test로 직접 검증할 수 있습니다.
- Red 1명 + blue 1명 구성은 테스트하되, team slot model은 한 team당 여러 player를 막지 않습니다.
- Network handler는 후속 티켓에서 `Step`을 호출하는 adapter가 되어야 하며, simulation package가 transport detail을 알면 안 됩니다.

## ADR-0007: E1 Movement Collision은 Tile Grid와 Circle-vs-Rectangle으로 고정

상태: 승인됨

맥락: SL-39는 server core `Step`이 static map fixture, movement input, wall collision을 처리해야 합니다. SL-9 client prototype은 file-based tile map, 30Hz simulator tick, player circle vs wall rectangle collision 방향을 사용했습니다. E1 server는 실제 Unity integration 없이 unit test로 같은 최소 개념을 재현해야 합니다.

결정: `internal/simulation`은 static `MapData` tile grid를 받는 `Config`를 지원합니다. `MapData`는 client prototype의 `width`, `height`, `index`, `maxPlayers`, `map` 의미를 서버 도메인 이름으로 고정합니다. `TileType`은 `TileGround = 0`, `TileWall = 1`, `TileSpawnPoint = 2`로 client `Ground`, `Wall`, `SpawnPoint` 값과 맞춥니다. `TileSize = 1.2`, `TickRate = 30`, default player `Speed = 2`, default player `Radius = 0.5`를 사용합니다. `TileWall`은 tile-aligned rectangle으로 보고, player는 radius를 가진 circle로 봅니다. `InputCommand.MoveDir`은 client `PlayerData.MoveDir`와 같은 이동 방향이며, movement는 `MoveDir * Speed * TickDuration`으로 계산합니다. Client physics와 맞춰 X축 이동과 Y축 이동은 분리해 collision을 검사합니다. Next position이 wall 또는 map 밖과 충돌하면 해당 axis movement를 무시하고 이전 position을 유지합니다. Player circle이 wall rectangle에 tangent contact만 해도 collision으로 처리합니다. Unknown player input과 invalid/non-finite movement input은 state를 변경하지 않습니다. Client `ProjectileData` 기본값(`Speed = 13`, `Damage = 10`, `Radius = 0.3`)은 SL-40 attack skeleton에서 다루며 SL-39에서는 projectile behavior를 구현하지 않습니다.

결과:

- Movement와 wall collision은 WebSocket 없이 unit test로 검증됩니다.
- Server fixture는 client prototype의 이름과 핵심 값을 맞추되, 실제 Unity integration은 후속 티켓에서 adapter로 다룹니다.
- Player-player collision, attack, damage, HP, death, respawn, score는 이 결정에 포함하지 않습니다.

## ADR-0008: E1 Attack Skeleton은 ProjectileData Snapshot State로 시작

상태: 승인됨

맥락: SL-40은 core `Step` 안에서 일반 공격 입력을 movement/collision과 같은 tick 흐름으로 처리해야 합니다. Client prototype은 `PlayerData.AttackDir`, `PlayerData.PressedAttack`, `ProjectileData` vocabulary를 사용하고, simulator tick에서 player movement 이후 새 projectile을 생성합니다. E1 server는 실제 Unity integration, projectile physics, damage 판정 없이 snapshot에 공격 관련 최소 state를 노출해야 합니다.

결정: `internal/simulation.InputCommand`는 `AttackDir`와 `PressedAttack`을 받습니다. `PlayerData`는 client field와 같은 의미의 `MoveDir`, `AttackDir`, `PressedAttack`을 보존합니다. `PressedAttack = true`이고 `AttackDir`가 zero vector가 아니면 `Step`은 movement/collision 이후 이동된 player `Pos`에서 `ProjectileData` skeleton을 생성하고 `Snapshot.Projectiles`에 포함합니다. `ProjectileData`는 client `ProjectileData`와 같은 의미의 `ID`, `OwnerID`, `Pos`, `Dir`, `Speed`, `Damage`, `Radius`, `Type`, `IsDestroyed`를 둡니다. 기본 projectile 값은 client `BaseProjectile`과 맞춰 `Speed = 13`, `Damage = 10`, `Radius = 0.3`입니다. `Damage`는 skeleton data field일 뿐이며 SL-40은 projectile movement, projectile-wall collision, projectile-player collision, hit detection, HP, death, respawn, score를 구현하지 않습니다.

결과:

- Attack input과 projectile skeleton은 WebSocket 없이 Go unit test로 직접 검증됩니다.
- Snapshot은 player state와 projectile skeleton state를 함께 전달할 수 있습니다.
- Combat result behavior는 후속 Linear issue에서 별도 acceptance criteria와 tests로 추가해야 합니다.

## ADR-0009: E1 Room REST API는 Debug Lifecycle Surface로 제한

상태: 승인됨

맥락: SL-41은 E1 개발/검증을 위해 room을 직접 만들고, player를 발급하고, start 조건을 확인할 수 있는 REST API가 필요합니다. 이 API는 matching queue나 production gameplay contract가 아니라 WebSocket room flow를 붙이기 전의 수동 검증 surface입니다. Public Cloudflare Tunnel 뒤에서 호출될 수 있으므로 active room cap도 필요합니다.

결정: `internal/rooms` package에 in-memory room store와 REST handler를 둡니다. Server는 `GET /rooms`, `POST /rooms`, `GET /rooms/{roomID}`, `POST /rooms/{roomID}/players`, `POST /rooms/{roomID}/start`를 노출합니다. Active room cap은 5개입니다. Player 발급은 서버가 `player-*` ID와 red/blue alternating team, team별 slot을 부여합니다. Start는 player가 1명 이상일 때 room status를 `started`로 바꾸며, background scheduler나 tick runner를 시작하지 않습니다. Room detail은 latest snapshot summary를 `tick`, `playerCount`, `projectileCount`로만 제공합니다. REST error response는 `{\"error\":{\"code\",\"message\"}}` 형태의 JSON으로 통일합니다.

결과:

- E1 room lifecycle은 curl/httptest로 수동 검증할 수 있습니다.
- Matching queue, persistence, scheduler, runner, production room orchestration은 여전히 제외됩니다.
- Debug API response shape는 정식 gameplay contract로 승격되기 전까지 `ai-docs/api-docs.md`의 E1 debug note를 따라야 합니다.

## ADR-0010: E1 WebSocket은 Room-Local Tick Loop와 Snapshot Broadcast로 제한

상태: 승인됨

맥락: SL-42는 REST로 생성한 room/player를 WebSocket에 연결하고, started room에서 30Hz snapshot stream을 검증해야 합니다. E1 scope는 실제 Unity integration demo, production matchmaking, persistence, generic scheduler/runner/orchestration을 포함하지 않습니다. Core simulation은 계속 WebSocket 없이 unit test 가능해야 합니다.

결정: `internal/rooms`가 `WS /rooms/{roomID}/players/{playerID}` upgrade를 처리합니다. WebSocket implementation은 `nhooyr.io/websocket`을 사용합니다. Room/player validation과 duplicate same player connection rejection은 upgrade 전에 수행합니다. Waiting room은 connection과 input 수신을 허용하지만 snapshot broadcast를 하지 않습니다. Started room은 room-local ticker를 만들고, `simulation.TickRate` 기준 30Hz로 `simulation.State.Step`을 호출해 `{\"Type\":\"snapshot\",\"Snapshot\":...}` message를 connected clients에 broadcast합니다. Client input field는 client `PlayerData`와 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`을 사용하고, Unity `Vector2` 값은 `x`, `y`로 직렬화합니다. Snapshot 내부 `PlayerData`/`ProjectileData` wire field는 `Id`, `Pos`, `MoveDir`, `AttackDir`, `PressedAttack`, `IsDead`, `OwnerId`, `Dir`, `IsDestroyed`처럼 client code 이름을 따릅니다. Invalid input payload는 connection을 끊지 않고 무시합니다.

결과:

- Fake client integration test와 fake clock test로 WebSocket behavior를 검증할 수 있습니다.
- `internal/simulation`은 WebSocket dependency를 import하지 않으므로 transport-independent contract가 유지됩니다.
- SL-42 tick loop는 room-local gameplay loop이며, reusable scheduler/runner framework가 아닙니다.
- AsyncAPI/OpenAPI spec file 생성과 hosted docs는 별도 implementation issue에서 다룹니다.

## ADR-0011: E1 Room Cleanup은 Store 진입점 TTL로 제한

상태: 승인됨

맥락: SL-43은 public debug room API가 무한히 쌓이지 않도록 최소 cleanup을 추가해야 하지만, E1 범위는 persistence, scheduler, runner, dashboard, orchestration framework를 포함하지 않습니다. 또한 invalid input은 WebSocket stream을 깨지 않아야 합니다.

결정: `internal/rooms.Store`는 fake clock으로 검증 가능한 TTL rule을 Store 진입점에서 적용합니다. Waiting room idle TTL은 10분, started room all-disconnected TTL은 마지막 WebSocket client disconnect 후 5분, hard room lifetime은 생성 후 1시간입니다. Connected client가 있으면 waiting idle TTL과 all-disconnected TTL로 즉시 삭제하지 않습니다. Hard lifetime은 hard cap으로 유지합니다. Invalid JSON input은 `{"Type":"error","Error":{"code":"invalid_input","message":"invalid input"}}` message를 보내고 해당 input만 무시하며, connection과 snapshot stream은 유지합니다. Room/player validation, duplicate connection, room full은 REST 4xx JSON error 또는 WebSocket upgrade 전 JSON error response로 고정합니다.

결과:

- Room cleanup은 API/WS/tick activity 시점에 수행되며 별도 scheduler나 persistent storage를 요구하지 않습니다.
- Public debug API exposure risk는 room cap, per-room player cap, TTL로 낮춥니다.
- Invalid input regression은 error message와 이후 snapshot stream을 함께 검증합니다.

## ADR-0012: E1 API Docs는 Server-Hosted UI와 Raw Spec으로 제공

상태: 승인됨

맥락: SL-47은 REST raw spec, WebSocket raw spec, human-readable docs를 한 번에 제공해야 합니다. E1 server는 이미 Cloudflare Tunnel 뒤에서 실행됩니다. Clean build에서 docs UI를 재생성할 수 있어야 하고, generated assets가 기준처럼 commit되면 spec drift가 생길 수 있습니다. SL-51에서는 REST 문서의 가독성과 browser 기반 debug request 경험을 위해 Swagger UI 사용을 허용합니다.

결정: Source spec은 `api/openapi.yaml`, `api/asyncapi.yaml`에 둡니다. `docs-ui` build는 dependency-free Node script로 `internal/docs/api/`, `internal/docs/static/` embed 대상 파일을 생성합니다. Server는 `GET /openapi`, `GET /asyncapi`로 human-readable UI를, `GET /openapi.yaml`, `GET /asyncapi.yaml`로 raw spec을 제공합니다. REST `/openapi`는 Swagger UI CDN wrapper로 제공하고, WebSocket `/asyncapi`는 repository-owned static HTML/CSS로 유지합니다. Generated embed assets는 commit하지 않고 `make ci`, CI, CD build stage에서 재생성합니다.

결과:

- Running server 하나만으로 API docs와 raw spec을 확인할 수 있습니다.
- Clean checkout에서 Go compile/test/build 전 `make docs-build` 또는 `make ci`가 필요합니다.
- CI/CD는 Node.js setup과 docs build를 Go validation/build 전에 수행해야 합니다.
- 이 결정은 docs delivery만 다루며, 인증, rate limit, matching queue, persistence, dashboard는 추가하지 않습니다.

## ADR-0013: SL-49 Matchmaking은 단순 Room Join Connector로 제한

상태: 승인됨, SL-58에서 start 조건 일부 대체됨

맥락: SL-49는 Unity client가 수동 debug `/rooms` flow를 직접 호출하지 않고도 room/player 정보와 WebSocket path를 받을 수 있어야 합니다. 동시에 E2 범위는 production matchmaking queue, rating algorithm, auth, persistence, scheduler/runner framework를 포함하지 않습니다. 현재 simulation fixture의 `MaxPlayers`는 6이며 10명 확장은 후속 issue입니다.

결정: Server는 `POST /matchmaking/join`을 추가합니다. Handler는 기존 `internal/rooms.Store`를 재사용해 waiting room 중 fixture max player cap 안에 여유가 있는 room을 찾고, 없으면 새 waiting room을 만듭니다. Player 발급 규칙은 manual `/rooms/{roomID}/players`와 동일하게 `player-*`, red/blue alternating team, team slot을 사용합니다. Matchmaking join으로 room player count가 2가 되면 room-local simulation ticker를 자동 start합니다. Matchmaking path는 `started` room에 late join하지 않고 다른 waiting room을 찾거나 새 room을 만듭니다. Response는 `room`, `player`, `webSocketPath`를 포함합니다.

결과:

- Client는 `POST /matchmaking/join` 응답만으로 `WS /rooms/{roomID}/players/{playerID}`에 연결할 수 있습니다.
- Existing `/rooms` manual debug lifecycle과 WebSocket snapshot flow는 유지됩니다.
- Active room cap, room TTL cleanup, fixture max player cap은 기존 in-memory store boundary를 따릅니다.
- Production matching queue, persistence, auth, dashboard, scheduler/runner/orchestration은 여전히 제외됩니다.
- SL-58 이후 matchmaking join은 2명째 참가 시 즉시 simulation을 start하지 않고 WebSocket ready와 server 내부 countdown 이후 start합니다.

## ADR-0014: E1 Projectile Movement는 Existing Projectile Tick으로 처리

상태: 승인됨

맥락: SL-53은 SL-40에서 snapshot에만 추가되던 `ProjectileData` skeleton을 실제 tick 흐름에서 이동시키고, wall 또는 map boundary에 닿으면 destroyed state로 표시해야 합니다. 이 단계는 player hit, HP, death, respawn, score를 아직 포함하지 않습니다.

결정: `internal/simulation.State.Step`은 input으로 새 projectile을 만들기 전에 기존 projectile을 먼저 이동합니다. Projectile 이동은 `Dir * Speed * TickDuration` 기준입니다. 새 projectile은 생성된 tick의 snapshot에는 생성 위치로 보이고, 다음 tick부터 이동합니다. 이동한 projectile circle이 wall tile 또는 map boundary에 닿거나 밖으로 나가면 `IsDestroyed = true`로 표시합니다. Destroyed projectile은 snapshot에 남지만 이후 tick에서 더 이동하지 않습니다.

결과:

- Projectile 생성, 이동, 파괴 순서는 unit test로 재현할 수 있습니다.
- WebSocket room tick loop는 같은 `State.Step` 결과를 broadcast하므로 별도 transport behavior가 필요하지 않습니다.
- Projectile-player collision, HP, death behavior는 SL-54에서 별도 acceptance criteria와 tests로 추가해야 합니다.

## ADR-0015: E1 Hit Result는 PlayerData HP와 IsDead Snapshot으로 표현

상태: 승인됨

맥락: SL-54는 projectile-player collision, HP 감소, 사망 state를 server-authoritative simulation snapshot에 반영해야 합니다. 이 단계는 respawn, score, win/loss, character-specific stats, production friendly-fire policy를 포함하지 않습니다.

결정: `PlayerData.HP`를 현재 체력 값으로 추가하고 기본값은 `100`으로 둡니다. 기존 projectile 이동 이후 active projectile circle이 owner가 아닌 live player circle과 겹치면 hit으로 처리합니다. Hit projectile은 `IsDestroyed = true`가 되고, target player HP는 projectile `Damage`만큼 감소합니다. HP가 `0` 이하가 되면 `HP = 0`, `IsDead = true`로 snapshot에 반영합니다. Owner 본인은 자기 projectile의 hit target에서 제외합니다.

결과:

- 2명 player hit flow는 `internal/simulation` unit test로 deterministic하게 검증됩니다.
- WebSocket snapshot은 `HP` field를 포함하므로 AsyncAPI와 사람이 읽는 API docs를 함께 갱신해야 합니다.
- Respawn, score, win/loss, character-specific stats는 후속 issue에서 별도 acceptance criteria와 tests로 추가해야 합니다.

## ADR-0016: SL-58 Match Start는 Ready Event와 Snapshot.status로 표현

상태: 승인됨

맥락: SL-58은 `/matchmaking/join` 이후 client asset load/render 준비를 기다린 뒤 countdown을 거쳐 simulation을 시작해야 합니다. REST `POST /matchmaking/join` response shape는 유지해야 하며, 새 REST polling이나 SSE를 추가하지 않는 것이 범위입니다. Client는 game scene render 전에 서버가 쓰는 map과 player별 spawn position을 알아야 하므로, pre-game render data와 gameplay snapshot을 구분해야 합니다.

결정: Matchmaking에서 2명째 player가 들어오면 room을 matched 상태로 잠그고 late join 대상에서 제외하지만 REST `room.status`는 `waiting`으로 유지합니다. 두 matched player가 WebSocket에 연결하면 server는 `{"Type":"Ready","Map":...,"Players":[...]}` event를 양쪽 client에 broadcast합니다. `Map.map` row는 Base64 문자열이 아니라 JSON number array이고, `Players[].SpawnPosition`은 서버 spawn assignment 결과입니다. Client는 준비 완료 시 `{"Type":"ready"}`를 보냅니다. 모든 required client가 ready가 되면 server는 `Snapshot.status: "starting"`과 `Snapshot.countdown: 5`를 1번 broadcast합니다. Client는 이 신호를 기준으로 fake timer를 표시하고, server는 5초를 내부에서 센 뒤 `Snapshot.status: "started"`를 보낸 다음 room-local 30Hz simulation ticker를 시작합니다. Start 전 WebSocket close는 match cancel로 처리해 room과 남은 connection을 정리합니다.

결과:

- REST `/matchmaking/join` response shape와 `room.status` casing은 유지됩니다.
- Ready event는 render data를 담당하고, Snapshot lifecycle field는 countdown 시작 신호와 `started` 신호를 담당합니다.
- WebSocket lifecycle field는 lowercase `status/countdown`, gameplay field는 기존 client-compatible PascalCase `Tick/Players/Projectiles`를 유지합니다.
- `internal/simulation`은 match lifecycle을 모르는 transport-independent gameplay core로 남습니다.
- AsyncAPI는 Ready event, ready ACK, starting signal, gameplay snapshot 예시를 OpenAPI 수준으로 자세히 기록해야 합니다.
- Start 이후 disconnect policy, bot replacement, ping/pong timeout은 여전히 별도 issue 범위입니다.

## ADR-0017: SL-30 Gameplay Config는 client 공유용과 server runtime용을 분리

상태: 승인됨

맥락: SL-30은 server와 Unity client가 공유해야 하는 gameplay 상수 위치를 정리해야 합니다. 기존에는 tile size, radius, HP, speed, damage, map이 Go 상수와 map fixture, 문서에 흩어져 있었습니다. Client CI는 server repo에서 필요한 config만 가져와 Unity build에 포함할 수 있어야 하지만, client가 쓰지 않는 HP, speed, damage, tick rate, map까지 공유 artifact에 들어가면 책임 경계가 흐려집니다.

결정: config artifact를 두 파일로 분리합니다. `client-config/game-config.json`은 Unity client가 build 때 sparse checkout해서 runtime asset 경로로 복사하는 공유 config입니다. 이 파일은 `tileSize`, `playerRadius`, `playerTypes`, `projectileRadius`, `projectileTypes`만 포함합니다. `server-config/game-config.json`은 server binary가 embed하고 `cmd/server`가 로드하는 server-only runtime config입니다. 이 파일은 `tickRate`, `tile.size`, player type별 `radius/hp/speed`, projectile type별 `radius/damage/speed`, `map`을 포함합니다.

결과:

- Client build가 가져가는 공유 상수는 `client-config/game-config.json`입니다.
- 서버 런타임 기본값은 `server-config/game-config.json`입니다.
- Go 상수는 fallback과 drift test 기준으로 유지하되, 서버 런타임은 embedded server config를 우선합니다.
- `docs-ui/scripts/validate.mjs`와 `internal/simulation` 테스트가 두 config 구조와 Go 상수 drift를 검증합니다.
- Client가 서버 권위 movement/damage를 재계산한다는 뜻은 아니며, 최종 gameplay state는 계속 server snapshot을 기준으로 받습니다.

## ADR-0018: SL-63 GameEnd는 Player별 Win/Lose/Draw Event로 처리

상태: 승인됨

맥락: SL-63은 HP가 0이 된 뒤 client가 scene 종료와 결과 UI를 처리할 수 있도록 WebSocket 결과 event가 필요합니다. Simulation core는 HP/IsDead snapshot까지만 담당하고, room lifecycle과 WebSocket 종료 처리는 `internal/rooms` boundary에 남겨야 합니다. 같은 tick에 양쪽 player가 동시에 사망하는 상황은 드물지만 v1 결과 계약은 명시해야 합니다.

결정: started room에서 snapshot 이후 HP가 0인 player가 있으면 server는 같은 tick의 snapshot을 먼저 broadcast하고, 이어서 연결된 각 player에게 `{"Type":"GameEnd","PlayerId":...,"Result":"Win|Lose|Draw"}` event를 보냅니다. 한 명만 사망하면 생존 player는 `Win`, 사망 player는 `Lose`입니다. 같은 tick에 양쪽 player가 동시에 사망하면 양쪽 모두 `Draw`로 보냅니다. Server는 GameEnd event 전송 후 room-local ticker와 WebSocket connection을 정리하고 room store에서 해당 room을 제거합니다. 마지막 공격자 기준 타이브레이커는 후속 issue에서 별도 논의합니다.

결과:

- Client는 마지막 death snapshot으로 화면 state를 갱신한 뒤 GameEnd event로 결과 UI와 scene exit를 처리할 수 있습니다.
- Simulation package는 transport-independent `Step(inputs) -> Snapshot` 계약을 유지합니다.
- 동시 사망 정책은 모두 `Draw`입니다.
