# Linear Control Model

## Goal

Use Linear as the source of truth for project intent, task scope, and completion state. GitHub remains the implementation and review surface.

This model should be reusable across other personal projects with only project names, team keys, and labels changed.

## Ownership Boundaries

```text
Linear Project      product or large initiative
Linear Epic Issue   milestone or phase
Linear Child Issue  reviewable work unit
Git Branch          implementation workspace for one issue
GitHub PR           review and CI unit
CI + Review         completion gate
```

Linear owns:

- why the work exists
- what is in scope
- acceptance criteria
- validation contract
- status of the task
- follow-up decomposition

GitHub owns:

- code diff
- CI result
- review discussion
- merge history

## Status Flow

Use the existing `Second Loop` statuses this way:

```text
Backlog
  idea exists, scope is not ready

Todo
  ready to implement: scope, acceptance criteria, and validation are written

In Progress
  branch exists or active local work has started

In Review
  GitHub PR is open and waiting for CI or human review

Done
  PR merged, CI passed, docs and follow-ups are handled

Canceled / Duplicate
  work is intentionally closed without implementation
```

Do not move an issue to `Done` because local validation passed. `Done` means the GitHub PR is merged or the issue was explicitly completed without code.

## Definition Of Ready

An issue is ready for implementation when it has:

- parent epic or project
- summary
- scope
- out of scope
- acceptance criteria
- validation steps
- owner or explicit unassigned state
- links to relevant docs, decisions, or related issues

If a task lacks these fields, keep it in `Backlog` or refine it before implementation.

## Definition Of Done

An issue is done when:

- linked PR is merged or the issue is closed as non-code work
- CI passed for the merged PR when code changed
- validation commands/results are recorded in the PR or Linear comment
- docs were updated or explicitly confirmed unchanged
- follow-up work is split into separate issues

## Epic Decomposition

Use epic issues for phases, not vague buckets.

Good epic examples:

- `E0 Project bootstrap`
- `E1 First playable vertical slice`
- `E2 Reliable multiplayer loop`

Child issues should be small enough for one focused PR. A good child issue usually changes one of:

- repo/setup
- one server capability
- one client capability
- one protocol contract
- one validation or tooling path
- one documentation decision

When an issue contains multiple independent outcomes, split it before implementation.

## Recommended E0 Shape

Current structure:

```text
SL-1  [EPIC] E0 project kickoff and bootstrap
  SL-3  [Server] basic dev environment and repo setup
  SL-4  [Client] basic dev environment and repo setup
  SL-5  development spec confirmation
```

Suggested follow-up child issues:

- `Define Linear and GitHub control model`
- `Configure GitHub main branch rules`
- `Verify Linear GitHub integration`
- `Prepare first server vertical slice plan`
- `Draft protocol contract format`
- `Decide deployment target for early smoke tests`

Do not create these automatically unless the team agrees that each issue is worth tracking separately.

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

Branch names should start with the Linear issue ID:

```text
sl-3-server-bootstrap
sl-6-linear-control-model
```

PR titles should include the issue ID:

```text
[SL-3] Bootstrap server repository
```

PR bodies should include:

- Linear issue link
- summary
- scope of changes
- validation performed
- risks and follow-ups

## Manual Control Loop

Until reliable automation exists:

1. Move issue to `In Progress` when implementation starts.
2. Create a branch named after the issue.
3. Commit with the issue ID in the message.
4. Open a PR linked to the issue.
5. Move issue to `In Review`.
6. After merge, move issue to `Done`.
7. If follow-up work appears, create child or related issues instead of expanding the merged issue.

## Automation Boundary

Current Linear MCP access is enough for:

- listing teams, projects, issues, statuses, labels, cycles, and documents
- creating and updating issues
- changing issue status
- adding comments
- creating project or issue documents
- adding issue relations that the MCP supports

Linear PAT or direct GraphQL access may be needed for:

- workspace-level issue templates
- team workflow customization
- GitHub integration installation or admin settings
- custom workspace automations
- broad migration scripts
- direct GraphQL operations not exposed by MCP

Prefer MCP first. Add PAT only when a concrete operation cannot be performed through MCP or the Linear UI.

## Reusable Personal Default

For future projects, start with:

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

