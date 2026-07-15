# API 문서화 기준

REST 계약은 OpenAPI, WebSocket message 계약은 AsyncAPI로 문서화합니다. 사람이 읽는 요약은 `ai-docs/api-reference.md`에 둡니다.

## 원칙

- REST endpoint와 bounded request/response는 `api/openapi.yaml`에 기록합니다.
- WebSocket channel과 message payload는 `api/asyncapi.yaml`에 기록합니다.
- `/rooms` debug API는 정식 gameplay contract가 아니라 development surface입니다.
- Debug API를 stable client contract로 승격하려면 별도 Linear issue와 문서 업데이트가 필요합니다.
- 계약 변경이 있으면 `make docs-build` 또는 `make ci` 결과를 PR에 남깁니다.

## Hosted docs

서버는 다음 path를 제공합니다.

```text
GET /openapi
GET /asyncapi
GET /openapi.yaml
GET /asyncapi.yaml
```

- `/openapi`: Swagger UI wrapper
- `/asyncapi`: repository-owned static HTML
- `*.yaml`: raw spec

Docs build는 `docs-ui`의 dependency-free Node script가 담당합니다. 생성물은 `internal/docs/api/`, `internal/docs/static/`에 만들어지고 Go `embed`로 binary에 포함됩니다. 생성물은 git에 commit하지 않습니다.

## 현재 REST surface

```text
GET /health
POST /matchmaking/join
GET /rooms
POST /rooms
DELETE /rooms
GET /rooms/{roomID}
DELETE /rooms/{roomID}
POST /rooms/{roomID}/players
POST /rooms/{roomID}/start
```

`POST /matchmaking/join`은 Unity client가 room/player/WebSocket path를 한 번에 받을 수 있게 하는 simple connector입니다. Production queue, rating, auth, persistence는 없습니다. 두 번째 player가 들어오면 room은 matched 상태로 잠기지만 REST response shape와 `room.status: waiting`은 유지합니다.

`DELETE /rooms`와 `DELETE /rooms/{roomID}`는 테스트 중 active room cap을 즉시 회복하기 위한 debug API입니다. Room response에는 server simulation이 쓰는 `map` 데이터와 `latestSnapshot` summary가 포함됩니다. 외부 응답의 `map` row는 Base64 문자열이 아니라 JSON number array입니다.

Match Ready event, ready ACK, 5초 server-internal countdown, start 전 cancel은 WebSocket 계약에서 다룹니다. 새 REST polling이나 SSE를 늘리지 않고 Ready event와 기존 gameplay WebSocket wrapper인 `Type: snapshot` 안의 `Snapshot.status`/`Snapshot.countdown`을 사용합니다. `starting`은 countdown 시작 신호로 1번만 보냅니다.

## 현재 WebSocket surface

```text
WS /rooms/{roomID}/players/{playerID}
```

Input field는 Unity prototype 이름을 따릅니다.

```json
{
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false
}
```

서버는 유한한 `MoveDir`의 크기가 `1` 이하이면 그대로 보존하고, 더 크면 unit vector로 clamp합니다. Zero가 아닌 유한한 `AttackDir`는 항상 unit vector로 정규화하며, NaN/Inf가 포함된 input은 적용하지 않습니다. Player별 attack budget은 server-only이며 기본 4 charge로 시작해 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. 사망한 player의 input과 zero 방향 또는 소진된 charge의 공격 요청은 거부합니다.

Server message wrapper:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "started",
    "Tick": 1,
    "Players": [],
    "Projectiles": []
  }
}
```

Snapshot의 `Players[].PressedAttack`은 input echo가 아니라 방향, 생존 상태, 남은 charge를 검증한 뒤 서버가 해당 tick의 공격을 승인했는지 나타내는 transient 결과입니다.

Ready event:

```json
{
  "Type": "Ready",
  "Map": {
    "width": 5,
    "height": 5,
    "index": 0,
    "maxPlayers": 6,
    "tileSize": 1.2,
    "map": [[1, 1, 1, 1, 1]]
  },
  "Players": [
    {
      "Id": "player-1",
      "Team": "red",
      "Slot": 0,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    }
  ]
}
```

Ready 예시는 간결함을 위해 5x5 fallback map 기준입니다. 실제 기본 runtime map은 server binary가 embed한 `server-config/game-config.json`의 20x20 map이며, spawn은 `TileSpawnPoint(2)` tile에서 발급됩니다.

Match ready ACK:

```json
{
  "Type": "ready"
}
```

Countdown snapshot:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "starting",
    "countdown": 5,
    "Tick": 0,
    "Players": null,
    "Projectiles": null
  }
}
```

Invalid input:

```json
{
  "Type": "error",
  "Error": {
    "code": "invalid_input",
    "message": "invalid input"
  }
}
```

## Stability marker

Development API에는 spec과 설명 문서에서 다음을 명시합니다.

- `x-stability: e1-debug`
- debug 또는 development surface라는 설명
- Unity client가 장기 의존해도 되는 계약인지 여부

## Validation

```sh
make docs-build
make ci
```

현재 validation은 raw spec의 필수 version, stability marker, path/schema marker를 확인합니다. Full schema validation tool 도입은 별도 issue에서 결정합니다.
