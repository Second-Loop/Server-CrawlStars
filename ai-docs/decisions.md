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

후속 반영 (SL-81 Stack 3): 기존 E1 범위 설명은 당시 결정을 보존합니다. 현재 debug REST와 method fallback은 기본적으로 `404 not_found`이며, 활성화하면 모든 debug operation에 Bearer credential을 요구합니다. 올바른 credential 뒤에만 기존 2xx/404/405/409/500 route 결과를 평가합니다.

## ADR-0010: E1 WebSocket은 Room-Local Tick Loop와 Snapshot Broadcast로 제한

상태: 승인됨

맥락: SL-42는 REST로 생성한 room/player를 WebSocket에 연결하고, started room에서 30Hz snapshot stream을 검증해야 합니다. E1 scope는 실제 Unity integration demo, production matchmaking, persistence, generic scheduler/runner/orchestration을 포함하지 않습니다. Core simulation은 계속 WebSocket 없이 unit test 가능해야 합니다.

결정: `internal/rooms`가 `WS /rooms/{roomID}/players/{playerID}` upgrade를 처리합니다. WebSocket implementation은 `nhooyr.io/websocket`을 사용합니다. Room/player validation과 duplicate same player connection rejection은 upgrade 전에 수행합니다. Waiting room은 connection과 input 수신을 허용하지만 snapshot broadcast를 하지 않습니다. Started room은 room-local ticker를 만들고, `simulation.TickRate` 기준 30Hz로 `simulation.State.Step`을 호출해 `{\"Type\":\"snapshot\",\"Snapshot\":...}` message를 connected clients에 broadcast합니다. Client input field는 client `PlayerData`와 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`을 사용하고, Unity `Vector2` 값은 `x`, `y`로 직렬화합니다. Snapshot 내부 `PlayerData`/`ProjectileData` wire field는 `Id`, `Pos`, `MoveDir`, `AttackDir`, `PressedAttack`, `IsDead`, `OwnerId`, `Dir`, `IsDestroyed`처럼 client code 이름을 따릅니다. Invalid input payload는 connection을 끊지 않고 무시합니다.

결과:

- Fake client integration test와 fake clock test로 WebSocket behavior를 검증할 수 있습니다.
- `internal/simulation`은 WebSocket dependency를 import하지 않으므로 transport-independent contract가 유지됩니다.
- SL-42 tick loop는 room-local gameplay loop이며, reusable scheduler/runner framework가 아닙니다.
- AsyncAPI/OpenAPI spec file 생성과 hosted docs는 별도 implementation issue에서 다룹니다.

후속 반영 (SL-81 Stack 3): WebSocket path는 그대로 두고 정확히 한 개의 non-empty `token` query로 player session을 인증합니다. Room/player/token/duplicate 검증 순서는 404/404/401/409이며, 409는 live connection과 in-flight reservation을 모두 포함합니다. Debug Bearer는 WebSocket GET에 적용하지 않습니다.

## ADR-0011: E1 Room Cleanup은 Store 진입점 TTL로 제한

상태: 승인됨

맥락: SL-43은 public debug room API가 무한히 쌓이지 않도록 최소 cleanup을 추가해야 하지만, E1 범위는 persistence, scheduler, runner, dashboard, orchestration framework를 포함하지 않습니다. 또한 invalid input은 WebSocket stream을 깨지 않아야 합니다.

결정: `internal/rooms.Store`는 fake clock으로 검증 가능한 TTL rule을 Store 진입점에서 적용합니다. Waiting room idle TTL은 10분, started room all-disconnected TTL은 마지막 WebSocket client disconnect 후 5분, hard room lifetime은 생성 후 1시간입니다. Connected client가 있으면 waiting idle TTL과 all-disconnected TTL로 즉시 삭제하지 않습니다. Hard lifetime은 hard cap으로 유지합니다. Invalid JSON input은 `{"Type":"error","Error":{"code":"invalid_input","message":"invalid input"}}` message를 보내고 해당 input만 무시하며, connection과 snapshot stream은 유지합니다. Room/player validation, duplicate connection, room full은 REST 4xx JSON error 또는 WebSocket upgrade 전 JSON error response로 고정합니다.

결과:

- Room cleanup은 API/WS/tick activity 시점에 수행되며 별도 scheduler나 persistent storage를 요구하지 않습니다.
- Public debug API exposure risk는 room cap, per-room player cap, TTL로 낮춥니다.
- Invalid input regression은 error message와 이후 snapshot stream을 함께 검증합니다.

후속 반영 (SL-81 Stack 3): Token credential은 room/player session이 남아 있는 동안 재사용할 수 있지만 room lifetime을 연장하지 않습니다. Matchmaking pre-start 연결이 실제로 끊기면 room이 취소되고, started room은 all-disconnected TTL과 hard lifetime을 따릅니다. Failed upgrade는 reservation만 rollback하므로 같은 발급 경로로 재시도할 수 있습니다.

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

후속 반영 (SL-81 Stack 3): 당시 `player-*`와 response shape 문구는 역사적 결정을 보존합니다. 현재 join은 opaque room/player ID와 `sessionToken`, tokenized `webSocketPath`를 발급하며, 기본 10 requests/minute·burst 4의 IP별 token bucket을 store보다 먼저 평가합니다. 여기서 제외한 `auth`는 account/persistence auth이며 transport credential은 추가됐습니다.

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
- SL-58 당시에는 start 이후 disconnect policy, bot replacement, ping/pong timeout을 별도 issue로 남겼습니다.

후속 반영 (SL-81 Stack 3): 당시 REST response shape 유지 문구는 SL-58 변경 범위를 뜻합니다. 현재는 `sessionToken`이 추가됐고 Ready/Snapshot/GameEnd payload 자체는 계속 secret-free입니다. Pre-start 실제 disconnect가 room을 취소하는 기존 규칙 때문에 그 이후 같은 token으로 reconnect할 수는 없습니다.

후속 반영 (SL-81 Stack 4): Connection별 30초 heartbeat와 Ping별 90초 deadline을 추가했고, 실패는 pre-start cancel 또는 started all-disconnected TTL 경로를 재사용합니다. Bot replacement와 별도 reconnect grace는 계속 범위 밖입니다.

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

## ADR-0019: SL-70 기본 Match Mode는 Server Runtime Config의 1v1로 고정

상태: 승인됨

후속 상태: SL-86의 ADR-0028이 duel-only 활성화 결론을 `duel_1v1`/`solo`/`team` room-local 선택으로 대체합니다. 이 ADR의 server-only config와 map/debug capacity 분리 배경은 이력으로 유지합니다.

맥락: `internal/rooms`가 matchmaking required player 수 `2`와 red/blue team assignment를 직접 하드코딩하고 있었습니다. 동시에 map fixture의 `maxPlayers = 6`은 map/debug room capacity인데, 이를 active matchmaking size로 해석하면 6인 solo나 3v3 team mode가 의도치 않게 켜질 수 있습니다. SL-70은 mode/team rule boundary를 만들되 실제 6인 mode를 구현하지 않는 범위입니다.

결정: `server-config/game-config.json`과 `internal/simulation.GameConfig`에 server-only `mode`를 둡니다. 현재 active mode는 `duel_1v1`, `playersPerMatch = 2`, red/blue team 각각 size 1입니다. `mode.rules`에는 `teamBehavior`와 `friendlyFire`를 두어 free-for-all/team behavior와 friendly fire 정책을 나중에 확장할 수 있게 합니다. `internal/rooms`는 resolved `GameConfig`에서 match size와 team/slot 발급 규칙을 읽고, room lifecycle과 REST/WebSocket transport adapter 역할에 집중합니다. `internal/simulation.State.Step`은 player의 `Team`과 `Slot`을 state data로 보존하지만 matchmaking size, room 구성, 6인 mode 활성화는 적용하지 않습니다.

결과:

- 기본 runtime behavior는 기존 1v1 matchmaking 그대로입니다.
- `map.maxPlayers = 6`은 map/debug room capacity로 유지되고 active match size와 분리됩니다.
- 6인 solo나 3v3 team mode는 config/model 확장 지점만 생겼고 활성화하지 않았습니다.

## ADR-0020: SL-72 Capacity와 Player Assignment 경계 분리

상태: 승인됨

후속 상태: Capacity와 assignment 단일 source 결정은 유지합니다. “6-player mode/client selection 비활성”과 REST schema 불변 결론만 SL-86의 ADR-0028로 대체됐습니다.

맥락: `map.maxPlayers`는 debug room capacity이고 `mode.playersPerMatch`는 active matchmaking size입니다. 두 값을 같은 숫자처럼 쓰면 기본 1v1 matchmaking이 6명 match로 확장되거나, 반대로 debug room이 2명으로 줄어드는 regression이 생길 수 있습니다. 또한 Ready event의 spawn 위치와 실제 simulation 초기 위치가 다른 helper를 타면 client render와 서버 판정이 갈라질 수 있습니다.

결정: `internal/rooms`는 room/debug capacity와 match capacity를 별도 helper로 읽고, room lifecycle과 REST/WebSocket transport 책임을 유지합니다. Team/slot/spawn 계산은 `internal/simulation.PlayerAssignments`가 resolved server `GameConfig`와 player id join 순서를 받아 계산합니다. Ready event와 `simulation.NewStateWithConfig`에 전달하는 초기 player data는 같은 assignment 결과를 씁니다.

Spawn은 map의 `TileSpawnPoint(2)`를 tile scan/join 순서로 먼저 사용합니다. Spawn point가 부족하거나 없으면 map 크기에서 유도한 fallback 좌표를 사용해 panic을 피하고, fallback 후보가 남아 있는 동안 이미 사용한 spawn point 좌표와 겹치지 않게 합니다. 기본 5x5 fallback 좌표는 기존 red/blue 위치와 호환됩니다.

결과:

- 기본 runtime behavior는 계속 `duel_1v1`, `playersPerMatch = 2`입니다.
- `map.maxPlayers = 6`은 map/debug room capacity로 남습니다.
- 6-player solo, 3v3 team mode, client mode selection, production queue는 활성화하지 않습니다.
- REST/WebSocket response schema는 바꾸지 않았으므로 OpenAPI/AsyncAPI contract 변경은 없습니다.

## ADR-0021: SL-71 GameEnd 판정 계산과 WebSocket Delivery 분리

상태: 승인됨

후속 상태: 판정 계산과 delivery 분리는 유지합니다. 단일 active duel 전제는 ADR-0028로 대체됐고, solo/team은 새 mode별 elimination rule 없이 기존 player-survival fallback을 사용합니다.

맥락: SL-63에서 추가한 GameEnd 흐름은 `internal/rooms` 안에서 snapshot broadcast, Win/Lose/Draw 판정, player별 WebSocket event 생성, room cleanup이 한 흐름에 붙어 있었습니다. SL-71은 wire contract를 바꾸지 않고 판정 계산만 테스트 가능한 경계로 분리하는 리팩터입니다.

결정: `internal/rooms`에 GameEnd result domain helper를 두고, `Store.tickRoom`은 기존 순서인 `Step` -> latest snapshot 저장 -> snapshot delivery -> GameEnd 판정 -> GameEnd delivery -> room 삭제/resource 수집 -> unlock -> write -> close를 유지합니다. `room.gameEndDeliveries`는 계산 결과를 `{"Type":"GameEnd","PlayerId":...,"Result":"Win|Lose|Draw"}`로 바꾸는 transport boundary만 맡습니다.

현재 active mode는 `duel_1v1`입니다. N-player solo, 3v3 team elimination, score, respawn, 마지막 공격자 기준 tie-breaker는 활성화하지 않고 후속 issue에서 mode-specific GameEnd helper로 확장합니다.

결과:

- Simulation package는 계속 `Step(inputs) -> Snapshot`만 담당합니다.
- WebSocket GameEnd schema와 AsyncAPI contract는 바뀌지 않습니다.
- 동시 사망은 계속 양쪽 `Draw`입니다.

## ADR-0022: SL-81 일반 공격 Budget과 방향 검증은 Simulation State가 소유

상태: 승인됨

맥락: `State.Step`은 NaN/Inf만 거르고 input vector 크기를 그대로 신뢰했습니다. 그래서 큰 `MoveDir`로 이동 속도를 키우거나 큰 `AttackDir`로 projectile 속도를 바꿀 수 있었고, `PressedAttack`을 매 tick 보내면 제한 없이 공격할 수 있었습니다. 기존 projectile 이동에서 먼저 사망한 player도 같은 tick의 input을 적용할 수 있었습니다.

결정: `internal/simulation` 경계에서 유한한 `MoveDir`은 크기 `1`을 넘을 때만 clamp하고, zero가 아닌 유한한 `AttackDir`은 항상 unit vector로 정규화합니다. Player별 일반 공격 상태는 private `attackState`로 `State` 안에 두며, server runtime player config의 `maxAttackCharges = 4`, `attackRechargeTicks = 30`을 사용합니다. 최대치보다 적을 때만 recharge를 진행하고, 유효한 공격 요청이 남은 charge를 소비해 projectile을 만든 경우에만 snapshot `PressedAttack = true`로 기록합니다. 사망한 player input은 state mutation 전에 거부합니다.

Attack charge 설정과 진행도는 server-only입니다. `client-config/game-config.json`, `InputCommand`, `PlayerData`, `ProjectileData`, `Snapshot`에는 field를 추가하지 않습니다.

결과:

- 과대 이동과 조준 vector가 server simulation 결과를 증폭하지 못합니다.
- 기본 player는 4회 연속 공격할 수 있고, 30 tick마다 최대 4까지 1 charge를 회복합니다.
- Zero 방향, 소진된 charge, dead player input은 projectile을 만들지 않으며 snapshot `PressedAttack`도 `false`입니다.
- `State.Step(inputs) -> Snapshot` transport boundary와 REST/WebSocket schema는 그대로 유지됩니다.

## ADR-0023: SL-81 Transport Credential과 Trusted Client IP 경계

상태: 승인됨

맥락: Sequential room/player ID와 무인증 WebSocket은 다른 player connection 선점이 가능했고, public Cloudflare Tunnel 뒤의 debug REST는 destructive operation을 보호하지 않았습니다. `/matchmaking/join`은 room cap만 있어 단일 client의 반복 요청을 충분히 제한하지 못했습니다. 이 단계는 account identity, persistence, production matchmaking queue를 도입하지 않고 transport 경계만 보호해야 합니다.

결정:

- Room/player ID는 16 random bytes를 Raw URL Base64로 인코딩한 22자 payload에 `room_`/`player_` prefix를 붙입니다.
- Player session token은 32 random bytes를 같은 방식으로 인코딩한 43자 opaque credential입니다. Raw 값은 발급 JSON의 `sessionToken`과 tokenized `webSocketPath` 두 곳에 나타나고, client가 WebSocket `token` query로 다시 보냅니다. Private room state에는 SHA-256 digest만 저장합니다.
- WebSocket은 정확히 한 개의 non-empty token을 요구합니다. 실패 우선순위는 room 404, player 404, token 401, live connection 또는 in-flight reservation 409입니다. 정상 extra query key는 허용하지만 malformed query pair는 전체 query를 401로 거부합니다.
- Token은 일회용이 아니며 room/player session이 존재하는 동안 credential을 재사용할 수 있습니다. Matchmaking matched/loading/starting 단계의 실제 disconnect는 pre-start cancel로 room을 삭제합니다. Started room은 all-disconnected TTL과 hard lifetime을 따릅니다. Failed HTTP-to-WebSocket upgrade는 reservation만 rollback해 재시도할 수 있습니다.
- Debug REST와 관련 method fallback은 기본 `404 not_found`입니다. 활성화 시 정확히 하나의 `Authorization: Bearer <DEBUG_API_TOKEN>`을 요구하고, missing/wrong/multiple credential은 route dispatch보다 먼저 `401 unauthorized`입니다. 올바른 credential 뒤에 기존 route 결과를 평가합니다. WebSocket GET은 이 guard에서 제외하고 player session token으로 인증합니다.
- Matchmaking join은 store보다 먼저 process-local per-IP token bucket을 평가합니다. 기본값은 10 requests/minute, burst 4이며 bucket이 비면 429가 409/500보다 우선합니다. 허용된 요청은 store에서 409/500으로 끝나도 quota를 소비합니다. 429는 `rate_limited` JSON과 최소 1초 정수 `Retry-After`를 반환합니다.
- `CF-Connecting-IP`는 immediate peer가 `TRUSTED_PROXY_CIDRS`에 속하고 header가 정확히 하나의 valid IP일 때만 client IP로 사용합니다. 그 외에는 peer IP로 fallback하며 `X-Forwarded-For`는 항상 무시합니다.
- `sessionToken`, tokenized `webSocketPath`, inbound token query, `DEBUG_API_TOKEN`은 모두 secret-bearing surface입니다. Raw 값과 전체 query 문자열을 log, telemetry, 문서 예시에 기록하지 않습니다.

결과:

- Public Room/Player/list/detail/Ready/Snapshot/GameEnd payload에는 raw token이나 digest가 없습니다.
- Debug route는 운영에서 명시적으로 켜고 secret을 설정하기 전까지 노출되지 않습니다.
- Cloudflare Tunnel peer trust를 빠뜨리거나 CF header가 invalid하면 public client가 loopback peer bucket을 공유합니다. 이는 spoofing을 막는 fallback이지만 가용성 영향을 주므로 deployment 설정과 검증이 필요합니다.
- Account auth, persistence, multi-process/distributed rate limit은 후속 issue입니다. WebSocket heartbeat는 SL-81 Stack 4에서 후속 반영했습니다.

## ADR-0024: SL-81 Room/Session 동시성과 WebSocket 전달 경계

상태: 승인됨

맥락: 하나의 Store lock 아래에서 모든 room tick과 WebSocket write를 직렬화하면 느린 client 하나가 다른 room과 client까지 막습니다. 일반 snapshot을 모두 reliable하게 쌓으면 지연된 과거 state가 backlog가 되고, 반대로 Ready/GameEnd 같은 lifecycle message까지 버리면 client state가 깨집니다. Ping/pong이 없으면 silent peer가 connected 상태로 남아 TTL cleanup도 막습니다.

결정:

- `Store.mu`는 registry/lifecycle, `room.mu`는 한 room의 mutable gameplay/client 상태, `clientSession`은 outbox와 writer/heartbeat lifecycle을 소유합니다. `State.Step`, fanout, network I/O 동안 Store lock을 잡지 않습니다.
- 일반 gameplay snapshot만 client별 크기 1 latest-only slot에서 coalescing합니다. `Ready`, `starting`, `started`, `error`는 reliable control queue를 사용합니다.
- Terminal delivery는 이미 수락한 control을 비운 뒤 `terminal snapshot -> GameEnd -> close`를 writer 안에서 순서대로 실행합니다. Payload write마다 새 5초 context를 사용합니다.
- Connection마다 writer와 독립적인 30초 heartbeat를 실행하고 Ping마다 90초 context를 사용합니다. Ping/read/write failure는 `clientSession.close`의 close-once와 expected-session release를 공유합니다.
- `Store.mu`가 active client session registry를 함께 보호합니다. Attach는 Store close 판정, active 등록, heartbeat 시작을 `Store.mu -> room.mu` 순서 안에서 끝냅니다. Lifecycle monitor는 connection close, writer, heartbeat가 모두 끝난 뒤 session을 registry에서 제거하므로, GameEnd가 room을 먼저 삭제해도 `Store.Close`가 terminal in-flight session을 close하고 join할 수 있습니다.
- Heartbeat failure는 기존 lifecycle을 재사용합니다. Pre-start match는 cancel하고, started room의 마지막 client가 사라지면 disconnected TTL을 시작합니다. Bot replacement와 reconnect grace는 추가하지 않습니다.
- Store당 30초 janitor 하나가 TTL을 검사합니다. Create/matchmaking이 cap에 닿았을 때만 즉시 cleanup과 생성 retry를 각각 한 번 수행합니다.

결과:

- 서로 다른 room은 병렬로 tick하고 느린 client는 room tick이나 다른 client를 막지 않습니다.
- Stale reader/writer/heartbeat는 reconnect된 최신 session이나 같은 ID의 replacement room을 제거하지 않습니다.
- Snapshot freshness와 lifecycle reliability를 분리하면서 GameEnd 직전 terminal order를 보장합니다.
- REST payload schema는 바뀌지 않았고 OpenAPI는 변경하지 않습니다. AsyncAPI와 human docs에는 heartbeat와 delivery lifecycle을 명시합니다.

## ADR-0025: SL-81 Application은 Private Metrics와 하나의 Shutdown 경계를 소유

상태: 승인됨

맥락: 기존 process는 `http.ListenAndServe` 하나만 실행해서 SIGTERM 때 Store와 WebSocket worker를 정리하지 못했습니다. Room lifecycle log와 runtime metrics도 없어서 배포 후 상태를 확인하기 어려웠습니다. Metrics를 application HTTP에 그대로 추가하면 Cloudflare Tunnel을 통해 public endpoint가 될 수 있으므로 별도 노출 경계가 필요합니다.

결정:

- `cmd/server`의 application 하나가 `rooms.Store` 하나, process-local Prometheus registry 하나, application HTTP server와 metrics HTTP server를 함께 소유합니다.
- Application listener 기본값은 `127.0.0.1:8080`입니다. Metrics listener 기본값은 `127.0.0.1:9090`이고 `METRICS_ADDR`는 `127.0.0.0/8` 또는 `::1`의 IP literal과 숫자 port만 허용합니다. Hostname, wildcard, private/Tailscale IP는 거부합니다.
- 두 listener를 모두 먼저 bind한 뒤 serve를 시작합니다. Context cancel이나 어느 한 server 종료가 전체 shutdown을 시작합니다.
- SIGINT/SIGTERM shutdown은 `rooms.Store`, application HTTP, metrics HTTP를 병렬로 정리하고 최대 10초 기다립니다. Deadline 뒤에는 남은 HTTP transport를 강제로 닫습니다. Systemd는 `TimeoutStopSec=15s`를 유지합니다.
- Store shutdown은 외부 mutation을 quiesce하고 janitor, room/countdown ticker, WebSocket connection, writer, heartbeat를 join합니다. Client close는 `1000 / server shutting down`입니다.
- Application HTTP는 `ReadHeaderTimeout=5s`, `IdleTimeout=60s`를 사용합니다. WebSocket/streaming response를 위해 server-wide `WriteTimeout`은 두지 않습니다.
- Process와 HTTP server error는 stdout의 JSON `slog`로 기록합니다. Room/WebSocket lifecycle log는 bounded field만 기록하고 secret-bearing query/token과 raw transport error를 제외합니다.
- Logger와 Observer callback은 Store를 다시 호출하지 않는 bounded pure sink입니다. Core lock 밖에서 동기 publication하며 mutation 함수가 반환될 때 해당 transition의 log와 metric 반영도 끝납니다.
- Private listener의 정확한 `GET /metrics`만 `crawlstars_active_rooms`, `crawlstars_connected_clients`, `crawlstars_tick_duration_seconds`를 제공합니다. Application HTTP의 `/metrics`와 private listener의 다른 method/path는 노출하지 않습니다.

결과:

- 한 listener만 열린 반쪽짜리 process를 피하고 SIGTERM 때 Store와 HTTP transport를 같은 lifecycle로 정리합니다.
- Metrics는 loopback 운영 surface로 남고 Cloudflare Tunnel이나 public firewall에 연결하지 않습니다.
- `GET /metrics`는 REST/WebSocket product contract가 아니므로 OpenAPI, AsyncAPI, 사람이 읽는 API reference를 변경하지 않습니다.
- 종료가 끝나면 active room/client gauge가 0이고, lifecycle mutation 반환 시점과 log/metric 관측 시점이 일치합니다.

## ADR-0026: SL-81 VM Pull은 한 Release Tag와 Checksum에 고정

상태: 승인됨

맥락: 기존 VM pull script는 `latest` download URL에서 package를 바로 받아 압축을 풀었습니다. 배포 중 `latest`가 바뀌면 package와 manifest가 서로 다른 release에서 올 수 있고, `ASSET_NAME`에 경로 문자를 허용하면 root 실행 시 임시 디렉터리 밖 파일을 덮어쓸 수 있습니다. Release가 제공하는 `SHA256SUMS`도 소비자가 확인하지 않았습니다.

결정:

- `RELEASE_TAG=latest`는 시작 시 GitHub latest release API로 정확히 한 번 조회하고, 응답의 non-`latest` tag를 이번 실행의 고정 tag로 사용합니다. 명시적인 tag는 API 조회 없이 URL encoding합니다.
- Package와 `SHA256SUMS`는 모두 같은 고정 tag의 direct release download URL에서 받습니다. CD workflow와 GitHub asset ID 기반 다운로드는 이 변경에서 다루지 않습니다.
- `ASSET_NAME`은 영문자, 숫자, `.`, `_`, `-`만 포함한 최대 255자 basename으로 제한합니다. 빈 값, `.`, `..`, `SHA256SUMS`는 network와 임시 파일 생성 전에 거부합니다.
- Manifest는 요청 asset의 GNU checksum record 정확히 하나만 허용합니다. `sha256sum --strict -c`가 성공하기 전에는 tar 추출, install, symlink 전환, systemd restart를 실행하지 않습니다.
- Optional GitHub token은 mode `0600` 임시 header file로 전달하고 caller xtrace와 환경 변수 노출 시간을 줄입니다. 줄바꿈 token은 요청 전에 거부합니다.
- `make deploy-test`는 fake command PATH로 network 없이 latest 1회 해석, URL 고정, 입력 거부, checksum 순서, token 취급, rollback을 검증하며 `make ci`에 포함합니다.

결과:

- 한 번의 배포는 하나의 release tag와 exact checksum record에 고정되고 검증 실패 시 현재 release를 바꾸지 않습니다.
- 안전하지 않은 asset 이름은 root 권한의 filesystem/network side effect 전에 차단됩니다.
- Same-release checksum은 손상과 asset 혼합을 감지하지만 GitHub release 쓰기 권한 탈취로 package와 manifest가 함께 바뀌는 공격은 방어하지 않습니다.

## ADR-0027: SL-92 Client Map_0를 Runtime 기준으로 고정하고 Entity별 Tile 충돌 정책 분리

상태: 승인됨

맥락: Client SL-79에서 merge된 `Map_0`에는 기존 Ground(0), Wall(1), SpawnPoint(2) 외에 Bush(3)와 Water(4)가 있습니다. Server runtime map과 REST/WebSocket `MapData`가 이 값을 그대로 전달해야 하며, Player는 Water에 막히지만 projectile은 통과하므로 기존 wall-only 판정 하나로는 entity별 규칙을 표현할 수 없습니다.

결정:

- Client SL-79에서 merge된 `Map_0`을 `server-config/game-config.json` runtime map의 값 기준으로 사용합니다.
- Client/server map artifact 공유나 자동 동기화는 SL-92 범위 밖에 두고, client grid 값을 고정한 exact-grid Go regression으로 현재 drift를 막습니다.
- Circle-vs-tile 기하 계산과 map boundary 판정은 공유하고, Player와 projectile의 blocking predicate만 분리합니다.
- Player는 Wall, Water, map boundary에 충돌하고 Bush를 통과합니다. Projectile은 Wall과 map boundary에 충돌하고 Bush와 Water를 통과합니다.
- Bush visibility와 Water pathfinding/bot AI는 client 또는 bot 후속 범위로 남깁니다.

결과:

- Runtime과 OpenAPI/AsyncAPI `MapData`는 `0=Ground`, `1=Wall`, `2=SpawnPoint`, `3=Bush`, `4=Water`를 같은 값으로 사용합니다.
- REST room response와 WebSocket Ready event가 client `Map_0`의 Bush/Water tile을 JSON number array로 전달합니다.
- Shared map artifact, client rendering, visibility, pathfinding, bot AI, multi-map은 추가하지 않습니다.

## ADR-0028: SL-86 Match Mode 선택은 Room-local Config로 고정

상태: 승인됨

맥락: ADR-0019는 기본 1v1만 활성화해 mode/team boundary를 먼저 만들었고, ADR-0020은 match capacity와 map/debug capacity를 분리했습니다. SL-86은 `duel_1v1`, `solo`, `team`을 실제 matchmaking 선택지로 열어야 합니다. Store의 global selected mode를 매 lifecycle 단계에서 다시 읽으면 이미 생성된 room의 capacity, Ready assignment, simulation, GameEnd가 나중의 default나 다른 request에 따라 달라질 수 있습니다.

결정:

- `server-config/game-config.json`은 `mode.default = duel_1v1`과 세 canonical `mode.catalog` entry를 소유합니다.
- `POST /matchmaking/join`은 optional `gameMode`를 받습니다. Body 없음, 빈 object, 빈 문자열은 기존 client 호환을 위해 default duel로 처리하고, unknown non-empty mode는 `invalid_game_mode`, malformed JSON은 `invalid_request`로 거부합니다.
- Store는 request mode를 catalog에서 canonical `GameConfig`로 한 번 선택하고 같은 selected mode의 waiting room만 재사용합니다.
- 새 room은 선택된 `gameConfig`를 immutable하게 소유합니다. Match capacity, team/slot, Ready quorum과 payload, simulation State, gameplay tick rate, GameEnd calculator는 모두 `room.gameConfig`만 사용합니다.
- Store의 `gameConfig`는 catalog와 새 debug/matchmaking room의 default source로만 남고 이미 생성된 room의 gameplay 판단에는 사용하지 않습니다.
- Join response의 top-level `gameMode`와 nested `room.gameMode`는 같은 selected ID를 required field로 반환합니다.
- `room.maxPlayers`와 `room.map.maxPlayers`는 계속 map/debug capacity 6을 뜻합니다. Selected mode의 match size 2/6/6과 합치지 않습니다.
- `friendlyFire`와 `teamBehavior`는 server-only catalog metadata입니다. SL-86은 projectile friendly-fire 판정이나 mode별 새 GameEnd rule, WebSocket message shape를 추가하지 않습니다.

결과:

- Duel, solo, team request는 서로 waiting room을 공유하지 않으며 같은 mode request만 같은 pool에서 합쳐집니다.
- Room이 생성된 뒤에는 Store default와 무관하게 lifecycle 전체가 하나의 canonical selected config를 사용합니다.
- No-body client는 계속 duel 1v1로 동작하고 새 client는 REST `gameMode`로 선택과 응답을 명시적으로 확인할 수 있습니다.
- ADR-0019의 “duel만 활성” 결정은 이 ADR로 확장되고, ADR-0020의 map/debug capacity 분리와 assignment 단일 source 원칙은 유지됩니다.

## ADR-0029: SL-87 Ready Quorum은 Room-local Mode Config를 따른다

상태: 승인됨

맥락: SL-86은 `duel_1v1`, `solo`, `team`의 waiting pool과 room-local selected config를 제공합니다. 기존 Ready state machine은 required count를 받을 수 있지만 실제 6 WebSocket, 6 human ACK, duplicate ACK, single-start behavior가 end-to-end로 고정되지 않았습니다. 또한 5x5 StaticMap의 preferred fallback 가운데 center `(2,2)`가 Wall이라 다섯 번째 player가 blocking tile에서 시작할 수 있었습니다.

결정:

- `duel_1v1`은 2명, `solo`와 `team`은 6명의 human player와 서로 다른 WebSocket session을 required quorum으로 사용합니다.
- Room의 selected `GameConfig`가 required count와 team/slot/spawn의 유일한 기준입니다.
- Required client가 모두 attach된 뒤 같은 Ready payload를 보내고, `readyPlayers map[string]bool`에 required player identity가 모두 들어온 뒤 countdown을 한 번 시작합니다.
- Duplicate ACK는 idempotent하고 `starting`, `started`, countdown ticker, gameplay ticker를 추가로 만들지 않습니다.
- `attachClientSession`은 `room.mu` 아래 matched/all-attached 조건으로 Ready를 전이하고, `markClientReady`는 current expected session과 loading/all-ready 조건을 확인한 뒤 `startMatchCountdownLocked`를 호출합니다. Quorum helper와 `startMatchCountdownLocked` 자체에는 잠금이나 재진입 guard가 없으므로 caller가 이 조건을 소유합니다.
- Countdown worker는 current ticker identity와 `starting`을 확인합니다. `startRoomLocked`는 `room.mu` 아래 state/ticker nil guard로 gameplay state와 ticker를 room당 하나만 생성합니다.
- Fallback spawn candidate는 player collision policy를 재사용해 Wall과 Water를 제외하고 Ground와 Bush를 허용합니다. Passable candidate가 남아 있는 동안 spawn position은 중복하지 않습니다.
- Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 추가하지 않습니다. Start 전 실제 disconnect는 기존 pre-start cancel을 유지합니다.
- Solo/Team GameEnd와 elimination rule은 별도 issue 범위로 남깁니다.

결과:

- Solo와 Team은 6개의 실제 WebSocket과 6개의 human Ready ACK 없이는 시작하지 않습니다.
- Ready assignment와 첫 gameplay snapshot은 같은 room-local config와 spawn 결과를 사용합니다.
- Config fallback에서도 여섯 player가 Wall/Water가 아닌 unique spawn으로 시작하고 Bush는 passable candidate로 유지됩니다.
- 기존 duel 2-player Ready/countdown/start wire behavior는 유지됩니다.
