# GitHub

## Repository

- Remote: `https://github.com/Second-Loop/Server-CrawlStars`
- Visibility: public
- Current local access check: `gh repo view` reports admin access.

부트스트랩 시점에는 레포지토리에 committed default branch가 없었습니다. Initial baseline이 push된 이후 새 작업은 branch와 PR을 통해 진행해야 합니다.

## Current Settings Snapshot

2026-05-16 첫 PR 이전, repository를 public으로 전환한 뒤 확인한 내용:

- Default branch: GraphQL에서는 없음, REST에서는 configured default branch name이 `main`으로 보고됨.
- `main` branch protection: branch가 아직 없어서 존재하지 않음.
- Branches: remote에 없음.
- Workflows: remote에 아직 없음.
- Repository issues: enabled.
- Repository projects: enabled.
- Repository wiki: disabled.
- Merge commit: enabled.
- Squash merge: enabled.
- Rebase merge: enabled.
- Delete branch on merge: enabled.
- Auto-merge: disabled.
- Actions: enabled.
- Allowed Actions: all.
- SHA pinning required: false.
- Rulesets: none.
- Branch protection rules: none.
- Webhooks: none.
- Repository teams: none.
- Direct collaborators: `Tolerblanc` admin, `SikPang` admin.
- GitHub GraphQL access: `gh api graphql`로 동작 확인.
- Codex GitHub connector access: repository가 public이 된 뒤 동작 확인. Baseline branch가 아직 push되지 않아 branch search는 branch 없음으로 반환.

Baseline commit이 들어간 뒤 권장되는 확인:

- Team이 squash-only merge를 원한다면 merge commit과 rebase merge를 비활성화합니다.
- `main`에 required PR review와 required CI checks를 추가합니다.
- Linear GitHub integration이 organization/repository level에 설치되어 있는지 다시 확인합니다.

## PR Rules

모든 implementation PR은 다음을 포함해야 합니다.

- Linked Linear issue
- Summary
- Scope of changes
- Validation commands and results
- Known risks or follow-ups

CI가 실패하면 merge하지 않습니다. Human review 전에 작업을 완료로 취급하지 않습니다.

## Branch Protection Notes

원하는 rule:

- `main` 직접 push 금지
- PR review required
- CI required
- squash merge preferred

Remote branch가 없어서 branch protection을 적용하거나 검증할 수 없었습니다. 첫 baseline branch가 push된 뒤 다시 확인합니다.
