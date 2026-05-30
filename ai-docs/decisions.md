# Decisions

## ADR-0001: 최소 Go HTTP Server로 시작

Status: Accepted

Context: Gameplay system이 존재하기 전에 CI로 검증 가능한 code가 필요합니다.

Decision: 작은 Go module, `cmd/server` entrypoint, `/health`를 노출하는 `internal/health` package로 시작합니다.

Consequences:

- CI가 format, vet, tests, build를 즉시 검증할 수 있습니다.
- Gameplay architecture를 확정하지 않아도 server executable이 생깁니다.
- 향후 networking decision은 열린 상태로 남깁니다.

## ADR-0002: Symphony 차용 범위는 Workflow Rule로 제한

Status: Accepted

Context: Project는 orchestration infrastructure를 만들지 않고 issue-driven, review-gated collaboration을 원합니다.

Decision: Issue-as-source-of-truth, acceptance criteria, validation, PR review, repository workflow docs만 차용합니다. Scheduler, runner, polling daemon, web dashboard, automatic merge loop는 만들지 않습니다.

Consequences:

- Process가 명시적이고 versioned 상태로 repo에 남습니다.
- Automation은 나중에 Linear-scoped work로 정당화될 때만 추가할 수 있습니다.

## ADR-0003: 초기 Oracle VM Runtime은 systemd 기반 VM Pull CD 사용

Status: Accepted

Context: SL-6은 `main` update 이후 Go server를 위한 작은 deployment path가 필요합니다. VM에는 SSH access와 passwordless sudo가 있지만 Docker, Cloudflare Tunnel, Tailscale, nginx, caddy, required public app port가 없습니다. OCI Security List와 NSG 변경은 issue scope 밖입니다.

Decision: GitHub Actions가 linux/amd64 tarball을 build하고 workflow artifact와 GitHub Release asset을 모두 publish합니다. Oracle VM은 최신 release asset을 pull하고, `/opt/crawl-stars-server/releases/<sha>/` 아래에 install하고, `/opt/crawl-stars-server/current`를 전환하고, systemd service를 restart한 뒤 `http://127.0.0.1:8080/health`를 확인합니다.

Consequences:

- Deployment를 위해 inbound application port가 필요하지 않습니다.
- GitHub Release asset은 public repo 기준 VM pull path를 단순하게 유지합니다.
- Server process는 Docker, PM2, Kubernetes 대신 systemd로 관리합니다.
- Rollback은 `/opt/crawl-stars-server/previous`로 symlink를 되돌리고 systemd restart를 실행하는 방식입니다.

## ADR-0004: HTTPS는 Cloudflare Tunnel로 노출

Status: Accepted

Context: SL-35는 Go server를 VM 내부 private 상태로 유지하면서 public HTTPS hostname을 필요로 합니다. 현재는 OCI public inbound 변경을 피해야 하므로 direct Caddy `80/tcp`, `443/tcp` ingress는 선택하지 않습니다.

Decision: Go server를 `127.0.0.1:8080`에 유지하고 VM에서 Cloudflare Tunnel connector를 실행합니다. Cloudflare는 `api-crawlstars.tolerblanc.com`을 `http://127.0.0.1:8080`으로 route합니다. Apex `tolerblanc.com`은 local Caddy `http://127.0.0.1:8081`로 route하며, Caddy는 최소 hello response를 반환합니다. Public HTTPS는 Cloudflare edge가 소유하고, 이 tunnel-backed setup에서 Caddy는 local-only입니다.

Consequences:

- OCI public inbound는 이 경로에서 application `80/tcp` 또는 `443/tcp`를 필요로 하지 않습니다. Connector가 Cloudflare로 outbound connection을 만듭니다.
- Go server port는 VM firewall, OCI Security Lists, NSG에 열면 안 됩니다.
- Go server가 WebSocket endpoint를 구현하면 WebSocket traffic도 같은 Cloudflare Tunnel hostname을 사용할 수 있습니다.
- Caddy는 systemd로 실행되지만 apex hello page를 위해 `127.0.0.1:8081`에서만 listen합니다.

## ADR-0005: REST는 OpenAPI로, WebSocket Message는 AsyncAPI로 문서화

Status: Accepted

Context: E1에는 room lifecycle, client input, server snapshot flow를 위한 작은 contract surface가 필요합니다. REST endpoint는 읽고 수동 호출하기 쉬워야 하지만, WebSocket gameplay traffic은 Swagger UI가 잘 모델링하지 못하는 bidirectional stream입니다.

Decision: REST API는 OpenAPI 3.x를 사용하고, interactive REST page를 추가할 때 server-hosted UI를 사용합니다. WebSocket channel과 message payload는 AsyncAPI를 사용합니다. OpenAPI는 `ws://` 또는 `wss://` server URL을 참조할 수 있지만, WebSocket input과 snapshot stream의 source of truth는 AsyncAPI입니다.

Consequences:

- REST와 WebSocket contract는 필요한 경우 schema vocabulary를 공유하면서도 독립적으로 발전할 수 있습니다.
- E1 debug API는 승격 전까지 unstable 및 E1-only로 명확히 표시해야 합니다.
- 처음 spec file을 추가하는 implementation issue는 OpenAPI와 AsyncAPI document validation을 함께 추가해야 합니다.
- SL-47 기준 hosted path는 `/openapi`, `/asyncapi`, `/openapi.yaml`, `/asyncapi.yaml`입니다.

## ADR-0006: Simulation Core는 Transport-Independent Step Contract로 시작

Status: Accepted

Context: E1 server work는 REST/WebSocket contract surface를 열기 전에 server-authoritative core loop skeleton을 unit test로 고정해야 합니다. SL-38은 room lifecycle, WebSocket, matching 없이 domain model과 `Step(inputs) -> Snapshot` 경계를 먼저 정의합니다.

Decision: `internal/simulation` package에 최소 domain vocabulary와 `State.Step(inputs []InputCommand) Snapshot` 계약을 둡니다. 이 package는 HTTP, WebSocket, room manager, matching queue를 import하지 않습니다. SL-38에서는 tick 증가와 snapshot 생성만 고정하고, movement/collision, attack skeleton, REST room lifecycle, WebSocket runner는 후속 E1 하위 티켓에서 같은 계약 위에 얹습니다.

Consequences:

- Core simulation은 WebSocket 없이 Go unit test로 직접 검증할 수 있습니다.
- Red 1명 + blue 1명 구성은 테스트하되, team slot model은 한 team당 여러 player를 막지 않습니다.
- Network handler는 후속 티켓에서 `Step`을 호출하는 adapter가 되어야 하며, simulation package가 transport detail을 알면 안 됩니다.

## ADR-0007: E1 Movement Collision은 Tile Grid와 Circle-vs-Rectangle으로 고정

Status: Accepted

Context: SL-39는 server core `Step`이 static map fixture, movement input, wall collision을 처리해야 합니다. SL-9 client prototype은 file-based tile map, 30Hz simulator tick, player circle vs wall rectangle collision 방향을 사용했습니다. E1 server는 실제 Unity integration 없이 unit test로 같은 최소 개념을 재현해야 합니다.

Decision: `internal/simulation`은 static `MapData` tile grid를 받는 `Config`를 지원합니다. `MapData`는 client prototype의 `width`, `height`, `index`, `maxPlayers`, `map` 의미를 서버 도메인 이름으로 고정합니다. `TileType`은 `TileGround = 0`, `TileWall = 1`, `TileSpawnPoint = 2`로 client `Ground`, `Wall`, `SpawnPoint` 값과 맞춥니다. `TileSize = 1.2`, `TickRate = 30`, default player `Speed = 2`, default player `Radius = 0.5`를 사용합니다. `TileWall`은 tile-aligned rectangle으로 보고, player는 radius를 가진 circle로 봅니다. `InputCommand.MoveDir`은 client `PlayerData.MoveDir`와 같은 이동 방향이며, movement는 `MoveDir * Speed * TickDuration`으로 계산합니다. Client physics와 맞춰 X축 이동과 Y축 이동은 분리해 collision을 검사합니다. Next position이 wall 또는 map 밖과 충돌하면 해당 axis movement를 무시하고 이전 position을 유지합니다. Player circle이 wall rectangle에 tangent contact만 해도 collision으로 처리합니다. Unknown player input과 invalid/non-finite movement input은 state를 변경하지 않습니다. Client `ProjectileData` 기본값(`Speed = 13`, `Damage = 10`, `Radius = 0.3`)은 SL-40 attack skeleton에서 다루며 SL-39에서는 projectile behavior를 구현하지 않습니다.

Consequences:

- Movement와 wall collision은 WebSocket 없이 unit test로 검증됩니다.
- Server fixture는 client prototype의 이름과 핵심 값을 맞추되, 실제 Unity integration은 후속 티켓에서 adapter로 다룹니다.
- Player-player collision, attack, damage, HP, death, respawn, score는 이 결정에 포함하지 않습니다.

## ADR-0008: E1 Attack Skeleton은 ProjectileData Snapshot State로 시작

Status: Accepted

Context: SL-40은 core `Step` 안에서 일반 공격 입력을 movement/collision과 같은 tick 흐름으로 처리해야 합니다. Client prototype은 `PlayerData.AttackDir`, `PlayerData.PressedAttack`, `ProjectileData` vocabulary를 사용하고, simulator tick에서 player movement 이후 새 projectile을 생성합니다. E1 server는 실제 Unity integration, projectile physics, damage 판정 없이 snapshot에 공격 관련 최소 state를 노출해야 합니다.

Decision: `internal/simulation.InputCommand`는 `AttackDir`와 `PressedAttack`을 받습니다. `PlayerData`는 client field와 같은 의미의 `MoveDir`, `AttackDir`, `PressedAttack`을 보존합니다. `PressedAttack = true`이고 `AttackDir`가 zero vector가 아니면 `Step`은 movement/collision 이후 이동된 player `Pos`에서 `ProjectileData` skeleton을 생성하고 `Snapshot.Projectiles`에 포함합니다. `ProjectileData`는 client `ProjectileData`와 같은 의미의 `ID`, `OwnerID`, `Pos`, `Dir`, `Speed`, `Damage`, `Radius`, `Type`, `IsDestroyed`를 둡니다. 기본 projectile 값은 client `BaseProjectile`과 맞춰 `Speed = 13`, `Damage = 10`, `Radius = 0.3`입니다. `Damage`는 skeleton data field일 뿐이며 SL-40은 projectile movement, projectile-wall collision, projectile-player collision, hit detection, HP, death, respawn, score를 구현하지 않습니다.

Consequences:

- Attack input과 projectile skeleton은 WebSocket 없이 Go unit test로 직접 검증됩니다.
- Snapshot은 player state와 projectile skeleton state를 함께 전달할 수 있습니다.
- Combat result behavior는 후속 Linear issue에서 별도 acceptance criteria와 tests로 추가해야 합니다.

## ADR-0009: E1 Room REST API는 Debug Lifecycle Surface로 제한

Status: Accepted

Context: SL-41은 E1 개발/검증을 위해 room을 직접 만들고, player를 발급하고, start 조건을 확인할 수 있는 REST API가 필요합니다. 이 API는 matching queue나 production gameplay contract가 아니라 WebSocket room flow를 붙이기 전의 수동 검증 surface입니다. Public Cloudflare Tunnel 뒤에서 호출될 수 있으므로 active room cap도 필요합니다.

Decision: `internal/rooms` package에 in-memory room store와 REST handler를 둡니다. Server는 `GET /rooms`, `POST /rooms`, `GET /rooms/{roomID}`, `POST /rooms/{roomID}/players`, `POST /rooms/{roomID}/start`를 노출합니다. Active room cap은 5개입니다. Player 발급은 서버가 `player-*` ID와 red/blue alternating team, team별 slot을 부여합니다. Start는 player가 1명 이상일 때 room status를 `started`로 바꾸며, background scheduler나 tick runner를 시작하지 않습니다. Room detail은 latest snapshot summary를 `tick`, `playerCount`, `projectileCount`로만 제공합니다. REST error response는 `{\"error\":{\"code\",\"message\"}}` 형태의 JSON으로 통일합니다.

Consequences:

- E1 room lifecycle은 curl/httptest로 수동 검증할 수 있습니다.
- Matching queue, persistence, scheduler, runner, production room orchestration은 여전히 제외됩니다.
- Debug API response shape는 정식 gameplay contract로 승격되기 전까지 `ai-docs/api-docs.md`의 E1 debug note를 따라야 합니다.

## ADR-0010: E1 WebSocket은 Room-Local Tick Loop와 Snapshot Broadcast로 제한

Status: Accepted

Context: SL-42는 REST로 생성한 room/player를 WebSocket에 연결하고, started room에서 30Hz snapshot stream을 검증해야 합니다. E1 scope는 실제 Unity integration demo, production matchmaking, persistence, generic scheduler/runner/orchestration을 포함하지 않습니다. Core simulation은 계속 WebSocket 없이 unit test 가능해야 합니다.

Decision: `internal/rooms`가 `WS /rooms/{roomID}/players/{playerID}` upgrade를 처리합니다. WebSocket implementation은 `nhooyr.io/websocket`을 사용합니다. Room/player validation과 duplicate same player connection rejection은 upgrade 전에 수행합니다. Waiting room은 connection과 input 수신을 허용하지만 snapshot broadcast를 하지 않습니다. Started room은 room-local ticker를 만들고, `simulation.TickRate` 기준 30Hz로 `simulation.State.Step`을 호출해 `{\"Type\":\"snapshot\",\"Snapshot\":...}` message를 connected clients에 broadcast합니다. Client input field는 client `PlayerData`와 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`을 사용하고, Unity `Vector2` 값은 `x`, `y`로 직렬화합니다. Snapshot 내부 `PlayerData`/`ProjectileData` wire field는 `Id`, `Pos`, `MoveDir`, `AttackDir`, `PressedAttack`, `IsDead`, `OwnerId`, `Dir`, `IsDestroyed`처럼 client code 이름을 따릅니다. Invalid input payload는 connection을 끊지 않고 무시합니다.

Consequences:

- Fake client integration test와 fake clock test로 WebSocket behavior를 검증할 수 있습니다.
- `internal/simulation`은 WebSocket dependency를 import하지 않으므로 transport-independent contract가 유지됩니다.
- SL-42 tick loop는 room-local gameplay loop이며, reusable scheduler/runner framework가 아닙니다.
- AsyncAPI/OpenAPI spec file 생성과 hosted docs는 별도 implementation issue에서 다룹니다.

## ADR-0011: E1 Room Cleanup은 Store 진입점 TTL로 제한

Status: Accepted

Context: SL-43은 public debug room API가 무한히 쌓이지 않도록 최소 cleanup을 추가해야 하지만, E1 범위는 persistence, scheduler, runner, dashboard, orchestration framework를 포함하지 않습니다. 또한 invalid input은 WebSocket stream을 깨지 않아야 합니다.

Decision: `internal/rooms.Store`는 fake clock으로 검증 가능한 TTL rule을 Store 진입점에서 적용합니다. Waiting room idle TTL은 10분, started room all-disconnected TTL은 마지막 WebSocket client disconnect 후 5분, hard room lifetime은 생성 후 1시간입니다. Connected client가 있으면 waiting idle TTL과 all-disconnected TTL로 즉시 삭제하지 않습니다. Hard lifetime은 hard cap으로 유지합니다. Invalid JSON input은 `{"Type":"error","Error":{"code":"invalid_input","message":"invalid input"}}` message를 보내고 해당 input만 무시하며, connection과 snapshot stream은 유지합니다. Room/player validation, duplicate connection, room full은 REST 4xx JSON error 또는 WebSocket upgrade 전 JSON error response로 고정합니다.

Consequences:

- Room cleanup은 API/WS/tick activity 시점에 수행되며 별도 scheduler나 persistent storage를 요구하지 않습니다.
- Public debug API exposure risk는 room cap, per-room player cap, TTL로 낮춥니다.
- Invalid input regression은 error message와 이후 snapshot stream을 함께 검증합니다.

## ADR-0012: E1 API Docs는 Server-Hosted No-CDN Static UI로 제공

Status: Accepted

Context: SL-47은 REST raw spec, WebSocket raw spec, human-readable docs를 한 번에 제공해야 합니다. E1 server는 이미 Cloudflare Tunnel 뒤에서 실행되므로 별도 CDN이나 GitHub Pages를 먼저 만들 필요가 없습니다. Clean build에서 docs UI를 재생성할 수 있어야 하고, generated assets가 source of truth처럼 commit되면 spec drift가 생길 수 있습니다.

Decision: Source spec은 `api/openapi.yaml`, `api/asyncapi.yaml`에 둡니다. `docs-ui` build는 source spec을 parse하고 `internal/docs/api/`, `internal/docs/static/` embed 대상 파일을 생성합니다. Server는 `GET /openapi`, `GET /asyncapi`로 human-readable static UI를, `GET /openapi.yaml`, `GET /asyncapi.yaml`로 raw spec을 제공합니다. Generated embed assets는 commit하지 않고 `make ci`, CI, CD build stage에서 재생성합니다. UI는 no-CDN static HTML/CSS로 유지합니다.

Consequences:

- Running server 하나만으로 API docs와 raw spec을 확인할 수 있습니다.
- Clean checkout에서 Go compile/test/build 전 `make docs-build` 또는 `make ci`가 필요합니다.
- CI/CD는 Node.js setup과 docs build를 Go validation/build 전에 수행해야 합니다.
- 이 결정은 docs delivery만 다루며, 인증, rate limit, matching queue, persistence, dashboard는 추가하지 않습니다.
