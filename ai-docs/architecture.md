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

internal/rooms
  E1 debug room lifecycle store
  REST room handler

internal/simulation
  transport-independent simulation domain model
  manual Step(inputs) -> Snapshot contract
  E1 static tile map movement and wall collision
```

현재 서버는 로컬 및 CI 검증을 위한 `/health` endpoint와 E1 개발/검증용 room lifecycle REST endpoint를 노출합니다. `internal/simulation`은 REST, WebSocket, room lifecycle, matching을 모르는 순수 domain package입니다. 이 package는 E1 기준 static tile map, movement input, wall collision을 처리합니다. `internal/rooms`는 E1 debug API용 in-memory room store이며, persistence, matchmaking queue, scheduler, runner, production gameplay contract를 구현하지 않습니다.

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
- `ProjectileData`는 client `ProjectileData`와 같은 의미의 `ID`, `OwnerID`, `Pos`, `Dir`, `Speed`, `Damage`, `Radius`, `Type`, `IsDestroyed` field를 둡니다.
- Projectile 기본값은 client `BaseProjectile`과 맞춰 `Speed = 13`, `Damage = 10`, `Radius = 0.3`입니다.
- SL-40의 `Damage` field는 data skeleton 값일 뿐이며 피격, 체력, 사망, 리스폰, 점수 계산은 하지 않습니다.
- Existing projectile movement, projectile-wall collision, projectile-player collision, projectile destroy lifecycle은 후속 티켓 범위입니다.

## E1 Room REST Debug API Boundary

SL-41 기준 room REST API는 E1 개발/검증용 debug surface입니다.

- `GET /rooms`: active room list를 반환합니다.
- `POST /rooms`: room을 생성합니다. Active room cap은 5개입니다.
- `GET /rooms/{roomID}`: room detail과 latest snapshot summary를 반환합니다.
- `POST /rooms/{roomID}/players`: 서버가 `playerID`, `team`, `slot`을 발급합니다.
- `POST /rooms/{roomID}/start`: player가 1명 이상이면 room status를 `started`로 바꿉니다.
- 0명 room start, room cap 초과, missing room은 JSON error response로 반환합니다.
- Latest snapshot summary는 debug 요약이며, 현재는 `tick`, `playerCount`, `projectileCount`만 포함합니다.
- Room start는 scheduler나 background loop를 시작하지 않습니다.
- 이 API는 실제 Unity gameplay client가 장기 의존할 정식 contract가 아닙니다.

WebSocket integration은 후속 E1 하위 티켓에서 추가합니다.
