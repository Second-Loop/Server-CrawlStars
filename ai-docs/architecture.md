# Architecture

## 단계

이 레포지토리는 부트스트랩을 마치고 E1 서버 권위 코어 루프 준비 단계로 이동하고 있습니다. 서버 architecture는 아직 의도적으로 작게 유지합니다.

## 현재 구조

```text
cmd/server
  process entrypoint

internal/health
  health status model
  HTTP health handler

internal/docs
  server-hosted OpenAPI/AsyncAPI raw specs
  server-hosted human-readable docs UI

internal/rooms
  E1 debug room lifecycle store
  REST room handler
  WebSocket room connection handler
  per-room 30Hz tick loop

internal/simulation
  transport-independent simulation domain model
  manual Step(inputs) -> Snapshot contract
  E1 static tile map movement and wall collision
```

현재 서버는 로컬 및 CI 검증을 위한 `/health` endpoint, E1 개발/검증용 room lifecycle REST endpoint, E1 WebSocket room endpoint, server-hosted API docs endpoint를 노출합니다. `internal/simulation`은 REST, WebSocket, room lifecycle, matching을 모르는 순수 domain package입니다. 이 package는 E1 기준 static tile map, movement input, wall collision, attack skeleton을 처리합니다. `internal/rooms`는 E1 debug API용 in-memory room store와 transport adapter이며, persistence, matchmaking queue, generic scheduler, production gameplay contract를 구현하지 않습니다.

## Runtime 배포 구조

초기 Oracle VM runtime은 의도적으로 단순합니다.

```text
GitHub Actions
  linux/amd64 tarball build
  GitHub artifact와 Release asset publish

Oracle VM
  Release asset pull
  /opt/crawl-stars-server/releases/<sha> 아래에 immutable release 저장
  /opt/crawl-stars-server/current 전환
  systemd로 /opt/crawl-stars-server/current/crawl-stars-server 실행

Cloudflare
  tolerblanc.com public HTTPS 종료
  Cloudflare Tunnel로 api-crawlstars.tolerblanc.com을 127.0.0.1:8080에 route
  Cloudflare Tunnel로 tolerblanc.com을 local Caddy 127.0.0.1:8081에 route
```

systemd unit은 `SERVER_ADDR=127.0.0.1:8080`을 설정합니다. Public exposure path는 Cloudflare Tunnel입니다. Caddy는 apex hello page를 위한 local-only 용도입니다. Tailscale, Docker, Kubernetes, dashboard는 현재 범위 밖입니다.

## 가까운 방향

다음 architecture 작업은 구현 전에 첫 vertical slice를 정의해야 합니다.

- process model
- protocol boundary
- room lifecycle vocabulary
- validation 및 test strategy
- observability basics

첫 slice가 선택되기 전에 game architecture를 과도하게 일반화하지 않습니다.

## E1 Simulation Core Boundary

E1 server-authoritative core는 `internal/simulation.State`가 소유합니다. 현재 계약은 수동 호출 가능한 `Step(inputs []InputCommand) Snapshot`입니다.

이 package가 정의하는 최소 domain vocabulary는 다음과 같습니다.

- `Tick`
- `PlayerID`
- `Team` / `Slot`
- `Vector2`
- `InputCommand`
- `PlayerData`
- `ProjectileID`
- `ProjectileData`
- `ProjectileType`
- `MapData`
- `TileType`
- `Snapshot`

`Step`은 transport-independent contract입니다. REST handler, WebSocket connection, room lifecycle, matching queue는 이 package 안으로 들어오지 않습니다.

SL-39 기준 movement/collision model은 다음과 같습니다.

- Static `MapData` tile grid fixture를 사용할 수 있습니다.
- `MapData`는 client prototype의 `width`, `height`, `index`, `maxPlayers`, `map` 의미를 서버 도메인 이름으로 고정합니다.
- `TileType`은 `TileGround = 0`, `TileWall = 1`, `TileSpawnPoint = 2`로 client `MapData.TileType` 의미와 맞춥니다.
- 좌표계는 client `MapHelper`와 맞춰 `TileSize = 1.2`를 사용하고, tile `(0, 0)`은 centered map의 좌상단 world position입니다.
- `TileWall`은 tile-aligned rectangle wall입니다.
- `PlayerData`는 `Pos`, `Speed`, `Radius`를 사용하며 기본값은 client `BasePlayerData`와 맞춰 `Speed = 2`, `Radius = 0.5`입니다.
- `InputCommand.MoveDir`은 client `PlayerData.MoveDir`와 같은 의미의 이동 방향입니다.
- Movement는 30Hz tick에서 `MoveDir * Speed * TickDuration`으로 계산하고, client physics처럼 X축과 Y축을 분리해 적용합니다.
- Next position이 wall 또는 map 밖과 충돌하면 해당 axis movement만 무시하고 이전 위치를 유지합니다.
- Player circle이 wall rectangle에 닿기만 해도 collision으로 처리합니다.
- Invalid/non-finite movement input은 state를 오염시키지 않고 무시합니다.
- Player-player collision은 E1 범위에서 제외합니다.

SL-40 기준 attack skeleton model은 다음과 같습니다.

- `InputCommand`는 `AttackDir`와 `PressedAttack`을 받습니다.
- `PlayerData`는 client `PlayerData`와 같은 의미의 `MoveDir`, `AttackDir`, `PressedAttack` field를 보존합니다.
- `PressedAttack = true`이고 `AttackDir`가 zero vector가 아니면 같은 tick 안에서 `ProjectileData` skeleton을 생성합니다.
- Client simulator 흐름과 맞춰 player movement/collision을 먼저 적용하고, 새 projectile은 이동 후 player `Pos`에서 생성합니다.
- `Snapshot`은 `Projectiles []ProjectileData`를 포함합니다.
- `ProjectileData`는 client `ProjectileData`와 같은 의미의 `Id`, `OwnerId`, `Pos`, `Dir`, `Speed`, `Damage`, `Radius`, `Type`, `IsDestroyed` field를 wire JSON에 노출합니다.
- Projectile 기본값은 client `BaseProjectile`과 맞춰 `Speed = 13`, `Damage = 10`, `Radius = 0.3`입니다.
- SL-40의 `Damage` field는 data skeleton 값일 뿐이며 피격, 체력, 사망, 리스폰, 점수 계산은 하지 않습니다.

SL-53 기준 projectile movement/destroy model은 다음과 같습니다.

- 기존 projectile은 tick마다 `Dir * Speed * TickDuration` 기준으로 이동합니다.
- 새 projectile은 생성된 tick의 snapshot에는 생성 위치로 보이고, 다음 tick부터 이동합니다.
- Projectile circle이 wall tile 또는 map boundary에 닿거나 밖으로 나가면 `IsDestroyed = true`가 됩니다.
- Destroyed projectile은 snapshot에 남지만 이후 tick에서 위치가 변하지 않습니다.
- Projectile-player collision, 피격, HP, 사망, 리스폰, 점수 계산은 후속 티켓 범위입니다.

## E1 Room REST Debug API Boundary

SL-41 기준 room REST API는 E1 개발/검증용 debug surface입니다.

- `GET /rooms`: active room list를 반환합니다.
- `POST /rooms`: room을 생성합니다. Active room cap은 5개입니다.
- `GET /rooms/{roomID}`: room detail과 latest snapshot summary를 반환합니다.
- `POST /rooms/{roomID}/players`: 서버가 `playerID`, `team`, `slot`을 발급합니다.
- `POST /rooms/{roomID}/start`: player가 1명 이상이면 room status를 `started`로 바꿉니다.
- 0명 room start, room cap 초과, missing room은 JSON error response로 반환합니다.
- Latest snapshot summary는 debug 요약이며, 현재는 `tick`, `playerCount`, `projectileCount`만 포함합니다.
- 이 API는 실제 Unity gameplay client가 장기 의존할 정식 contract가 아닙니다.

## SL-49 Simple Matchmaking Boundary

SL-49 기준 server는 Unity client가 room/player 발급과 WebSocket path를 한 번에 받을 수 있도록 `POST /matchmaking/join`을 제공합니다.

- Waiting room 중 `simulation.StaticMapFixture().MaxPlayers` 기준 여유가 있는 room을 찾습니다.
- 여유 room이 없으면 새 waiting room을 만듭니다.
- Player 발급은 `/rooms/{roomID}/players`와 같은 `playerID`, `team`, `slot` 규칙을 사용합니다.
- Room player count가 2가 되는 join에서 room simulation을 자동 start합니다.
- Matchmaking path는 이미 `started`인 room에 late join하지 않습니다.
- Response는 `room`, `player`, `webSocketPath`를 포함합니다.
- 현재 cap은 fixture 기준 6명이며, 10명 확장은 후속 issue 범위입니다.
- Matching algorithm, auth, persistence, production queue, scheduler/runner/dashboard는 추가하지 않습니다.

## E1 WebSocket Room Boundary

SL-42 기준 WebSocket endpoint는 E2 client integration을 준비하는 E1 server contract surface입니다.

- Endpoint는 `GET /rooms/{roomID}/players/{playerID}` WebSocket upgrade입니다.
- WebSocket 연결은 REST에서 발급된 room/player만 허용합니다.
- Unknown room/player는 upgrade 전에 JSON error response로 거부합니다.
- 같은 room/player의 duplicate connection은 새 연결만 거부하고 기존 연결은 유지합니다.
- Room이 `waiting`이면 WebSocket 연결과 input 수신은 허용하지만 gameplay snapshot broadcast는 하지 않습니다.
- Room이 `started`가 되면 per-room ticker가 `TickRate = 30` 기준으로 `simulation.State.Step`을 호출합니다.
- Input이 없어도 매 tick snapshot message를 broadcast합니다.
- WebSocket input은 client `PlayerData`와 맞춰 `MoveDir`, `AttackDir`, `PressedAttack` field를 사용하고, Unity `Vector2` 값은 `x`, `y`로 직렬화합니다.
- WebSocket snapshot wrapper는 `Type`, `Snapshot` field를 사용하며, snapshot 내부 `PlayerData`/`ProjectileData` wire field는 client code 이름과 맞춥니다.
- Invalid input payload는 연결을 끊지 않고 `{"Type":"error","Error":...}` message를 보낸 뒤 해당 input만 무시하며 snapshot stream을 유지합니다.
- WebSocket adapter는 `internal/simulation`을 호출하지만, `internal/simulation`은 WebSocket package를 import하지 않습니다.
- 이 tick loop는 SL-42 room-local gameplay loop이며, generic scheduler/runner/orchestration framework가 아닙니다.

## E1 Room TTL Cleanup Boundary

SL-43 기준 `internal/rooms`는 in-memory E1 debug room이 public 환경에서 무한히 쌓이지 않도록 Store 진입점에서 cleanup을 수행합니다.

- Waiting room idle TTL은 10분입니다.
- Started room에서 모든 WebSocket client가 disconnect되면 5분 뒤 cleanup합니다.
- Room 생성 후 1시간 hard lifetime을 적용합니다.
- Connected client가 있으면 waiting idle TTL과 all-disconnected TTL로 즉시 삭제하지 않습니다.
- Cleanup은 REST/WS/tick 진입점에서 fake clock으로 검증 가능한 bounded behavior로 구현하며, generic scheduler, runner, persistence, dashboard를 추가하지 않습니다.

## E1 API Documentation Boundary

SL-47 기준 API documentation은 source spec과 hosted docs UI로 나뉩니다.

- Source spec은 `api/openapi.yaml`, `api/asyncapi.yaml`입니다.
- `docs-ui` build는 dependency-free Node script로 source spec을 복사하고 `internal/docs/api/`, `internal/docs/static/`에 embed 대상 파일을 생성합니다.
- Running server는 `GET /openapi`, `GET /asyncapi`, `GET /openapi.yaml`, `GET /asyncapi.yaml`을 노출합니다.
- REST docs UI는 Swagger UI CDN wrapper로, WebSocket docs UI는 repository-owned static HTML/CSS로 생성하고 server binary에 embed합니다.
- 생성된 embed 파일은 commit하지 않으며 `make ci`와 GitHub Actions가 build stage에서 재생성합니다.
- 이 문서화 작업은 E1 debug contract를 설명할 뿐, 인증, rate limit, matchmaking, persistence, dashboard를 추가하지 않습니다.
