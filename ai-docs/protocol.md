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

SL-40은 attack/projectile skeleton만 정의합니다. Projectile movement, projectile collision, hit detection, HP, death, respawn, score는 protocol behavior로 구현하지 않습니다.

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

Server snapshot message는 다음 wrapper를 사용합니다. Snapshot 안의 client-facing data field는 client code의 이름을 따라 `Id`, `Pos`, `MoveDir`, `AttackDir`, `PressedAttack`, `IsDead`, `OwnerId`, `Dir`, `IsDestroyed`처럼 직렬화합니다.

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
        "PressedAttack": false,
        "IsDead": false
      }
    ],
    "Projectiles": []
  }
}
```

Invalid input payload는 connection을 끊지 않고 무시합니다. Snapshot stream은 다음 valid tick에서도 계속 유지되어야 합니다.

## Documentation Policy

E1 REST API는 OpenAPI 3.x로 문서화하고, interactive page를 추가할 때 Swagger UI로 render합니다. E1 WebSocket message contract는 AsyncAPI로 문서화합니다.

OpenAPI는 `ws://` 또는 `wss://` server URL을 언급할 수 있지만, client input과 server snapshot broadcast 같은 bidirectional WebSocket message stream의 source of truth는 AsyncAPI입니다.

전체 documentation policy는 `ai-docs/api-docs.md`를 참고합니다.

## 현재 Endpoint

```text
GET /health
```

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
