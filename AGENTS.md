# Agent 지침

여기서 시작한 뒤, 자세한 작업 합의는 `ai-docs/workflow.md`를 읽습니다.

- Linear를 task source of truth로 취급합니다.
- 변경 범위는 활성 issue에 맞춰 제한합니다.
- 완료를 주장하기 전에 관련 validation을 실행합니다.
- 코드, workflow, architecture가 바뀌면 `ai-docs/`를 업데이트합니다.
- REST/WebSocket 계약이 바뀌면 같은 변경에서 `api/openapi.yaml`,
  `api/asyncapi.yaml`, `ai-docs/api-reference.md`를 함께 확인하고
  필요한 부분을 업데이트합니다.

Linear issue가 명시적으로 범위에 포함하지 않는 한 gameplay loop, matchmaking, persistence, Kubernetes, dashboard, scheduler, runner, multi-agent orchestration을 추가하지 않습니다.
