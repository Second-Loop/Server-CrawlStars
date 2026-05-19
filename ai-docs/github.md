# GitHub

## Repository

- Remote: `https://github.com/Second-Loop/Server-CrawlStars`
- Visibility: public
- Current local access check: `gh repo view` reports admin access.

At bootstrap time, the repository has no committed default branch yet. After the initial baseline is pushed, new work should happen through branches and PRs.

## Current Settings Snapshot

Checked on 2026-05-16 before the first PR, after switching the repository to public:

- Default branch: none through GraphQL, while REST reports the configured default branch name as `main`.
- `main` branch protection: not present yet because the branch does not exist.
- Branches: none on remote.
- Workflows: none on remote yet.
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
- GitHub GraphQL access: works through `gh api graphql`.
- Codex GitHub connector access: works after the repository was made public; branch search returns no branches because the baseline branch has not been pushed yet.

Recommended after the baseline commit lands:

- Disable merge commits and rebase merges if the team wants squash-only merges.
- Add required PR review and required CI checks for `main`.
- Re-check whether Linear's GitHub integration is installed at the organization/repository level.

## PR Rules

Every implementation PR should include:

- Linked Linear issue
- Summary
- Scope of changes
- Validation commands and results
- Known risks or follow-ups

Do not merge when CI is failing. Do not treat work as complete before human review.

## Branch Protection Notes

The desired rule is:

- no direct pushes to `main`
- PR review required
- CI required
- squash merge preferred

Branch protection could not be applied or verified because no remote branch exists yet. Re-check this after the first baseline branch is pushed.
