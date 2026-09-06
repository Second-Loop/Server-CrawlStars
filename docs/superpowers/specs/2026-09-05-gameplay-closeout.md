# SL-123 입력·대기·스킬 마무리 명세

Linear SL-123의 2026-09-05 최종 마무리 구현 기준을 적용해요. 팀 승패, 모드, 봇 정책, 영속 저장은 바꾸지 않아요. merge·배포는 별도예요.

1. 양수 ClientTick 입력에서 tick 처리 전 최신 액션(공격/스킬 flag 묶음과 해당 조준)을 이동 전용 입력이 지우지 못하게 해요. 이동 방향과 ACK는 최신 입력을 따라가요. 여러 액션은 마지막 액션 전체가 승리해요. stale/duplicate는 버리고, 소비·disconnect 뒤에 재생하지 않아요. 양쪽 중 하나가 legacy ClientTick=0이면 기존 전체 덮어쓰기를 유지해요. 쿨타임 이후 실행하는 예약 큐는 만들지 않아요.
2. Loading 진입 시각부터 human Ready ACK 대기는 30초예요. deadline 직전 모든 human이 준비하면 Starting으로 넘어가고, 정확한 경계 이후 ACK는 worker 실행이 늦어도 허용하지 않아요. 미응답이면 기존 prestart cancel 규칙으로 방·세션·자격·ID·timer를 정리해요. 봇 ACK는 필요 없어요. 정상 시작·삭제·shutdown은 타이머를 취소해요. store→room lock 순서를 유지해요.
3. Shelly 스킬은 승인 tick을 첫 이동으로 하는 고정 방향 9 tick(30Hz에서 0.3초) 대시예요. 총 2.7 tiles를 매 tick 1/9씩 swept collision로 이동해요. 승인 tick부터 마지막 tick까지 일반 이동·공격을 막아요. 조준을 바꿔도 대시 방향은 고정돼요. 벽/물/경계/live player 충돌이나 사망 때 즉시 중단해요. 여러 대시의 충돌은 기존 결정적 동시 처리 방식으로 계산해요. 탄약 재충전·스킬 cooldown 승인은 기존처럼 한 번만 적용해요. 입력 ACK는 대시 중에도 처리해요. 마지막 이동 snapshot에서 IsDashing=false가 되고 다음 Step부터 공격 가능해요. 공격 잠금은 AttackReadyTick에도 투영해요. 사망 snapshot에 stale dash/lock 상태가 남지 않게 해요.
4. 밸런스는 Shelly normal damage 280→252, dash 5.4→2.7 tiles; Colt skill count 12→10(마지막 offset 18,20 제거, 나머지 유지), skill range11→9.35 tiles; Lily normal damage1100→1210, skill range10.4→12.48 tiles예요. 그 외 수치는 유지해요.
5. 서버 config는 필수 dashDurationTicks=9를 추가하고 version6→7로 올려요. client config는 version3을 유지해요. 기존 client 표시 거리 보정은 별도 계약이므로 이 서버 작업에서 임의의 환산 규칙을 추가하지 않아요. 새 IsDashing은 additive PlayerData 필드로 AsyncAPI·사람용 문서·소스 검증을 함께 반영해요. OpenAPI 영향도 확인해요.

검증: 행동 회귀를 먼저 실패시킨 뒤 수정해요. task 단위 독립 리뷰와 전체 브랜치 리뷰를 수행해요. 최종 동일 HEAD make ci와 race를 실행해요. Unity 실플레이는 자동 서버 검증과 구분해요.
