# Linear Control Model

## Goal

Linear를 project intent, task scope, completion state의 source of truth로 사용합니다. GitHub는 implementation과 review surface로 유지합니다.

이 model은 project name, team key, label만 바꾸면 다른 personal project에도 재사용할 수 있어야 합니다.

## Ownership Boundaries

```text
Linear Project      product 또는 큰 initiative
Linear Epic Issue   milestone 또는 phase
Linear Child Issue  review 가능한 work unit
Git Branch          하나의 issue를 위한 implementation workspace
GitHub PR           review와 CI unit
CI + Review         completion gate
```

Linear가 소유하는 것:

- work가 존재하는 이유
- scope에 포함되는 것
- acceptance criteria
- validation contract
- task status
- follow-up decomposition

GitHub가 소유하는 것:

- code diff
- CI result
- review discussion
- merge history

## Status Flow

기존 `Second Loop` status는 다음 의미로 사용합니다.

```text
Backlog
  idea는 있지만 scope가 준비되지 않음

Todo
  implementation 준비 완료: scope, acceptance criteria, validation이 작성됨

In Progress
  branch가 있거나 active local work가 시작됨

In Review
  GitHub PR이 열렸고 CI 또는 human review를 기다림

Done
  PR이 merge되었고 CI, docs, follow-up 처리가 끝남

Canceled / Duplicate
  implementation 없이 의도적으로 닫힘
```

Local validation이 통과했다는 이유만으로 issue를 `Done`으로 옮기지 않습니다. `Done`은 GitHub PR이 merge되었거나 code 없이 명시적으로 완료된 issue를 의미합니다.

## Definition Of Ready

Issue가 implementation 준비 상태가 되려면 다음이 있어야 합니다.

- parent epic 또는 project
- summary
- scope
- out of scope
- acceptance criteria
- validation steps
- owner 또는 명시적인 unassigned 상태
- 관련 docs, decisions, related issues link

이 field가 부족한 task는 `Backlog`에 두거나 implementation 전에 refine합니다.

## Definition Of Done

Issue가 완료되려면 다음이 충족되어야 합니다.

- linked PR이 merge되었거나 issue가 non-code work로 닫힘
- code가 바뀐 경우 merged PR의 CI 통과
- validation command/result가 PR 또는 Linear comment에 기록됨
- docs가 업데이트되었거나 변경 없음이 명시됨
- follow-up work가 별도 issue로 분리됨

## Epic Decomposition

Epic issue는 모호한 bucket이 아니라 phase로 사용합니다.

좋은 epic 예:

- `E0 Project bootstrap`
- `E1 First playable vertical slice`
- `E2 Reliable multiplayer loop`

Child issue는 하나의 집중된 PR로 처리할 수 있을 만큼 작아야 합니다. 좋은 child issue는 보통 다음 중 하나를 변경합니다.

- repo/setup
- one server capability
- one client capability
- one protocol contract
- one validation or tooling path
- one documentation decision

Issue가 여러 독립 outcome을 포함하면 implementation 전에 분리합니다.

## Recommended E0 Shape

현재 구조:

```text
SL-1  [EPIC] E0 project kickoff and bootstrap
  SL-3  [Server] basic dev environment and repo setup
  SL-4  [Client] basic dev environment and repo setup
  SL-5  development spec confirmation
```

권장 follow-up child issue:

- `Define Linear and GitHub control model`
- `Configure GitHub main branch rules`
- `Verify Linear GitHub integration`
- `Prepare first server vertical slice plan`
- `Draft protocol contract format`
- `Decide deployment target for early smoke tests`

Team이 각 issue를 따로 추적할 가치가 있다고 합의하기 전에는 자동으로 만들지 않습니다.

## Issue Template

```md
### Summary

One or two sentences explaining the intended outcome.

### Scope

- 

### Out Of Scope

- 

### Acceptance Criteria

- [ ] 

### Validation

- [ ] 

### GitHub

- Branch:
- PR:

### Notes / Risks
```

## Branch And PR Mapping

Branch name은 Linear issue ID로 시작해야 합니다.

```text
sl-3-server-bootstrap
sl-6-linear-control-model
```

PR title은 issue ID를 포함해야 합니다.

```text
[SL-3] Bootstrap server repository
```

PR body는 다음을 포함해야 합니다.

- Linear issue link
- summary
- scope of changes
- validation performed
- risks and follow-ups

## Manual Control Loop

신뢰할 수 있는 automation이 생기기 전까지:

1. Implementation이 시작되면 issue를 `In Progress`로 옮깁니다.
2. Issue 이름에 맞춘 branch를 만듭니다.
3. Commit message에 issue ID를 포함합니다.
4. Issue와 연결된 PR을 엽니다.
5. Issue를 `In Review`로 옮깁니다.
6. Merge 후 issue를 `Done`으로 옮깁니다.
7. Follow-up work가 생기면 merged issue를 확장하지 말고 child 또는 related issue를 만듭니다.

## Automation Boundary

현재 Linear MCP access로 가능한 작업:

- team, project, issue, status, label, cycle, document listing
- issue 생성 및 업데이트
- issue status 변경
- comment 추가
- project 또는 issue document 생성
- MCP가 지원하는 issue relation 추가

Linear PAT 또는 direct GraphQL access가 필요할 수 있는 작업:

- workspace-level issue template
- team workflow customization
- GitHub integration 설치 또는 admin settings
- custom workspace automation
- broad migration scripts
- MCP에 노출되지 않은 direct GraphQL operation

MCP를 먼저 사용합니다. 구체적 operation이 MCP 또는 Linear UI로 불가능할 때만 PAT를 추가합니다.

## Reusable Personal Default

향후 project는 다음으로 시작합니다.

```text
Project
  E0 Bootstrap
    repo bootstrap
    GitHub/Linear integration
    workflow and CI
    first slice plan

  E1 First Vertical Slice
    protocol draft
    minimal backend capability
    minimal client capability
    end-to-end smoke test
```

Default labels:

- `Feature`
- `Bug`
- `Improvement`
- `discussion`
- `tooling`
- `docs`

Default statuses:

- `Backlog`
- `Todo`
- `In Progress`
- `In Review`
- `Done`
- `Canceled`
- `Duplicate`
