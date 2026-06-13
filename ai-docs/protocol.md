# Protocol Planning

아직 gameplay protocol은 구현되어 있지 않습니다.

## Core Simulation Contract

SL-38에서 network protocol과 분리된 server-authoritative simulation contract를 먼저 추가했습니다.

```text
internal/simulation.State.Step(inputs []InputCommand) Snapshot
```

이 계약은 Go unit test에서 직접 호출합니다. REST endpoint, WebSocket endpoint, matching queue, room lifecycle에 의존하지 않습니다.

현재 snapshot은 tick, `PlayerData` list, `ProjectileData` list를 포함합니다. Input command는 player ID, `MoveDir` movement direction, `AttackDir` attack direction, `PressedAttack` attack trigger를 담습니다.

SL-39부터 movement는 client prototype 이름과 값을 맞춘 `MoveDir * Speed * TickDuration`으로 계산됩니다. Core simulation은 `MapData` static tile grid 위에서 player circle과 wall rectangle collision을 검사합니다. `TileSize = 1.2`, `TickRate = 30`, default `Speed = 2`, default `Radius = 0.5`를 사용합니다. `TileType` 값은 `Ground = 0`, `Wall = 1`, `SpawnPoint = 2` 의미와 맞춥니다. Player circle이 wall rectangle에 닿기만 해도 collision으로 처리합니다. Collision이 발생하거나 movement vector가 non-finite이면 해당 input은 무시되고 기존 player state가 유지됩니다.

SL-40부터 attack pressed input은 같은 `Step` tick에서 처리됩니다. `PressedAttack = true`이고 `AttackDir`가 zero vector가 아니면 `ProjectileData` skeleton이 snapshot에 추가됩니다. Client simulator 순서에 맞춰 player movement/collision을 먼저 처리하고, 새 projectile은 이동 후 player `Pos`에서 생성합니다. `ProjectileData`의 기본값은 client `BaseProjectile`과 맞춰 `Speed = 13`, `Damage = 10`, `Radius = 0.3`입니다.

SL-53부터 기존 projectile은 다음 `Step` tick부터 `Dir * Speed * TickDuration` 기준으로 이동합니다. 새 projectile은 생성된 tick의 snapshot에는 생성 위치로 보이고, 다음 tick부터 이동합니다. Projectile circle이 wall tile 또는 map boundary에 닿거나 밖으로 나가면 `IsDestroyed = true`로 표시되며, destroyed projectile은 이후 tick에서 더 이동하지 않습니다.

SL-54부터 `PlayerData.HP`는 현재 체력 값으로 snapshot에 포함됩니다. 기본 HP는 `100`이고 projectile hit은 `Damage`만큼 target HP를 줄입니다. Projectile circle이 owner가 아닌 live player circle과 겹치면 hit으로 처리하며, hit projectile은 `IsDestroyed = true`가 됩니다. HP가 `0` 이하가 되면 `HP = 0`, `IsDead = true`로 표시합니다. Owner 본인은 자기 projectile의 hit target에서 제외합니다.

SL-54는 projectile-player collision, HP 감소, death snapshot만 정의합니다. Respawn, score, win/loss, friendly-fire policy 확장, character-specific stats는 protocol behavior로 구현하지 않습니다.

## E2-3 Server Validation Scenario

2인 server-only 검증은 REST로 room/player를 만들고, WebSocket 두 개가 같은 snapshot stream을 받는지 확인하는 흐름입니다. 자동 회귀 테스트는 `internal/rooms`의 2-player WebSocket test가 담당하고, 사람이 수동으로 볼 때는 다음 순서로 확인합니다.

1. `POST /matchmaking/join`을 두 번 호출하거나 `/rooms` debug API로 room 1개와 player 2명을 만듭니다.
2. 두 응답의 `webSocketPath` 또는 `WS /rooms/{roomID}/players/{playerID}` 경로로 WebSocket connection 두 개를 엽니다.
3. 한 player가 `{"MoveDir":{"x":1,"y":0}}` input을 보내면 다음 snapshot tick에서 두 connection 모두 같은 player `Pos` 변화를 받아야 합니다.
4. Red spawn은 map `(1, 1)`, blue spawn은 map `(3, 3)`입니다. Center wall 때문에 red에서 blue로 대각선 직선 공격하면 wall collision이 먼저 납니다. Hit flow를 수동 확인할 때는 red를 오른쪽으로 움직여 blue와 같은 column에 맞춘 뒤 `{"AttackDir":{"x":0,"y":-1},"PressedAttack":true}`를 보냅니다.
5. Projectile이 blue player circle과 겹치는 tick에서 두 connection 모두 같은 projectile `IsDestroyed: true`, blue `HP` 감소 snapshot을 받아야 합니다.
6. 같은 공격 flow를 반복해 blue HP가 `0`이 되면 두 connection 모두 blue `HP: 0`, `IsDead: true` snapshot을 받아야 합니다.
7. Invalid JSON input은 `{"Type":"error","Error":{"code":"invalid_input","message":"invalid input"}}`을 보내고, 다음 valid tick의 snapshot stream은 계속 유지되어야 합니다.

이 시나리오는 server behavior 확인용입니다. Unity client prediction/interpolation, production matchmaking queue, respawn, score, win/loss는 포함하지 않습니다.

## Shared Constants Boundary

현재 server simulation은 다음 값을 protocol behavior로 사용합니다.

- `TickRate = 30`
- `TileSize = 1.2`
- `DefaultPlayerSpeed = 2`
- `DefaultPlayerRadius = 0.5`
- `DefaultPlayerHP = 100`
- `DefaultProjectileSpeed = 13`
- `DefaultProjectileDamage = 10`
- `DefaultProjectileRadius = 0.3`
- `StaticMapFixture().MaxPlayers = 6`

SL-56은 이 값을 문서에 기록만 합니다. Client/server 공통 상수 파일, shared package, asset-driven config, 10-player cap 변경은 SL-30에서 별도 설계와 acceptance criteria로 다룹니다.

## E1 WebSocket Room Contract

SL-42부터 다음 E1 debug WebSocket endpoint를 제공합니다.

```text
WS /rooms/{roomID}/players/{playerID}
```

연결 조건:

- `roomID`는 `POST /rooms`로 생성된 room이어야 합니다.
- `playerID`는 `POST /rooms/{roomID}/players`로 발급된 player여야 합니다.
- 같은 room/player에 이미 WebSocket connection이 있으면 새 connection은 거부합니다.
- room이 아직 `started`가 아니어도 connection과 input 수신은 허용합니다.
- room이 `started`가 된 뒤에만 gameplay snapshot을 broadcast합니다.

Client input message는 E1 debug JSON입니다. Field 이름은 client `PlayerData`와 맞춰 `MoveDir`, `AttackDir`, `PressedAttack`을 사용하고, Unity `Vector2` 값은 `x`, `y`를 사용합니다.

```json
{
  "MoveDir": {
    "x": 1,
    "y": 0
  },
  "AttackDir": {
    "x": 0,
    "y": 1
  },
  "PressedAttack": false
}
```

Server snapshot message는 다음 wrapper를 사용합니다. Snapshot 안의 client-facing data field는 client code의 이름을 따라 `Id`, `Pos`, `MoveDir`, `AttackDir`, `PressedAttack`, `HP`, `IsDead`, `OwnerId`, `Dir`, `IsDestroyed`처럼 직렬화합니다.

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "Tick": 1,
    "Players": [
      {
        "Id": "player-1",
        "Pos": {
          "x": -1.2,
          "y": 1.2
        },
        "MoveDir": {
          "x": 0,
          "y": 0
        },
        "AttackDir": {
          "x": 0,
          "y": 0
        },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 100,
        "PressedAttack": false,
        "IsDead": false
      }
    ],
    "Projectiles": []
  }
}
```

Invalid input payload는 connection을 끊지 않고 error message를 보낸 뒤 해당 input만 무시합니다.

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

Snapshot stream은 다음 valid tick에서도 계속 유지되어야 합니다.

SL-43 기준 room cleanup rule은 다음과 같습니다.

- waiting room idle TTL은 10분입니다.
- started room에서 모든 WebSocket client가 disconnect되면 5분 뒤 cleanup 대상이 됩니다.
- room 생성 후 1시간 hard lifetime이 지나면 cleanup 대상이 됩니다.
- connected client가 있으면 waiting idle TTL과 all-disconnected TTL로 즉시 삭제하지 않습니다.

## Documentation Policy

E1 REST API는 OpenAPI 3.x로 문서화합니다. E1 WebSocket message contract는 AsyncAPI로 문서화합니다. SL-47부터 running server가 human-readable docs UI와 raw spec을 함께 제공합니다.

OpenAPI는 `ws://` 또는 `wss://` server URL을 언급할 수 있지만, client input과 server snapshot broadcast 같은 bidirectional WebSocket message stream의 source of truth는 AsyncAPI입니다.

Server-hosted documentation path:

```text
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
```

전체 documentation policy는 `ai-docs/api-docs.md`를 참고합니다.

## 현재 Endpoint

```text
GET /health
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
POST /matchmaking/join
GET /rooms
POST /rooms
GET /rooms/{roomID}
POST /rooms/{roomID}/players
POST /rooms/{roomID}/start
WS /rooms/{roomID}/players/{playerID}
```

`POST /matchmaking/join`은 client-facing simple matching endpoint입니다. Response의 `webSocketPath`는 같은 server origin에서 연결할 WebSocket path를 제공합니다.

Response:

```json
{
  "status": "ok",
  "service": "server-crawlstars"
}
```

## 향후 계획 주제

- HTTP와 WebSocket 책임 분리
- OpenAPI REST contract shape
- AsyncAPI WebSocket message contract shape
- authentication boundary
- room 생성 및 join flow
- match state snapshot
- client input message
- server tick model
- reconciliation 및 prediction 가정
- versioning strategy

첫 vertical slice가 Linear에서 승인되기 전에는 protocol message를 구현하지 않습니다.
