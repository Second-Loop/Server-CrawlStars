# SL-82 CharacterType 선택 계약과 상태 전파 설계

## 1. 목적

`POST /matchmaking/join`에서 선택한 정수 `CharacterType`을 room의 canonical participant에 한 번 저장하고, join 응답부터 Ready와 gameplay snapshot까지 같은 값으로 보존해요. Simulation은 이 값을 server runtime config의 명시적 key로 조회해 캐릭터별 HP와 v1 공통 speed/radius를 적용해요.

이 설계는 다음 경계를 지켜요.

- SL-82는 캐릭터 선택, 기본 능력치, wire 전파만 구현해요.
- 일반 공격 패턴은 SL-83, 스킬 입력·쿨타임은 SL-84, 스킬 효과는 SL-85에서 다뤄요.
- `characterType` 필수값 전환은 SL-98에서 다뤄요.
- 역할별 speed/radius 조정은 실제 연동과 플레이테스트 계측 뒤 별도 후속 범위에서 다뤄요.

## 2. 확정한 제품 결정

### 2.1 고정 CharacterType ID

Wire identity는 config 배열 index나 문자열 이름이 아니라 명시적 정수 `characterType`이에요.

| CharacterType | 문자열 ID | 이름 | 역할 | HP | Speed | Radius |
| ---: | --- | --- | --- | ---: | ---: | ---: |
| `0` | `shelly` | Shelly | `damage_dealer` | `4000` | `2` | `0.5` |
| `1` | `colt` | Colt | `damage_dealer` | `3100` | `2` | `0.5` |
| `2` | `lily` | Lily | `assassin` | `4100` | `2` | `0.5` |

- `speed` 단위는 world unit/second예요.
- `radius` 단위는 world unit이에요.
- ID `0 | 1 | 2`는 중복 없이 유지하고 이후 재번호화하지 않아요.
- Config 배열 순서는 의미가 없으며 재정렬해도 mapping이 바뀌지 않아야 해요.
- v1에서는 세 캐릭터의 speed/radius를 현재값과 같게 유지해 근거 없는 밸런싱을 피합니다.

### 2.2 단계적 필수화

SL-82에서는 `characterType`을 optional migration field로 추가해요.

- 필드가 누락되면 Shelly `0`을 사용해 기존 client와 body 없는 join을 호환해요.
- 명시적 `null`, 비정수, 문자열, 지원하지 않는 정수는 `400 invalid_character_type`으로 거부해요.
- 누락값 보정은 deprecated compatibility path라고 OpenAPI와 사람용 문서에 명시해요.
- 누락값을 보정한 요청에는 비밀값 없는 `character_type_defaulted` 구조화 경고를 남겨 client 전환 여부를 확인해요.
- 현재 client가 모든 join에 `0 | 1 | 2`를 보낸다고 통합 검증한 뒤 SL-98에서 필수값으로 전환해요.

### 2.3 Bot과 debug participant

- Sessionless bot은 Shelly `0`을 사용해요.
- Character 선택 입력이 없는 debug player도 Shelly `0`을 사용해요.
- Bot/debug 기본값 변경과 bot의 캐릭터 선택 정책은 SL-82 범위 밖이에요.

## 3. Config 소유권과 schema

### 3.1 Client-shared config

`client-config/game-config.json`은 client가 알아야 하는 stable identity mapping을 소유해요.

```json
{
  "version": 2,
  "tileSize": 1.2,
  "playerRadius": 0.5,
  "playerTypes": ["default"],
  "characters": [
    {
      "characterType": 0,
      "id": "shelly",
      "name": "Shelly",
      "role": "damage_dealer"
    },
    {
      "characterType": 1,
      "id": "colt",
      "name": "Colt",
      "role": "damage_dealer"
    },
    {
      "characterType": 2,
      "id": "lily",
      "name": "Lily",
      "role": "assassin"
    }
  ],
  "projectileRadius": 0.3,
  "projectileTypes": ["default"]
}
```

기존 client parser가 읽는 `playerTypes: string[]`와 `playerRadius`는 SL-82에서 shape과 값을 바꾸지 않아요. 새 `characters` catalog를 additive field로 제공해 current client migration과 구버전 parser의 unknown-field 호환을 분리해요. SL-82 client integration은 config version `2`와 `characters`를 명시적으로 지원하는지 확인해야 하지만, 기존 `playerTypes` consumer를 같은 변경에서 강제로 깨지 않아요.

`playerRadius`는 v1 세 캐릭터가 모두 `0.5`를 사용하는 동안 client compatibility mirror로 유지해요. Server snapshot의 `Radius`가 authoritative gameplay 결과라는 기존 경계는 바꾸지 않아요. 캐릭터별 radius가 달라지는 후속 작업에서는 shared schema도 함께 재검토해요.

### 3.2 Server-authoritative config

`server-config/game-config.json`은 각 `characterType`의 authoritative HP/speed/radius를 소유해요.

```json
{
  "version": 2,
  "player": {
    "types": [
      {
        "characterType": 0,
        "id": "shelly",
        "radius": 0.5,
        "hp": 4000,
        "speed": 2,
        "maxAttackCharges": 4,
        "attackRechargeTicks": 30
      }
    ]
  }
}
```

실제 config에는 Colt와 Lily entry도 같은 구조로 들어가요. SL-82는 일반 공격 범위가 아니므로 `maxAttackCharges`와 `attackRechargeTicks`는 기존 동작인 `4`와 `30`을 세 entry에 유지해요. SL-83에서 합의된 탄창 `3/3/2`와 캐릭터별 일반 공격 규칙을 적용해요.

### 3.3 Config validation

`simulation.CharacterType`은 정수 기반 named type으로 정의하고 `Shelly=0`, `Colt=1`, `Lily=2` 상수를 제공해요. `GameConfig`는 다음을 검증해요.

- server config version이 정확히 `2`인지
- numeric `characterType`이 정확히 `0 | 1 | 2`인지
- numeric `characterType`이 중복되지 않는지
- 문자열 `id`가 비어 있거나 중복되지 않는지
- stable mapping `0=shelly`, `1=colt`, `2=lily`가 유지되는지
- 세 ID가 모두 존재하는지
- HP/speed/radius와 기존 attack state 값이 양수인지
- client-shared `characters[].characterType <-> id`와 server `player.types[].characterType <-> id` mapping이 drift하지 않는지

Name과 role은 client-shared metadata이며 server drift 비교 대상이 아니에요. Client config loader도 version `2`와 additive `characters` catalog를 검증해요. Version `1`은 새 schema loader에서 명시적으로 거부해 오래된 shape가 조용히 새 계약으로 해석되지 않게 해요.

Lookup은 배열 index가 아니라 `PlayerType(characterType)` 같은 명시적 method를 사용해요. `DefaultPlayerType()`도 첫 배열 entry가 아니라 `CharacterTypeShelly` lookup으로 Shelly를 반환해요. Production config, embedded static fallback, tests가 같은 3종 catalog를 사용해요.

현재 application/store의 config 오류 정책은 유지해요. Embedded config load가 실패하면 loader는 오류를 반환하고 application은 오류를 기록한 뒤 valid version `2`의 static 3종 catalog로 fallback해 계속 시작해요. CI와 unit test는 embedded source config 오류를 실패로 잡고, runtime fallback 자체도 별도 회귀 테스트로 보호해요.

## 4. Wire 계약

### 4.1 REST request

```json
{
  "gameMode": "duel_1v1",
  "characterType": 1
}
```

- REST request field는 기존 lower camel convention에 따라 `characterType`을 사용해요.
- Type은 integer enum `0 | 1 | 2`예요.
- SL-82에서는 required 목록에 넣지 않고 missing fallback을 문서화해요.
- JSON decoder는 `0`, missing, explicit `null`을 모두 구분해야 하므로 `json.RawMessage` 기반 presence 경계를 사용해요. 단순 pointer는 missing과 `null`을 구분하지 못하므로 사용하지 않아요. Decoder는 character raw value를 보존하고 semantic validation은 gameMode 선택 뒤에 수행해 기존 오류 우선순위를 유지해요.

### 4.2 REST response

공통 REST `Player` schema가 required `characterType`을 포함해요. 따라서 join 응답의 top-level `player`, nested `room.players[]`, room list/detail player, debug player session response에 모두 같은 필드가 노출돼요. Missing request가 Shelly로 보정된 경우에도 response에는 명시적 `0`을 반환해요.

```json
{
  "player": {
    "id": "player-id",
    "team": "red",
    "slot": 0,
    "isBot": false,
    "characterType": 0
  }
}
```

### 4.3 Ready와 Snapshot

기존 Unity-facing PascalCase convention을 따라 Ready player와 gameplay `PlayerData`에는 required `CharacterType`을 사용해요.

```json
{
  "Id": "player-id",
  "CharacterType": 0
}
```

- Starting/started lifecycle control snapshot의 `Players: null` 의미는 바꾸지 않아요.
- 첫 gameplay snapshot부터 모든 human/bot player가 `CharacterType`을 포함해요.
- Snapshot의 HP/Speed/Radius는 해당 player의 CharacterType config에서 초기화된 authoritative 값이에요.

## 5. Runtime 데이터 흐름

```text
POST /matchmaking/join
  -> rate limit과 body size guard
  -> JSON framing과 gameMode shape decode
  -> gameMode semantic validation
  -> raw CharacterType shape/value validation 또는 missing fallback
  -> room의 canonical playerResponse에 immutable 저장
  -> join top-level player / nested room player
  -> Ready player projection
  -> simulation.PlayerData 생성
  -> CharacterType별 HP/speed/radius normalize
  -> gameplay snapshot projection
```

### 5.1 Join과 room 소유권

- `matchmakingJoinRequest`가 optional CharacterType presence를 표현해요.
- 기존 rate limit은 request decode보다 먼저 평가되며 invalid request도 quota를 소비하는 현재 동작을 유지해요.
- Decoder는 JSON framing과 `gameMode` shape를 검증하되 CharacterType은 raw value로 보존해요.
- Store는 mutation 전에 selected mode와 CharacterType을 순서대로 resolve해요. Existing `gameMode` semantic validation을 먼저 유지하므로 mode와 character가 모두 invalid이면 `invalid_game_mode`가 우선해요.
- CharacterType lookup은 room/player mutation 전에 끝내요.
- 유효하거나 보정된 CharacterType은 `playerResponse`에 저장해요.
- 별도 `room.characterTypes[playerID]` side map은 만들지 않아요.
- `tryJoinMatchmakingRoom`과 `createMatchmakingRoom`은 이미 검증된 CharacterType을 participant append까지 전달해요.

### 5.2 Ready와 simulation

- `readyEventPlayers`는 canonical participant의 CharacterType을 그대로 복사해요.
- `simulationPlayers`는 CharacterType을 `simulation.PlayerData`에 넣어요.
- 정상 room start의 `simulationPlayers`는 각 player의 CharacterType으로 player type config를 조회해 HP/speed/radius를 채워요. `NewStateWithConfig`는 zero stats에 같은 lookup을 적용하되 internal simulation tests가 명시한 positive HP/speed/radius override는 기존처럼 보존해요.
- `DefaultPlayerType()`은 Shelly config를 반환하는 legacy/debug compatibility helper로 유지해요. 정상 room start에서는 사용하지 않고 모든 participant를 명시적 CharacterType lookup으로 초기화해요.
- Attack charge/recharge는 SL-82에서 CharacterType별로 분기하지 않아요. 세 config의 값과 runtime 동작을 기존 `4/30`으로 유지하고, character별 `3/3/2` 전환은 SL-83에서 처리해요.
- Movement, collision, projectile hit, death, GameEnd는 이미 `PlayerData`의 Speed/Radius/HP를 사용하므로 알고리즘을 바꾸지 않아요.

## 6. 오류 처리와 관측성

| 입력/상태 | 결과 |
| --- | --- |
| `characterType` missing | Shelly `0`, `201`, compatibility warning |
| `characterType: 0 | 1 | 2` | 선택한 type으로 `201` |
| `characterType: null` | `400 invalid_character_type` |
| 문자열, bool, object, array | `400 invalid_character_type` |
| 소수 또는 정수 범위 밖 숫자 | `400 invalid_character_type` |
| 지원하지 않는 정수 | `400 invalid_character_type` |
| config mapping 중복·누락·drift | source load 오류 기록 후 valid static v2 catalog fallback |

Invalid CharacterType은 room, player ID, credential, bot-fill timer를 만들지 않아요. 기존 rate-limit quota 소비 순서는 유지해요.

오류 우선순위는 다음과 같이 고정해요.

1. Rate limit 초과는 body와 무관하게 `429 rate_limited`예요.
2. Body size 초과, malformed/trailing JSON, invalid top-level body, invalid `gameMode` JSON type은 `400 invalid_request`예요.
3. Unknown `gameMode`는 CharacterType semantic 오류보다 먼저 `400 invalid_game_mode`예요.
4. 유효한 mode 뒤 explicit CharacterType shape/value 오류는 `400 invalid_character_type`이에요.

`character_type_defaulted` 경고는 CharacterType이 missing이고 human participant가 성공적으로 생성되어 `201`을 반환하는 요청마다 정확히 한 번 기록해요. Invalid mode, room cap, credential 발급 실패, bot/debug 생성에는 기록하지 않아요. 경고에는 `game_mode`와 event name만 넣고 client IP, session token, tokenized WebSocket path, raw request body는 남기지 않아요. SL-98 전환 판단은 이 compatibility path가 더 이상 사용되지 않는다는 client/server 통합 검증을 기준으로 해요.

## 7. 테스트 설계

### 7.1 Config와 simulation

- `0/1/2` catalog load와 exact mapping을 확인해요.
- Config 배열을 재정렬해도 lookup 결과가 같은지 확인해요.
- Duplicate numeric ID, duplicate string ID, missing ID, unknown ID를 각각 거부하는지 확인해요.
- Version `1`을 거부하고 version `2`의 additive client catalog와 server catalog를 load하는지 확인해요.
- Client/server `characterType <-> id` mapping drift를 repo-local test로 검출해요.
- Embedded config 오류 시 오류를 기록하고 valid static v2 3종 catalog로 fallback하는 기존 application/store 동작을 확인해요.
- Config 배열을 재정렬해도 `DefaultPlayerType()`이 첫 entry가 아닌 Shelly `0`을 반환하는지 확인해요.
- Shelly/Colt/Lily가 HP `4000/3100/4100`과 공통 speed `2`, radius `0.5`로 초기화되는지 table test로 확인해요.
- 같은 room의 서로 다른 CharacterType player가 서로의 stats를 공유하지 않는지 확인해요.
- Attack charge/recharge가 세 캐릭터 모두 기존 `4/30`을 유지하는지 확인해요.
- 기존 movement, collision, attack charge, projectile hit/death 테스트가 회귀하지 않는지 확인해요. Death regression은 production HP나 projectile damage를 바꾸지 않고 internal test의 작은 explicit HP fixture를 유지해요.

### 7.2 REST와 room

- Body 없음, 빈 object, missing CharacterType이 Shelly `0`으로 join되는지 확인해요.
- Explicit `0`, `1`, `2`가 top-level player와 nested room player에 동일하게 보존되는지 확인해요.
- `null`, 문자열, 소수, 음수, `3`, 큰 정수가 exact `400 invalid_character_type`을 반환하는지 table test로 확인해요.
- Unknown gameMode와 invalid CharacterType을 함께 보냈을 때 `invalid_game_mode`가 우선하는지 확인해요.
- Rate-limited 또는 oversized request와 invalid CharacterType을 조합해 각각 `rate_limited`, `invalid_request` 우선순위를 확인해요.
- Invalid CharacterType이 room/player/credential/timer mutation을 만들지 않는지 확인해요.
- 성공한 missing human join만 비밀값 없는 `character_type_defaulted` warning을 정확히 한 번 남기는지 확인해요. Invalid mode, room cap, credential failure, bot/debug에는 warning이 없어야 해요.
- Matchmaking top-level/nested player, room list/detail, debug player session response가 required `characterType`을 포함하는지 확인해요.
- Duel/Solo/Team과 debug player, manual/internal bot 추가, automatic bot fill이 기존 lifecycle과 Shelly `0` 기본값을 유지하는지 확인해요.

### 7.3 WebSocket과 문서

- Ready의 모든 human/bot player가 required `CharacterType`을 갖는지 확인해요.
- Starting/started control의 `Players: null` 회귀와 첫 gameplay snapshot의 CharacterType을 함께 확인해요.
- Reconnect 뒤에도 같은 room participant CharacterType이 유지되는지 확인해요.
- OpenAPI request/response, AsyncAPI Ready/Snapshot schema와 예제를 갱신해요.
- `ai-docs/api-reference.md`, `api-docs.md`, `protocol.md`, `architecture.md`, `decisions.md`, `project-map.md`를 구현과 맞춰요.
- `docs-ui/scripts/validate.mjs`와 generated docs embed를 갱신하고 공식 docs validation을 실행해요.
- Client-shared config의 legacy `playerTypes: string[]` shape가 유지되고 additive `characters` catalog가 exact `0/1/2` mapping을 제공하는지 확인해요.

최종 검증은 focused simulation/rooms/docs tests, 반복·race 검증이 필요한 affected room tests, 전체 `make ci` 순서로 실행해요.

## 8. 범위 밖

- 일반 공격의 projectile 수, 퍼짐, 연사, 사거리, damage 변경
- SL-80의 탄창 `3/3/2` 적용
- `PressedSkill`, skill cooldown, 캐릭터별 skill effect
- Player-player collision과 map collision 성능 최적화
- Strict required CharacterType 전환
- 캐릭터별 speed/radius 밸런싱과 계측 도구
- Client 선택 UI, 아트, 애니메이션, 이펙트
- Bot의 캐릭터 선택 또는 skill 사용

## 9. 완료 조건

- SL-82의 acceptance criteria와 이 문서의 오류 matrix에 대응하는 자동화 테스트가 있어요.
- CharacterType이 join부터 Ready와 gameplay snapshot까지 한 값으로 보존돼요.
- Mixed-character room의 stats가 server config에 따라 독립적으로 초기화돼요.
- 기존 duel/solo/team, bot, reconnect, GameEnd 흐름이 회귀하지 않아요.
- OpenAPI, AsyncAPI, generated docs와 `ai-docs/`가 runtime과 일치해요.
- `make ci`가 통과해요.
- PR과 Linear comment에 validation 결과와 SL-98 후속 경계를 짧게 남겨요.
