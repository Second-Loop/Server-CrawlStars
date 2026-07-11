# SL-81 Stack 6 Deploy Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refuse to extract or restart a GitHub release whose published SHA-256 manifest is missing, malformed, or mismatched.

**Architecture:** Download the tarball and `SHA256SUMS` from the same resolved release URL into one temporary directory, select the exact asset entry, and run `sha256sum -c` before extraction. A dependency-injected shell regression harness proves no `tar` or `systemctl restart` call occurs on verification failure.

**Tech Stack:** Bash, curl, GNU `sha256sum`, shell stubs, Make.

## Global Constraints

- Verification happens before `tar`, symlink changes, copy, or systemd restart.
- The checksum manifest URL uses the same `RELEASE_TAG` resolution as the asset URL.
- Missing manifest, missing exact asset filename, malformed digest, and mismatch all fail closed.
- Preserve existing rollback behavior for failures that happen after installation begins.

---

### Task 1: Add a shell regression harness

**Files:**
- Create: `scripts/deploy/pull-latest_test.sh`
- Modify: `Makefile`

**Interfaces:**
- Produces: `make deploy-test`, using temporary fake `curl`, `tar`, `systemctl`, `sudo`, and filesystem roots without touching the real machine.

- [ ] **Step 1: Write failing valid/mismatch tests**

The fake downloader writes a deterministic tarball and either a matching or mismatching `SHA256SUMS`. Record every fake `tar` and `systemctl` invocation. Assert valid input reaches extraction/restart while mismatch exits non-zero with neither invocation.

- [ ] **Step 2: Add missing/malformed cases**

Cover manifest download failure, manifest without the exact `ASSET_NAME`, non-64-hex digest, and checksum command failure. Each case must assert install/restart markers are absent.

- [ ] **Step 3: Run and verify RED**

Run: `bash scripts/deploy/pull-latest_test.sh`

Expected: mismatch/missing cases currently continue to extraction or the test reports that no manifest download occurred.

- [ ] **Step 4: Add `deploy-test` to Make**

Declare the target phony and make `ci` depend on `deploy-test` after syntax checking.

- [ ] **Step 5: Commit the failing regression harness**

```text
[SL-81] test(deploy): release checksum 회귀 시나리오 추가

- 정상과 누락 및 mismatch manifest 경로 구성
- 검증 실패 전 extract와 restart 차단 assertion 추가
```

### Task 2: Verify the release manifest before extraction

**Files:**
- Modify: `scripts/deploy/pull-latest.sh`
- Modify: `scripts/deploy/pull-latest_test.sh`

**Interfaces:**
- Produces: `release_url(asset_name)` and `verify_checksum(manifest_path, package_path, asset_name)` shell functions.

- [ ] **Step 1: Generalize release URL construction**

Make the helper accept an asset name so both the package and `SHA256SUMS` resolve through `latest/download` or the same explicit release tag.

- [ ] **Step 2: Download and constrain the manifest**

Download `${tmpdir}/SHA256SUMS`, select exactly one line whose filename is `ASSET_NAME` (allow the standard optional leading `*`), validate a 64-character lowercase/uppercase hex digest, and write a one-entry `${tmpdir}/SHA256SUMS.selected` using the local package basename.

- [ ] **Step 3: Run GNU verification before tar**

Execute `(cd "${tmpdir}" && sha256sum -c --strict SHA256SUMS.selected)` before creating/extracting the package directory. Print a concise verified-asset message without echoing authorization headers.

- [ ] **Step 4: Run the shell tests and syntax checks**

Run: `bash scripts/deploy/pull-latest_test.sh && bash -n scripts/deploy/*.sh`

Expected: every case passes and mismatch markers prove no extraction/restart.

- [ ] **Step 5: Commit verification**

```text
[SL-81] fix(deploy): release SHA256 검증 강제

- package와 같은 release의 manifest 다운로드
- checksum 통과 전 extract와 restart 차단
```

### Task 3: Refresh deployment and final current-state docs

**Files:**
- Modify: `ai-docs/deployment.md`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/workflow.md` only if validation commands change.
- Modify: `ai-docs/decisions.md`
- Modify: `ai-docs/improvement-report.md`

**Interfaces:**
- Produces: final SL-81 operational state and `make ci` contract including deploy checksum tests.

- [ ] **Step 1: Document checksum trust boundary**

Explain same-release corruption/tamper detection, the `sha256sum` runtime dependency, failure before extraction, and the limitation that a compromised GitHub release can replace both asset and manifest.

- [ ] **Step 2: Mark report items resolved by stack**

Append a concise resolution section mapping P0-1 through P3-1 to PR stacks 1-6 without deleting the original analysis.

- [ ] **Step 3: Run final full-stack validation**

Run: `make ci`

Run: `mise exec -- go test -race ./... -count=10`

Run: `git diff --check main...HEAD`

Expected: all commands exit 0, no race report, and no whitespace errors.

- [ ] **Step 4: Commit final documentation**

```text
[SL-81] docs(deploy): hardening 완료 상태 반영

- checksum 실패와 운영 복구 절차 문서화
- 개선 보고서에 stacked PR 해결 범위 기록
```

- [ ] **Step 5: Prepare the final Linear evidence**

Collect six PR URLs, branch bases, exact validation commands/results, benchmark summary, and review order in one concise SL-81 comment. Move the issue to `In Review`, not `Done`.
