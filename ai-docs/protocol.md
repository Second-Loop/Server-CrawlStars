# 프로토콜

현재 protocol surface는 E2 client-server integration을 위한 development 계약입니다. Production gameplay protocol은 아직 아닙니다.

## 현재 구현

- `duel_1v1`, `solo`, `team`을 선택하는 same-mode matchmaking join
- room/player WebSocket
- server-authoritative snapshot stream
- client SL-79 `Map_0` 기반 static map movement
- player Wall/Water/boundary collision과 projectile Wall/boundary destroy
- Bush는 둘 다 통과하고 projectile은 Water도 통과
- selected mode rules를 따르는 projectile hit, 결정적 target 선택, HP, death snapshot
- server config v3 기반 Shelly spread, Colt burst, Lily centerline melee 일반 공격
- GameEnd Win/Lose/Draw event와 종료 room 정리
- matchmaking Ready event/ready ACK/countdown/start
- session/credential 없는 server-owned bot participant와 결정적 basic controller
- 첫 human join 기준 room-owned 10초 bot fill과 first-lock-wins late join
- unmatched 연결 종료 시 deadline/credential 유지, matched/loading/starting match cancel
- opaque room/player ID와 player session WebSocket 인증
- 기본 비활성화된 debug REST Bearer guard
- matchmaking join IP별 token-bucket rate limit
- 30초 WebSocket heartbeat와 Ping별 90초 deadline
- client별 snapshot coalescing과 reliable control/terminal delivery
- Store당 30초 cleanup janitor와 cap-pressure 단일 cleanup/retry
- client build용 shared game config artifact

아직 구현하지 않은 것:

- bot replacement와 별도 reconnect grace
- pathfinding, 회피, 시야 판정 같은 advanced bot AI
- respawn, score
- production matchmaking queue

## Simulation 계약

```text
internal/simulation.State.Step(inputs []InputCommand) Snapshot
```

이 계약은 transport와 분리되어 Go unit test에서 직접 검증합니다. REST, WebSocket, room lifecycle, matching queue는 simulation package 안으로 들어오지 않습니다.

`Step` 순서:

1. 모든 player의 transient `PressedAttack`을 `false`로 초기화
2. 최대 charge보다 적은 player의 attack recharge tick 진행
3. 기존 projectile을 남은 range까지 clamp해 이동하고 Wall/boundary 충돌, selected mode별 player hit, range 만료 순서로 처리
4. 현재 snapshot tick에 예정된 Colt burst projectile 수집
5. input을 `PlayerID` 오름차순으로 stable sort하고 live player, 유한한 방향, non-negative `ClientTick`, 마지막 processed ACK보다 큰 양수 tick인지 검증
6. 유효한 양수 input의 `LastProcessedClientTick`을 visible gameplay effect 판정보다 먼저 갱신하고 legacy `ClientTick: 0`은 ACK를 유지
7. movement는 X축, Y축 순서로 player의 Wall/Water/boundary collision 검사
8. 공격 요청, non-zero 방향, 남은 캐릭터별 charge가 유효하면 projectile emission 또는 Lily melee intent 승인
9. Lily melee intent의 피해를 같은 tick batch로 적용한 뒤 projectile emission을 owner ID/ordinal 순서로 생성
10. tick 증가 후 processed input ACK, HP/death, projectile history가 포함된 snapshot 반환

현재 값:

- `TickRate = 30`
- `TileSize = 1.2`
- `DefaultPlayerSpeed = 2`
- `DefaultPlayerRadius = 0.5`
- character catalog/HP = `0=Shelly/4000`, `1=Colt/3100`, `2=Lily/4100`
- normal attack charge/recharge = Shelly `3/30`, Colt `3/30`, Lily `2/30`
- Shelly = `spread_projectile`, damage `280`, range `7.2 tiles`, offsets `-12,-6,0,6,12`
- Colt = `burst_projectile`, damage `340`, range `9 tiles`, 6발/6 tick interval
- Lily = `melee`, damage `1100`, range `2.2 tiles`
- `DefaultProjectileSpeed = 13`
- `DefaultProjectileRadius = 0.3`
- tile 값은 `0=Ground`, `1=Wall`, `2=SpawnPoint`, `3=Bush`, `4=Water`
- Player는 Wall/Water/boundary, projectile은 Wall/boundary에 충돌
- `StaticMapFixture().MaxPlayers = 6`
- Player spawn은 map의 `TileSpawnPoint(2)`를 join 순서대로 먼저 사용합니다. SpawnPoint가 부족하면 map에서 유도한 fallback candidate를 사용하되 player blocking policy와 같은 기준으로 Wall과 Water를 제외합니다. Ground와 Bush는 후보가 될 수 있습니다. Config 검증은 명시적 SpawnPoint와 passable fallback의 고유 좌표가 `map.maxPlayers` 이상인지 확인하므로, 정상 room의 spawn은 서로 겹치지 않습니다.
- server mode catalog는 `duel_1v1`, `solo`, `team`이고 body가 선택을 생략하면 default `duel_1v1`입니다.

Config artifact는 client 공유용과 server runtime용을 분리합니다.

`client-config/game-config.json`은 Unity client가 build 때 sparse checkout해서 runtime asset 경로로 복사하는 공유 config입니다.

- `tileSize`
- `playerRadius`
- `playerTypes`
- `characters` (v2 `0/1/2 = shelly/colt/lily`; legacy `playerTypes: ["default"]` mirror는 compatibility용)
- `projectileRadius`
- `projectileTypes`

`server-config/game-config.json`은 server binary가 embed해서 room store와 simulation 기본값으로 쓰는 server-only config입니다.

Server runtime의 character stats는 speed `2`, radius `0.5`, HP `4000/3100/4100`, attack charge/recharge `3/30`, `3/30`, `2/30`이며 이 mapping이 canonical source입니다. 줄여 쓰면 `3/3/2 charge`, 공통 `30 tick recharge`입니다.

- `tickRate`
- `tile.size`
- player type별 `id/characterType/radius/hp/speed/normalAttack`
- `normalAttack`의 `kind/damagePerHit/rangeTiles/maxCharges/rechargeTicks`와 projectile attack의 `type/count/directionOffsetsDegrees/intervalTicks`
- projectile type별 `id/radius/speed`
- `mode.default`
- `mode.catalog[].id`, `mode.catalog[].playersPerMatch`, `mode.catalog[].teams`, `mode.catalog[].rules`
- `map`: client SL-79에서 merge된 `Map_0`과 값이 같은 20x20 exact grid

Client는 여전히 최종 gameplay state를 서버 snapshot에서 받습니다. `HP`, speed, damage, tick rate, map은 server snapshot이나 Ready event로 받거나 서버만 판단하므로 client 공유 config에 넣지 않습니다.
Mode/team rule도 server-only입니다. REST join response와 Room은 선택된 mode ID만 `gameMode`로 노출하고 `friendlyFire`, `teamBehavior`, 전체 catalog는 노출하지 않습니다. Projectile hit은 room-local selected mode rules를 사용합니다. Solo는 owner가 아닌 live player를 모두 적으로 보고, 현재 `friendlyFire=false`인 Team/Duel은 ally를 통과해 enemy만 hit합니다. 이 동작은 WebSocket message shape를 바꾸지 않습니다.
Attack charge와 recharge 진행도도 server-only state이며 client config나 snapshot에 새 field로 노출하지 않습니다.

### SL-83 캐릭터 일반 공격

server config v3 `normalAttack`이 일반 공격의 source of truth입니다. Client config v2는 캐릭터 identity와 렌더 metadata만 유지하며 raw bytes를 바꾸지 않습니다.

- Shelly는 activation tick에 5발을 동시에 만들고 조준 방향 기준 `-12,-6,0,6,12`도 spread를 적용합니다.
- Colt는 activation tick `A` 기준 `A+[0,6,12,18,24,30]`에 6발을 생성합니다. 마지막 emission tick에는 새 activation을 겹치지 않고 `A+31`부터 다음 공격을 승인합니다. Burst 방향은 activation 때 고정되며 owner가 사망하면 남은 emission을 취소합니다.
- Lily는 2.2 tile centerline에서 첫 eligible target 하나를 찾습니다. 모든 Lily intent는 입력 전 player snapshot을 기준으로 target을 고르고 same-tick batched damage로 일괄 적용하므로 서로를 1100 HP로 맞춘 reciprocal 공격은 둘 다 사망합니다.

Projectile은 남은 configured range까지 이동량을 먼저 clamp한 뒤 map Wall/boundary 충돌, selected mode player hit, 미충돌 range 만료 순서로 처리합니다. 따라서 range endpoint의 tangent hit은 포함됩니다. Lily는 wall/boundary까지의 range를 먼저 잘라 centerline target을 찾고 target과 blocking contact가 같으면 Wall/boundary가 우선합니다. Bush와 Water는 Lily centerline을 막지 않습니다.

`PressedAttack`은 activation 승인 tick만 `true`입니다. Colt의 후속 scheduled emission은 새 activation이 아니므로 `false`이고, projectile `Damage`는 owner의 `normalAttack.damagePerHit`, `Type`은 `normalAttack.projectile.type`에서 결정됩니다. 새 wire field는 없습니다. Client parser 구현과 final balancing은 범위 밖입니다.

Bot도 별도 gameplay state를 만들지 않고 같은 `InputCommand -> State.Step -> Snapshot` 계약을 사용합니다. Room은 직전 authoritative snapshot의 `PlayerData`를 bot controller에 읽기 전용으로 전달합니다. Controller는 가장 가까운 살아 있는 enemy를 고르고, 거리가 같으면 `PlayerID` 오름차순으로 결정하며, 같은 좌표에서는 `+X`를 사용합니다. Human pending input은 map key를 authoritative `PlayerID`로 사용하고 bot key의 외부 input은 버립니다. Human command의 `ClientTick`은 merge 뒤에도 유지하고 bot command는 `ClientTick: 0`을 사용합니다. Bot input과 human input을 합쳐 `PlayerID`로 정렬한 뒤 room tick마다 `State.Step`을 정확히 한 번 호출하므로 movement, projectile, hit, HP, attack charge와 최종 processed input ACK는 계속 `internal/simulation`이 판정합니다.

## WebSocket 계약

```text
WS /rooms/{roomID}/players/{playerID}?token=<player-session-token>
```

연결 조건:

- room이 존재해야 합니다.
- player가 room에 속해야 합니다.
- 정확히 한 개의 non-empty `token` query가 발급된 player session과 일치해야 합니다.
- 정상적인 extra query key는 허용하지만 어느 query pair든 malformed하면 401입니다.
- 같은 room/player의 live connection 또는 in-flight reservation은 409로 거부합니다.
- 검증 우선순위는 room 404, player 404, token 401, connection/reservation 409입니다.
- waiting room도 연결과 input은 허용합니다.
- gameplay snapshot은 room이 started가 된 뒤에만 broadcast합니다.
- payload write는 client별 writer가 수행하며 매번 새 5초 context를 사용합니다.

Token은 일회용 credential이 아니며 room/player session이 존재하는 동안 재사용할 수 있습니다. 다만 matchmaking matched/loading/starting 단계의 실제 disconnect는 pre-start cancel로 room을 삭제하므로 그 뒤에는 reconnect할 수 없습니다. Started room도 all-disconnected 5분 TTL과 hard 1시간 lifetime 안에서만 남습니다. 같은 started match에 reconnect하면 snapshot의 기존 `LastProcessedClientTick` 다음 양수 tick부터 이어 보내고, 새 match의 ACK는 `0`으로 초기화됩니다. Failed HTTP-to-WebSocket upgrade는 reservation만 rollback해 같은 발급 경로로 재시도할 수 있습니다.

발급 JSON의 `sessionToken`, tokenized `webSocketPath`, inbound query는 모두 같은 raw secret을 담습니다. Raw token과 전체 query 문자열을 log나 telemetry에 남기지 않습니다. Ready/Snapshot/GameEnd payload에는 token이나 digest가 없습니다.

Server는 각 connection에 snapshot writer와 독립적인 30초 heartbeat ticker를 둡니다. 각 Ping은 90초 context로 제한하며 error/timeout은 read/write failure와 같은 close-once 경로로 현재 session만 해제합니다. 오래된 heartbeat가 늦게 실패해도 expected-session identity가 다르면 reconnect된 connection을 제거하지 않습니다. Reconnect 전에 current map에서 빠진 이전 connection도 transport `closeDone`까지 room-owned close barrier에 남습니다. Unmatched disconnect는 credential과 deadline을 유지하고 matched/loading/starting disconnect만 match cancel을 적용하며, started room에서 마지막 client가 사라지면 disconnected TTL을 시작합니다. Bot replacement나 reconnect grace는 만들지 않습니다.

일반 gameplay snapshot만 크기 1 latest-only slot에서 coalescing합니다. `Ready`, `starting`, `started`, `error`는 reliable control queue에서 FIFO를 유지합니다. 종료 시 남아 있던 일반 snapshot을 폐기하고, 이미 수락한 control 뒤에 `terminal snapshot -> GameEnd -> close`를 순서대로 실행합니다. Queue overflow, marshal/write failure도 해당 session을 close/release합니다.

Client input:

```json
{
  "ClientTick": 12,
  "MoveDir": { "x": 1, "y": 0 },
  "AttackDir": { "x": 0, "y": 1 },
  "PressedAttack": false
}
```

Server snapshot:

```json
{
  "Type": "snapshot",
  "Snapshot": {
    "status": "started",
    "Tick": 1,
    "Players": [
      {
        "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
        "Team": "red",
        "Slot": 0,
        "IsBot": false,
        "CharacterType": 1,
        "Pos": { "x": -1.2, "y": 1.2 },
        "MoveDir": { "x": 0, "y": 0 },
        "AttackDir": { "x": 0, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 3100,
        "PressedAttack": false,
        "IsDead": false,
        "LastProcessedClientTick": 12
      },
      {
        "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
        "Team": "blue",
        "Slot": 0,
        "IsBot": true,
        "CharacterType": 0,
        "Pos": { "x": 1.2, "y": -1.2 },
        "MoveDir": { "x": -1, "y": 0 },
        "AttackDir": { "x": -1, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 4000,
        "PressedAttack": true,
        "IsDead": false,
        "LastProcessedClientTick": 0
      }
    ],
    "Projectiles": null
  }
}
```

`ClientTick`은 optional `int64`이며 누락/`0`은 legacy input입니다. Room은 `room.mu` 아래 마지막 processed ACK와 positive pending을 비교해 더 큰 양수 command만 저장합니다. Stale/duplicate 양수는 error 없이 무시하고, legacy `0`은 last-write-wins로 positive pending도 덮을 수 있지만 ACK를 변경하지 않습니다. 음수는 `invalid_input`이고 기존 pending을 보존합니다.

`LastProcessedClientTick`은 WebSocket 수신이나 pending 저장이 아니라 `State.Step`이 실제 처리한 마지막 양수 tick입니다. Live player의 유한한 input은 충돌이나 공격 budget 때문에 visible effect가 없어도 ACK합니다. Unknown/dead/non-finite/negative/stale input은 ACK하지 않습니다. ACK는 player별로 단조 증가하며 bot command와 bot ACK는 `0`입니다. Match 시작용 Ready ACK와 processed input ACK는 서로 다른 계약입니다.

Matchmaking room은 선택 mode의 full participant capacity를 채운 뒤 room 내 human participant의 WebSocket session이 모두 연결되면 human session에만 `Ready` event를 보냅니다. Human participant가 0명이면 이 gate는 성립하지 않습니다. 이 event는 client가 map을 렌더하고 bot을 포함한 full participant spawn을 배치하는 기준 데이터입니다.

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
    "map": [
      [1, 1, 1, 1, 1],
      [1, 0, 0, 0, 1],
      [1, 0, 1, 0, 1],
      [1, 0, 0, 0, 1],
      [1, 1, 1, 1, 1]
    ]
  },
  "Players": [
    {
      "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
      "Team": "red",
      "Slot": 0,
      "IsBot": false,
      "CharacterType": 1,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    },
    {
      "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
      "Team": "blue",
      "Slot": 0,
      "IsBot": true,
      "CharacterType": 0,
      "SpawnPosition": { "x": 1.2, "y": -1.2 }
    }
  ]
}
```

예시는 human 한 명과 bot 한 명으로 채운 exact 2-participant duel cardinality와 5x5 fallback map 기준입니다. Ready는 human session에만 전달하지만 payload의 `Players`는 bot을 포함한 full participant list입니다. 실제 기본 runtime map은 `server-config/game-config.json`의 20x20 map입니다. SpawnPoint를 먼저 쓰고 부족하면 Wall/Water를 제외한 Ground/Bush fallback candidate를 사용하므로 실제 위치는 예시와 다를 수 있습니다.

Ready ACK:

```json
{
  "Type": "ready"
}
```

Starting signal:

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

`started` control도 `Tick: 0`, `Players: null`을 유지합니다. 첫 gameplay `Tick: 1`부터 모든 `Players[]`가 required `LastProcessedClientTick`을 가집니다.

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

Invalid input은 connection을 끊지 않습니다.

Malformed JSON과 음수 `ClientTick`은 `invalid_input`을 보내지만 stale/duplicate 양수 tick은 control/error frame 없이 조용히 무시합니다.

GameEnd event:

```json
{
  "Type": "GameEnd",
  "PlayerId": "player_VuTsRqPoNmLkJiHgFeDcBa",
  "Result": "Win"
}
```

`GameEnd`의 `Type`, `PlayerId`, `Result`와 `Win|Lose|Draw` enum은 바뀌지 않았습니다. Mode별 결과는 다음과 같습니다.

| Mode | 진행 중 | Terminal decision |
| --- | --- | --- |
| `duel_1v1` | 두 player가 live이면 계속합니다. | 한 player 사망은 Win/Lose, 같은 tick 동시 사망은 두 player 모두 Draw입니다. |
| `solo` | Solo 중간 탈락 player만 Lose와 close를 받고 survivor gameplay는 계속됩니다. | 마지막 생존자는 Win, 새로 확정되는 dead player는 Lose입니다. 처음 관측한 전원 사망은 모두 Draw입니다. |
| `team` | Team 일부 사망에는 GameEnd가 없고 match를 계속합니다. | 한 team 전멸은 패배 team 3명은 Lose, 상대 team 3명은 Win입니다. 양 team이 같은 tick에 전멸하면 6명 모두 Draw입니다. |

Player별 첫 결과는 immutable ledger에 한 번만 확정합니다. 그래서 Solo에서 이전 Lose는 유지되고, 나중에 전원 사망한 terminal tick은 아직 결과가 없던 player에게만 Draw를 보냅니다. 이미 Lose와 close를 받은 player는 새 terminal event를 받지 않습니다.

Solo 중간 탈락은 해당 session에만 `terminal snapshot -> GameEnd -> close`를 보내며 room ticker는 계속 동작합니다. Room terminal decision에서는 `ending`을 먼저 예약하고 ticker를 terminal decision 즉시 중단합니다. 그 뒤 tick observer, encode, enqueue를 실행해 terminal snapshot과 player별 GameEnd, close 순서를 보장합니다. 새 input, join, reservation, attach, start, 추가 tick은 ending/finalized 경계에서 거부합니다.

## Field 의미

`AttackDir`와 `PressedAttack`은 분리합니다.

- input `AttackDir`: 현재 조준 방향. zero가 아닌 유한한 값은 서버가 unit vector로 정규화합니다.
- input `PressedAttack`: 이번 tick의 캐릭터 일반 공격 activation 요청
- snapshot `PressedAttack`: 방향과 캐릭터별 charge 검증을 통과해 서버가 이번 tick activation을 승인했는지 나타내는 transient 결과

`AttackDir != zero`로 공격을 추론하면 조준 유지 중 매 tick 발사될 수 있습니다. 그래서 input 계약에서는 `PressedAttack`을 유지합니다.

`MoveDir`은 크기가 `1` 이하면 아날로그 입력을 그대로 보존하고, 더 크면 서버가 크기 `1`로 clamp합니다. Shelly/Colt/Lily는 각각 `3/3/2` charge로 시작하고 최대치보다 적을 때 30 tick마다 1 charge를 회복합니다. Zero `AttackDir`, 소진된 charge, 사망한 player input은 공격을 승인하지 않습니다.

`ClientTick`과 `LastProcessedClientTick`은 입력 순서와 처리 완료를 연결합니다.

- input `ClientTick > 0`: room과 simulation의 stale/duplicate guard 대상인 client sequence
- input `ClientTick = 0` 또는 누락: 기존 last-write-wins를 유지하지만 processed input ACK를 바꾸지 않는 legacy command
- snapshot `LastProcessedClientTick`: simulation이 처리한 마지막 양수 command이며 receipt/pending ACK가 아님

같은 gameplay `State.Step`의 input batch는 caller slice를 바꾸지 않고 `PlayerID` 오름차순으로 stable sort한 뒤 적용합니다. 이는 pending input map의 순회 순서와 무관하게 movement와 새 projectile 생성을 결정적으로 만드는 기준입니다.

기존 projectile은 selected mode rules로 hit eligibility를 판단합니다. Owner와 이미 사망한 player는 제외하고, Solo는 나머지 live player를 모두 적으로 보며, 현재 `friendlyFire=false`인 Team/Duel은 ally를 통과해 enemy만 hit합니다. 여러 eligible target과 동시에 겹치면 player의 join/배정 순서에서 첫 target만 피해를 받습니다. 이 target tie-break는 `PlayerID` input 정렬과 서로 다른 순서입니다.

`IsDead`는 `HP <= 0`에서 유도할 수 있지만 snapshot에는 유지합니다. Client가 death rule을 재해석하지 않아도 되고, future state를 추가하기 쉽기 때문입니다.

SL-94는 public WebSocket input에 optional `ClientTick`, gameplay `PlayerData`에 required `LastProcessedClientTick`을 추가합니다. Attack charge 자체는 계속 server-only이고 client에 별도 field로 노출하지 않습니다.

## Matchmaking 계약

현재:

```text
POST /matchmaking/join
```

Request body는 optional입니다.

```json
{"gameMode":"solo"}
```

Canonical mode ID는 `duel_1v1`, `solo`, `team`입니다. Body 없음, `{}`, `{"gameMode":""}`는 default `duel_1v1`로 normalize합니다. optional lower-camel `characterType`은 stable `0=Shelly`, `1=Colt`, `2=Lily`이고 missing field만 Shelly `0` warning compatibility로 처리합니다. explicit invalid type/value는 400 `invalid_character_type`이며 SL-98에서 required로 전환합니다. 지원하지 않는 non-empty mode ID는 400 `invalid_game_mode`, malformed JSON과 trailing JSON value는 400 `invalid_request`입니다.

응답:

```json
{
  "gameMode": "solo",
  "room": {
    "id": "room_AbCdEfGhIjKlMnOpQrStUv",
    "gameMode": "solo",
    "status": "waiting",
    "players": [
      {
        "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
        "team": "solo-1",
        "slot": 0,
        "isBot": false,
        "characterType": 1
      }
    ],
    "maxPlayers": 6,
    "map": {
      "width": 5,
      "height": 5,
      "index": 0,
      "maxPlayers": 6,
      "tileSize": 1.2,
      "map": [
        [1, 1, 1, 1, 1],
        [1, 0, 0, 0, 1],
        [1, 0, 1, 0, 1],
        [1, 0, 0, 0, 1],
        [1, 1, 1, 1, 1]
      ]
    },
    "latestSnapshot": {
      "tick": 0,
      "playerCount": 1,
      "projectileCount": 0
    }
  },
  "player": {
    "id": "player_VuTsRqPoNmLkJiHgFeDcBa",
    "team": "solo-1",
    "slot": 0,
    "isBot": false,
    "characterType": 1
  },
  "sessionToken": "<player-session-token>",
  "webSocketPath": "/rooms/room_AbCdEfGhIjKlMnOpQrStUv/players/player_VuTsRqPoNmLkJiHgFeDcBa?token=<player-session-token>"
}
```

Top-level `gameMode`와 nested `room.gameMode`는 항상 같습니다. Store는 요청 mode를 catalog에서 canonical config로 한 번 선택하고 같은 `gameMode`의 waiting room만 재사용합니다. 새 room은 이 selected config를 immutable하게 소유하며 이후 Store default가 바뀌어도 capacity, team/slot, Ready, simulation, tick interval, GameEnd가 room-local config를 사용합니다.

Room/player ID는 각각 16 random bytes 기반 22자 Raw URL Base64 payload와 `room_`/`player_` prefix를 사용합니다. Session token은 32 random bytes 기반 43자 Raw URL Base64 value입니다. Private room state에는 raw token 대신 SHA-256 digest만 저장하고 public Room/Player payload에는 token/digest field를 넣지 않습니다.

Join handler는 `client IP resolve → token-bucket quota 평가/소비 → body decode와 mode 검증 → store join` 순서로 실행합니다. 기본값은 process-local per-IP 10 requests/minute, burst 4입니다. Quota가 없으면 429 `rate_limited`와 최소 1초 정수 `Retry-After`가 malformed/unknown mode 400, room cap 409, `internal_error` 500보다 먼저 나갑니다. Store에서 409/500으로 끝난 허용 요청도 quota를 소비하며, non-POST 405는 소비하지 않습니다.

Immediate peer가 `TRUSTED_PROXY_CIDRS`에 속하고 `CF-Connecting-IP`가 정확히 하나의 valid IP일 때만 forwarded client IP를 씁니다. Header가 absent/malformed/multiple이면 peer bucket으로 fallback하고 `X-Forwarded-For`는 무시합니다.

Human과 bot을 합친 participant가 selected mode의 capacity 2명 또는 6명을 채워도 `room.status`는 `waiting`입니다. 해당 room은 matchmaking match로 잠겨 late join 대상에서 빠집니다.

Full participant gate를 통과한 뒤 연결된 human participant의 WebSocket session이 모두 attach되면 human session에만 같은 `Type: "Ready"` event를 보냅니다. Ready payload에는 bot을 포함한 full participant list와 JSON number array 형태의 `Map.map`, room-local assignment의 `Players[].Team`, `Slot`, `IsBot`, `SpawnPosition`이 들어갑니다. Bot은 WebSocket sender나 Ready ACK 주체가 아닙니다. 서로 다른 human player가 모두 `{"Type":"ready"}`를 보내야 하며, 같은 player의 중복 ACK는 idempotent하고 quorum을 늘리거나 countdown을 다시 시작하지 않습니다. Human-only quorum 뒤 server는 `Snapshot.status: "starting"`과 `Snapshot.countdown: 5`를 human connection당 1번 보내고, 5초를 내부에서 센 뒤 `Snapshot.status: "started"`를 1번 보낸 다음 room-local simulation ticker 하나를 시작합니다.

첫 human matchmaking join의 `0 -> 1` 전이에서 room-owned one-shot 10초 deadline을 시작합니다. 후속 human join과 partial manual bot 추가는 deadline을 reset하지 않습니다. Timer worker와 human join은 `mutationMu -> matchmakingMu -> Store.mu -> room.mu` 순서로 직렬화하고, `matchmakingMu`를 먼저 얻은 transition이 이깁니다. Timer-first fill 뒤 late join은 다른 waiting room을 찾거나 만들며 active-room cap이면 기존 `room_cap_reached` 409를 반환합니다.

Deadline worker는 selected mode의 남은 participant slot을 bot으로 원자적으로 채웁니다. Bot ID 발급이 하나라도 실패하면 모든 예약 ID를 rollback해 partial participant를 남기지 않고 `bot_fill_failed` structured log event를 한 번 기록하며 retry하지 않습니다. 일반 delete/clear/TTL cleanup/debug start/matched pre-start cancel은 room lock 아래에서 ticker/stop channel을 detach만 하고 모든 core lock 밖에서 ticker `Stop`과 stop channel close를 수행합니다. 일반 cleanup은 worker join을 기다리지 않으며 `workerWG.Wait`는 Shutdown에서만 추가로 수행합니다. Bot은 session token, WebSocket path, Ready ACK를 만들지 않습니다. Unmatched disconnect는 deadline과 credential을 유지하고 matched/loading/starting disconnect는 기존 pre-start cancel로 resource를 회수합니다. Ready timeout, pre-start reconnect grace, reconnect participant replacement도 없습니다.

첫 번째 player만 연결된 상태에서는 room이 `waiting`이라 WebSocket input은 저장되지만 gameplay snapshot은 오지 않습니다. 1명으로 테스트하려면 debug API `POST /rooms/{roomID}/start`를 호출해야 합니다.

Room response와 Ready event의 `map`은 서버 simulation이 collision에 쓰는 tile grid입니다. `map` row는 Base64 문자열이 아니라 JSON number array로 직렬화합니다. 기본 map source는 server binary가 embed한 `server-config/game-config.json`의 `map`입니다. 서버가 이 config 로드나 검증에 실패하면 `StaticGameConfig()`의 5x5 map으로 fallback합니다. `internal/simulation/fixtures/default-map.json`은 runtime source가 아니라 테스트와 legacy 호환 확인용 fixture입니다.

`room.maxPlayers`와 `room.map.maxPlayers`는 map/debug room capacity를 뜻하며 runtime map과 5x5 fallback map 모두 6입니다. Matchmaking required participant 수는 room-local selected mode의 `playersPerMatch`입니다. `duel_1v1`은 2명, `solo`와 `team`은 6명이며 다른 mode끼리는 waiting room을 공유하지 않습니다. Solo team 값은 `solo-1`부터 `solo-6`, team mode assignment는 `red/0, blue/0, red/1, blue/1, red/2, blue/2`입니다.

`SL-58`에서는 당시 `POST /matchmaking/join` response shape를 유지한 채 WebSocket state message를 추가했습니다. `SL-81` Stack 3은 transport credential을 위해 `sessionToken`과 tokenized `webSocketPath`를 발급합니다. REST polling이나 SSE는 늘리지 않습니다.

## Room cleanup

- waiting room idle TTL: 10분
- started room all-disconnected TTL: 5분
- hard room lifetime: 1시간
- connected client가 있으면 idle/all-disconnected TTL cleanup을 막습니다.

Store는 30초 janitor ticker 하나로 registry snapshot을 짧게 얻은 뒤 room별 expiry를 검사합니다. Gameplay tick, 일반 GET, input 경로는 전체 registry cleanup을 수행하지 않습니다. Debug create와 matchmaking create가 active room cap에 닿았을 때만 즉시 cleanup을 정확히 한 번 수행하고 생성도 한 번만 재시도합니다. 아직 만료되지 않은 room만 남으면 `409 room_cap_reached`를 유지합니다.

현재 WebSocket close는 connection과 pending input을 제거합니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지하고, matched/loading/starting disconnect는 match cancel로 room과 남은 connection을 정리합니다.

각 terminal session의 connected-client observer는 session close callback에서 반영되어 transport `closeDone`보다 먼저일 수 있습니다. Normal GameEnd cleanup은 current terminal session, 앞서 결과가 확정되어 기억한 session, reconnect 전에 current map에서 빠졌지만 close가 끝나지 않은 historical session generation의 `closeDone`을 모두 기다립니다. 따라서 Solo prior loser와 ordinary reconnect predecessor가 모두 barrier에 남고, lifecycle monitor가 각 `closeDone` 뒤 이를 제거합니다. 그 뒤 registry를 분리하고 active-room observer를 반영한 다음 player ID를 release하고 `room_ended` log와 남은 room resource close를 수행합니다. `cleanup success signal`은 이 정상 작업이 모두 성공한 마지막에만 닫습니다. Callback panic, stale ownership, 이미 제거된 room은 성공으로 표시하지 않습니다. Closing 중인 ending room은 hard TTL과 debug `DELETE /rooms`/`DELETE /rooms/{roomID}`가 제거하지 않습니다.

`Shutdown`은 forced-teardown 예외입니다. Store quiescing을 소유하므로 terminal `closeDone` 전에 room registry와 player ID를 detach할 수 있습니다. Deadline에는 WebSocket accept 때 캡처한 underlying `net.Conn`을 직접 닫아 이미 진행 중인 graceful close를 중단합니다. 그래도 GameEnd cleanup worker와 session의 close/writer/heartbeat/lifecycle을 모두 join한 뒤 반환합니다. Forced takeover는 normal GameEnd cleanup signal을 닫지 않고 `room_ended`를 기록하지 않습니다.

디버그 테스트 중 즉시 비워야 하면 REST debug API를 명시적으로 활성화하고 Bearer credential로 호출합니다. 기본 상태에서는 debug route와 method fallback이 `404 not_found`이며, 활성화 후 missing/wrong/multiple credential은 route result보다 먼저 `401 unauthorized`입니다. WebSocket GET은 debug Bearer 예외입니다.

```text
DELETE /rooms
DELETE /rooms/{roomID}
```

일반 room 삭제는 in-memory room, room-local ticker, WebSocket connection을 정리합니다. Ending room은 normal GameEnd cleanup 또는 forced Shutdown이 소유합니다. Persistence는 아직 없으므로 DB 삭제는 없습니다.

## Duel 2인 검증 시나리오

1. `POST /matchmaking/join`을 두 번 호출합니다.
2. 두 secret-bearing `webSocketPath`로 연결하되 raw path/query를 log에 남기지 않습니다.
3. 두 connection이 같은 `Type: "Ready"` event를 받고, 이 event의 `Map.map` row가 숫자 배열이어야 합니다.
4. 두 client가 `{"Type":"ready"}`를 보내면 `starting` 신호를 1번 받고, 중간 countdown broadcast 없이 5초 뒤 `started`를 받아야 합니다.
5. 한 player가 양수 `ClientTick` movement input을 보내면 수신 직후가 아니라 다음 gameplay `State.Step` snapshot에서 `LastProcessedClientTick`이 올라가고 두 connection이 같은 `Snapshot.Tick`, ACK, player `Pos`를 받아야 합니다.
6. 같은 player의 stale/duplicate 양수 tick은 error 없이 무시되고, 다른 player ACK는 독립적으로 유지되어야 합니다.
7. Red와 blue spawn은 Ready event의 `Players[].SpawnPosition`으로 확인합니다.
8. Hit tick에서 projectile은 `IsDestroyed: true`, selected mode에서 eligible한 첫 target은 HP 감소로 보여야 합니다.
9. HP가 0이 되면 `HP: 0`, `IsDead: true`가 보여야 합니다.
10. HP가 0이 된 tick의 snapshot 이후 player별 `GameEnd`를 받아야 합니다.
11. 한 player 사망은 Win/Lose, 같은 tick 동시 사망은 두 player 모두 Draw이며 기존 `duel_1v1` 결과를 유지해야 합니다.
12. Terminal ticker가 먼저 멈추고 모든 close가 끝난 뒤 room registry와 player ID가 정리되어야 합니다.
13. invalid JSON과 음수 `ClientTick` 이후에도 다음 snapshot stream은 유지되어야 합니다.

## Solo/Team human-only 6인 검증 시나리오

1. 같은 `gameMode`로 `POST /matchmaking/join`을 6번 호출하고 여섯 응답의 `room.id`와 `gameMode`가 같은지 확인합니다.
2. 여섯 secret-bearing `webSocketPath`로 서로 다른 WebSocket connection을 열고 raw token/query를 log에 남기지 않습니다.
3. 다섯 connection만 attach된 동안 internal `matchStatus`는 `matched`, public `room.status`는 `waiting`이며 Ready event와 countdown은 시작하지 않습니다.
4. 여섯 번째 connection이 attach되면 여섯 connection 모두 같은 `Ready` event를 받습니다.
5. `solo`는 `solo-1/0`부터 `solo-6/0`, `team`은 `red/0`, `blue/0`, `red/1`, `blue/1`, `red/2`, `blue/2`를 확인합니다.
6. Ready의 six spawn이 room-local `PlayerAssignments`와 같고 fallback 사용 시 Wall/Water가 아니며 서로 다른지 확인합니다. Ground와 Bush는 passable candidate입니다.
7. 서로 다른 다섯 player만 ready ACK를 보내면 `loading`을 유지하고 countdown ticker가 없어야 합니다.
8. 이미 ready인 player가 ACK를 반복해도 quorum은 5로 유지됩니다.
9. 여섯 번째 서로 다른 player가 ACK하면 `starting/countdown: 5`를 connection당 한 번 받고 countdown ticker는 하나여야 합니다.
10. 5초 뒤 `started`를 connection당 한 번 받고 gameplay ticker는 하나여야 합니다.
11. 첫 gameplay tick은 `Tick: 1`, `Players` 길이 6이고 여섯 connection에서 같은 payload여야 합니다. 모든 player에 `LastProcessedClientTick` key가 있으며 bot ACK는 `0`이어야 합니다.
12. Solo projectile은 owner를 제외한 live player를 hit하고, Team projectile은 ally를 통과해 enemy를 hit해야 합니다.
13. 여러 eligible target이 겹치면 join/배정 순서의 첫 target만 피해를 받고, input 전달 순서를 섞어도 `PlayerID` 오름차순 적용 결과가 같아야 합니다.
14. Solo 중간 탈락은 해당 player만 Lose와 close를 받고 survivor tick은 계속되어야 합니다. 마지막 생존자는 Win이며, 이전 Lose 뒤 전원 사망이면 이전 Lose는 유지되고 나머지만 Draw여야 합니다.
15. Team 일부 사망은 계속 진행하고, 한 team 전멸은 3 Lose/3 Win, 같은 tick 양 team 전멸은 6 Draw여야 합니다.
16. Ending room은 hard TTL/debug delete에서 보호되고, 정상 cleanup은 모든 `closeDone` 뒤에 registry/player ID를 정리해야 합니다.
17. Forced `Shutdown`은 registry/player ID를 먼저 detach할 수 있지만 cleanup worker와 session lifecycle을 join하고 normal cleanup signal/`room_ended`를 만들지 않아야 합니다.

이 시나리오는 bot을 넣지 않은 human-only 회귀 경로입니다. SL-91 timer 경로에서는 첫 human join 뒤 10초에 participant 6명을 채우고 human session만 attach/Ready ACK하며, human에게 전달된 Ready와 gameplay snapshot에는 bot을 포함한 6명 전체가 나타나야 합니다. Bot의 `LastProcessedClientTick`은 `0`입니다. Start 전 실제 human disconnect는 unmatched이면 deadline과 credential을 유지하고, matched/loading/starting이면 기존 pre-start cancel로 room과 남은 connection을 정리합니다.

자동 검증은 `go test ./internal/rooms`와 `go test ./internal/simulation`이 담당합니다.

## 문서 위치

- REST: `api/openapi.yaml`
- WebSocket: `api/asyncapi.yaml`
- 사람이 읽는 API: `ai-docs/api-reference.md`
- 문서화 기준: `ai-docs/api-docs.md`

후속 protocol message는 Linear issue에서 scope와 acceptance criteria를 먼저 정한 뒤 구현합니다.

## SL-82 CharacterType 전파

Join request의 optional lower-camel `characterType`은 `0=Shelly`, `1=Colt`, `2=Lily` stable numeric ID입니다. room admission은 이를 canonical participant에 저장하고, legacy missing만 Shelly `0`과 structured warning으로 처리합니다. explicit null, 잘못된 JSON type, 지원하지 않는 integer는 `invalid_character_type` 400이며 SL-98이 required 전환 경계입니다.

전파는 `join -> canonical room participant -> Ready -> PlayerData` 순서입니다. REST participant는 `characterType`, Ready와 Snapshot `PlayerData`는 PascalCase `CharacterType`을 required로 사용합니다. Bot/debug participant는 Shelly `0`입니다. `starting`과 `started` control은 기존처럼 `Players: null`이며 gameplay snapshot부터 participant identity와 stats가 나타납니다.
