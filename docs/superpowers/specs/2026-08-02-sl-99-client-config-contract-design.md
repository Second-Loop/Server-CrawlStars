# SL-99 Client Config 계약 정합성 설계

## 1. 목표

Server의 `client-config/game-config.json`을 명시적인 v3 계약으로 제공하고, Client 소비자가 따라야 할 검증 경계를 정의해요.

- 클라이언트 개발자가 제시한 캐릭터별 설정 구조와 값을 canonical client artifact로 채택해요.
- 필수 field 누락과 지원하지 않는 version을 기본값 `0`으로 숨기지 않고 build 또는 startup에서 실패시켜요.
- client-shared 값과 server-authoritative gameplay 값을 분리해 문서화해요.
- Server artifact의 schema·값·소유권을 Go test와 문서에서 반복 검증해요.

캐릭터 밸런스, server simulation의 일반 공격 판정, 신규 스킬 효과는 바꾸지 않아요.

## 2. Canonical artifact

`client-config/game-config.json`은 breaking schema 변경을 나타내기 위해 기존 v2에서 v3으로 올려요. v1으로 되돌리지 않고 소비자가 정확히 v3만 지원하게 해, 구버전 artifact가 조용히 통과하지 않게 해요.

```json
{
  "version": 3,
  "tileSize": 1.2,
  "playerRadius": 0.5,
  "characters": [
    {
      "type": 0,
      "normalAttackDistance": 5.0,
      "skillAttackDistance": 1.0,
      "skillAttackCoolDown": 10,
      "maxBullets": 3
    },
    {
      "type": 1,
      "normalAttackDistance": 1.5,
      "skillAttackDistance": 3.0,
      "skillAttackCoolDown": 10,
      "maxBullets": 3
    },
    {
      "type": 2,
      "normalAttackDistance": 6.0,
      "skillAttackDistance": 7.0,
      "skillAttackCoolDown": 10,
      "maxBullets": 4
    }
  ],
  "normalAttackCoolDown": 1,
  "projectileRadius": 0.3
}
```

v2의 `playerTypes`, `projectileTypes`, `characters[].characterType/id/name/role`은 Unity가 소비하지 않고 새 canonical schema와 중복되므로 v3에서 제거해요. Stable 캐릭터 ID는 `characters[].type`의 `0=Shelly`, `1=Colt`, `2=Lily`로 유지해요.

## 3. 값과 단위의 소유권

| Field | 단위와 소비자 | 소유권 |
| --- | --- | --- |
| `tileSize` | Unity world unit/tile | Client와 Server가 공유하는 좌표 변환 상수예요. |
| `playerRadius`, `projectileRadius` | Unity world unit | Client 표현·회피 계산과 Server collision이 같은 값을 사용해요. |
| `characters[].type` | stable numeric ID | Client/Server shared identity이며 `0/1/2`를 재번호화하지 않아요. |
| `normalAttackDistance`, `skillAttackDistance` | Unity world unit | Client 조준 UI와 로컬 bot 판단용 값이에요. Server hit/range 판정은 `server-config`를 계속 사용해요. |
| `normalAttackCoolDown`, `skillAttackCoolDown` | 초 | Client cooldown UI와 로컬 bot 입력 시도용 값이에요. Server의 실제 공격 승인은 tick 기반 상태가 계속 소유해요. |
| `maxBullets` | client charge 개수 | Client cooldown UI와 로컬 bot용 값이에요. Server의 authoritative charge는 `server-config`가 소유하므로 Lily의 client 값 `4`와 server 값 `2`를 억지로 동기화하지 않아요. |

따라서 이번 변경은 클라이언트 설정 계약을 맞추는 작업이지, Server의 Shelly/Colt/Lily 일반 공격 거리 `7.2/9/2.2 tile`이나 charge `3/3/2`를 변경하는 밸런스 작업이 아니에요. Server snapshot과 attack approval이 최종 gameplay truth예요.

## 4. Server 변경

Server-CrawlStars에서는 다음을 수행해요.

1. `client-config/game-config.json`을 위 v3 artifact로 교체해요.
2. Go 회귀 테스트가 exact top-level key와 캐릭터별 필수 key를 확인하게 해요.
3. 캐릭터가 정확히 세 개이고 `type`이 `0/1/2`로 중복 없이 존재하는지 검증해요.
4. 양수여야 하는 거리·cooldown·charge와 공용 radius/tile 값을 검증해요.
5. client artifact 값이 server-authoritative normal attack config와 같다고 가정하는 기존 테스트와 문서를 소유권 기준으로 바꿔요.

Server runtime은 `server-config/game-config.json` v3를 그대로 embed하고, room/simulation 동작에는 변경을 주지 않아요.

## 5. Client 소비 계약

이 Server PR은 Client-CrawlStars 코드를 수정하지 않아요. Client 저장소 소유자는 artifact를 가져갈 때 parser와 build preprocessor가 같은 검증 경계를 따르게 해요.

1. JSON property를 v3 field에 맞추고 모든 필수 field를 `Required.Always`로 선언해요.
2. parse 뒤 `version == 3`, exact character type set, 중복 type, 양수 field를 검증해요.
3. JSON parse 또는 contract 검증 실패를 잡아 명시적인 error를 남기고 `LoadAsync()`가 `false`를 반환하게 해요.
4. `FileSynchronizer`는 다운로드한 JSON을 `StreamingAssets`에 쓰기 전에 같은 contract validator를 실행해요. 실패하면 `BuildFailedException`으로 build를 중단하고 기존 local artifact를 덮어쓰지 않아요.
5. `CharacterInfo`와 `AttackManager`는 검증을 통과한 typed config만 받으며 누락값 `0` fallback에 기대지 않아요.

Unknown extra field는 앞으로의 additive 호환성을 막지 않도록 허용하지만, 필수 field와 exact version은 엄격하게 검증해요.

## 6. 실패 처리

- 다운로드 실패, 빈 파일, 잘못된 JSON: build preprocessor가 실패하고 기존 파일을 보존해요.
- `version != 3`: build와 runtime parse가 모두 지원하지 않는 version으로 실패해요.
- 필수 field 누락 또는 `null`: Newtonsoft JSON required-field 오류로 실패해요.
- 음수, `0`, NaN/Infinity, 중복·미지원 character type: post-parse validation으로 실패해요.
- runtime load 실패: 부분적으로 static property를 갱신하지 않고 `false`를 반환해 초기화를 중단할 수 있게 해요.

오류 메시지는 field path와 실패 이유를 포함하되 artifact 전체나 비밀값을 출력하지 않아요.

## 7. 검증

Server 검증:

- v3 artifact exact schema와 세 캐릭터 mapping 테스트
- 각 캐릭터 field·값·단위 fixture 테스트
- 구버전, 필수 field 누락, 중복 type, 0/음수 값 거부 테스트
- `make ci`

Client 소비자 권장 검증(Server PR acceptance 범위 밖):

- v3 실제 artifact parse 성공 EditMode 테스트
- v1/v2 version 거부 테스트
- 각 필수 field 누락과 `null`이 실패하는 table test
- 중복/미지원 type과 0/음수 값 거부 테스트
- build preprocessor가 invalid download를 쓰지 않고 build를 실패시키는 테스트

Server PR 검증 순서는 다음과 같아요.

1. embedded `client-config/game-config.json`을 Go parser로 읽어요.
2. 세 캐릭터 mapping, 필수 field, version과 invalid fixture를 확인해요.
3. 문서 drift validator와 전체 `make ci`를 실행해요.
4. OpenAPI/AsyncAPI가 바뀌지 않았는지 확인하고 결과를 Server PR과 SL-99 댓글에 남겨요.

## 8. 전달과 순서

Server PR 하나가 canonical v3 artifact, Go validator, 소유권 문서를 제공해요. Client 코드 기여는 이 PR에 포함하지 않고 Client 저장소 소유자가 필요할 때 v3 소비 계약을 반영해요. Server PR이 merge되기 전에는 SL-99를 `Done`으로 옮기지 않아요.
