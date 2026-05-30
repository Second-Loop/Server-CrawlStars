# Protocol Planning

아직 gameplay protocol은 구현되어 있지 않습니다.

## Core Simulation Contract

SL-38에서 network protocol과 분리된 server-authoritative simulation contract를 먼저 추가했습니다.

```text
internal/simulation.State.Step(inputs []InputCommand) Snapshot
```

이 계약은 Go unit test에서 직접 호출합니다. REST endpoint, WebSocket endpoint, matching queue, room lifecycle에 의존하지 않습니다.

현재 snapshot은 tick과 `PlayerData` list를 포함합니다. Input command는 player ID와 `MoveDir` movement direction vector를 담습니다.

SL-39부터 movement는 client prototype 이름과 값을 맞춘 `MoveDir * Speed * TickDuration`으로 계산됩니다. Core simulation은 `MapData` static tile grid 위에서 player circle과 wall rectangle collision을 검사합니다. `TileSize = 1.2`, `TickRate = 30`, default `Speed = 2`, default `Radius = 0.5`를 사용합니다. `TileType` 값은 `Ground = 0`, `Wall = 1`, `SpawnPoint = 2` 의미와 맞춥니다. Player circle이 wall rectangle에 닿기만 해도 collision으로 처리합니다. Collision이 발생하거나 movement vector가 non-finite이면 해당 input은 무시되고 기존 player state가 유지됩니다.

Client `ProjectileData`의 `Speed = 13`, `Damage = 10`, `Radius = 0.3` 기본값은 SL-40 attack skeleton에서 다룹니다. SL-39는 projectile behavior를 구현하지 않습니다.

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
