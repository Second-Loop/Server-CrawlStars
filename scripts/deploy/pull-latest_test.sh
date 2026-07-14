#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_SCRIPT="$SCRIPT_DIR/pull-latest.sh"
REAL_MKTEMP="$(command -v mktemp)"
REAL_SHA256SUM="$(command -v sha256sum)"
TEST_ROOT="$("$REAL_MKTEMP" -d)"
TESTS=0
trap 'rm -rf "$TEST_ROOT"' EXIT

fail() {
  echo "not ok - $*" >&2
  exit 1
}

ok() {
  TESTS=$((TESTS + 1))
  echo "ok $TESTS - $1"
}

assert_success() {
  [ "$CASE_STATUS" -eq 0 ] || fail "$1: expected success, status=$CASE_STATUS output=$(cat "$CASE_OUTPUT")"
}

assert_failure() {
  [ "$CASE_STATUS" -ne 0 ] || fail "$1: expected failure"
}

assert_contains() {
  grep -F -q -- "$1" "$2" || fail "expected $2 to contain: $1"
}

assert_not_contains() {
  if grep -F -q -- "$1" "$2"; then
    fail "expected $2 not to contain: $1"
  fi
}

assert_count() {
  local pattern="$1"
  local file="$2"
  local want="$3"
  local got
  got="$(grep -F -c -- "$pattern" "$file" || true)"
  [ "$got" -eq "$want" ] || fail "expected $want occurrences of $pattern in $file, got $got"
}

assert_before() {
  local first
  local second
  first="$(grep -n -m1 -F -- "$1" "$3" | cut -d: -f1 || true)"
  second="$(grep -n -m1 -F -- "$2" "$3" | cut -d: -f1 || true)"
  [ -n "$first" ] && [ -n "$second" ] && [ "$first" -lt "$second" ] ||
    fail "expected $1 before $2 in $3"
}

setup_case() {
  CASE_ROOT="$TEST_ROOT/$1"
  FAKE_BIN="$CASE_ROOT/fake-bin"
  CASE_DATA="$CASE_ROOT/data"
  FAKE_TMPDIR="$CASE_ROOT/deploy-tmp"
  INSTALL_ROOT_VALUE="$CASE_ROOT/install"
  EVENTS="$CASE_ROOT/events"
  CASE_OUTPUT="$CASE_ROOT/output"
  PACKAGE_SOURCE="$CASE_DATA/package.tar.gz"
  MANIFEST_SOURCE="$CASE_DATA/SHA256SUMS"
  METADATA_SOURCE="$CASE_DATA/latest.json"
  SYSTEMCTL_COUNT="$CASE_ROOT/systemctl-count"
  CASE_ASSET_NAME="crawl-stars-server-linux-amd64.tar.gz"
  CASE_RELEASE_TAG="latest"
  CASE_TOKEN=""
  FAKE_CURL_FAIL=""
  FAKE_RESTART_FAIL=0
  FAKE_SMOKE_FAIL=0

  mkdir -p "$FAKE_BIN" "$CASE_DATA" "$INSTALL_ROOT_VALUE/releases"
  : > "$EVENTS"
  printf 'package payload for %s\n' "$1" > "$PACKAGE_SOURCE"
  PACKAGE_HASH="$("$REAL_SHA256SUM" "$PACKAGE_SOURCE" | awk '{print $1}')"
  printf '%s  %s\n' "$PACKAGE_HASH" "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE"
  printf '{"tag_name":"server-deadbeef"}\n' > "$METADATA_SOURCE"

  cat > "$FAKE_BIN/id" <<'FAKE'
#!/usr/bin/env bash
if [ "$1" = "-u" ]; then
  echo 0
  exit 0
fi
exec /usr/bin/id "$@"
FAKE

  cat > "$FAKE_BIN/mktemp" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
echo "mktemp|$*" >> "$EVENTS"
mkdir -p "$FAKE_TMPDIR"
printf '%s\n' "$FAKE_TMPDIR"
FAKE

  cat > "$FAKE_BIN/curl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
output=""
url=""
header_file=""
if [ -n "$EXPECTED_TOKEN" ]; then
  for argument in "$@"; do
    case "$argument" in
      *"$EXPECTED_TOKEN"*) echo "token_argv=set" >> "$EVENTS" ;;
    esac
  done
fi
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output)
      output="$2"
      shift 2
      ;;
    -H|--header)
      case "$2" in
        @*) header_file="$(printf '%s' "$2" | cut -c2-)" ;;
      esac
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

echo "curl|$url|$output" >> "$EVENTS"
if env | grep -q '^GH_TOKEN='; then
  echo "token_env=set" >> "$EVENTS"
fi
if [ -n "$header_file" ]; then
  if mode="$(stat -f '%Lp' "$header_file" 2>/dev/null)"; then
    :
  else
    mode="$(stat -c '%a' "$header_file")"
  fi
  echo "header_mode=$mode" >> "$EVENTS"
  if [ -n "$EXPECTED_TOKEN" ]; then
    IFS= read -r auth_line < "$header_file"
    [ "$auth_line" = "Authorization: Bearer $EXPECTED_TOKEN" ] || exit 97
  fi
fi

case "$url" in
  "https://api.github.com/repos/Second-Loop/Server-CrawlStars/releases/latest")
    [ "$FAKE_CURL_FAIL" != "api" ] || exit 22
    cp "$METADATA_SOURCE" "$output"
    ;;
  "$SMOKE_URL")
    [ "$FAKE_SMOKE_FAIL" -eq 0 ] || exit 22
    ;;
  */SHA256SUMS)
    [ "$FAKE_CURL_FAIL" != "manifest" ] || exit 22
    cp "$MANIFEST_SOURCE" "$output"
    ;;
  */*)
    [ "$FAKE_CURL_FAIL" != "package" ] || exit 22
    cp "$PACKAGE_SOURCE" "$output"
    ;;
  *)
    echo "unexpected URL: $url" >&2
    exit 98
    ;;
esac
FAKE

  cat > "$FAKE_BIN/sha256sum" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
echo "sha256sum|$*" >> "$EVENTS"
exec "$REAL_SHA256SUM" "$@"
FAKE

  cat > "$FAKE_BIN/tar" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
echo "tar|$*" >> "$EVENTS"
destination=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-C" ]; then
    destination="$2"
    shift 2
  else
    shift
  fi
done
[ -n "$destination" ] || exit 96
mkdir -p "$destination"
printf '#!/usr/bin/env sh\nexit 0\n' > "$destination/crawl-stars-server"
chmod 0755 "$destination/crawl-stars-server"
printf 'deadbeef\n' > "$destination/VERSION"
printf 'deadbeef\n' > "$destination/COMMIT_SHA"
FAKE

  cat > "$FAKE_BIN/chown" <<'FAKE'
#!/usr/bin/env bash
echo "chown|$*" >> "$EVENTS"
exit 0
FAKE

  cat > "$FAKE_BIN/systemctl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
count=0
if [ -f "$SYSTEMCTL_COUNT" ]; then
  count="$(cat "$SYSTEMCTL_COUNT")"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$SYSTEMCTL_COUNT"
echo "systemctl|$*" >> "$EVENTS"
if [ "$1" = "restart" ] && [ "$FAKE_RESTART_FAIL" -eq 1 ] && [ "$count" -eq 1 ]; then
  exit 1
fi
FAKE

  cat > "$FAKE_BIN/sleep" <<'FAKE'
#!/usr/bin/env bash
echo "sleep|$*" >> "$EVENTS"
FAKE

  chmod 0755 "$FAKE_BIN"/*
}

run_case() {
  set +e
  PATH="$FAKE_BIN:/usr/bin:/bin:/usr/sbin:/sbin" \
    EVENTS="$EVENTS" \
    FAKE_TMPDIR="$FAKE_TMPDIR" \
    PACKAGE_SOURCE="$PACKAGE_SOURCE" \
    MANIFEST_SOURCE="$MANIFEST_SOURCE" \
    METADATA_SOURCE="$METADATA_SOURCE" \
    SYSTEMCTL_COUNT="$SYSTEMCTL_COUNT" \
    REAL_SHA256SUM="$REAL_SHA256SUM" \
    EXPECTED_TOKEN="$CASE_TOKEN" \
    FAKE_CURL_FAIL="$FAKE_CURL_FAIL" \
    FAKE_RESTART_FAIL="$FAKE_RESTART_FAIL" \
    FAKE_SMOKE_FAIL="$FAKE_SMOKE_FAIL" \
    REPO="Second-Loop/Server-CrawlStars" \
    SERVICE_NAME="crawl-stars-server" \
    RUN_USER="crawlstars" \
    INSTALL_ROOT="$INSTALL_ROOT_VALUE" \
    ASSET_NAME="$CASE_ASSET_NAME" \
    RELEASE_TAG="$CASE_RELEASE_TAG" \
    GH_TOKEN="$CASE_TOKEN" \
    SMOKE_URL="http://127.0.0.1:18080/health" \
    bash "$DEPLOY_SCRIPT" > "$CASE_OUTPUT" 2>&1
  CASE_STATUS=$?
  set -e
}

invalid_names=(
  ""
  "."
  ".."
  "../escape"
  "dir/asset.tar.gz"
  'dir\asset.tar.gz'
  "asset name.tar.gz"
  "asset%2ftar.gz"
  "asset?name"
  "asset#name"
  "SHA256SUMS"
  $'asset\nname'
)
long_name="$(printf 'a%.0s' {1..256})"
invalid_names+=("$long_name")
invalid_index=0
for invalid_name in "${invalid_names[@]}"; do
  setup_case "invalid-$invalid_index"
  CASE_ASSET_NAME="$invalid_name"
  run_case
  assert_failure "invalid ASSET_NAME $invalid_index"
  [ ! -s "$EVENTS" ] || fail "invalid ASSET_NAME reached side effects: $(cat "$EVENTS")"
  invalid_index=$((invalid_index + 1))
done
ok "unsafe ASSET_NAME values fail before mktemp and network"

setup_case "latest-pinned"
run_case
assert_success "latest pinned deploy"
assert_count "curl|https://api.github.com/repos/Second-Loop/Server-CrawlStars/releases/latest|" "$EVENTS" 1
assert_count "curl|https://github.com/Second-Loop/Server-CrawlStars/releases/download/server-deadbeef/crawl-stars-server-linux-amd64.tar.gz|" "$EVENTS" 1
assert_count "curl|https://github.com/Second-Loop/Server-CrawlStars/releases/download/server-deadbeef/SHA256SUMS|" "$EVENTS" 1
assert_not_contains "/releases/latest/download/" "$EVENTS"
assert_before "sha256sum|--strict" "tar|" "$EVENTS"
assert_before "tar|" "systemctl|restart" "$EVENTS"
ok "latest resolves once and pins both downloads to one tag"

setup_case "explicit-tag"
CASE_RELEASE_TAG="server/explicit"
run_case
assert_success "explicit tag deploy"
assert_count "api.github.com" "$EVENTS" 0
assert_count "/releases/download/server%2Fexplicit/crawl-stars-server-linux-amd64.tar.gz|" "$EVENTS" 1
assert_count "/releases/download/server%2Fexplicit/SHA256SUMS|" "$EVENTS" 1
ok "explicit tag skips latest API and is URL encoded"

setup_case "custom-safe-asset"
CASE_ASSET_NAME="crawl_stars-server.2026-07-14.tgz"
printf '%s  %s\n' "$PACKAGE_HASH" "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE"
run_case
assert_success "custom safe asset"
assert_count "/releases/download/server-deadbeef/crawl_stars-server.2026-07-14.tgz|" "$EVENTS" 1
ok "safe custom ASSET_NAME remains supported"

setup_case "empty-tag"
CASE_RELEASE_TAG=""
run_case
assert_failure "empty explicit tag"
[ ! -s "$EVENTS" ] || fail "empty RELEASE_TAG reached side effects"
ok "explicit empty RELEASE_TAG fails closed"

for metadata_case in missing malformed latest; do
  setup_case "metadata-$metadata_case"
  case "$metadata_case" in
    missing) printf '{}\n' > "$METADATA_SOURCE" ;;
    malformed) printf '{not-json\n' > "$METADATA_SOURCE" ;;
    latest) printf '{"tag_name":"latest"}\n' > "$METADATA_SOURCE" ;;
  esac
  run_case
  assert_failure "metadata $metadata_case"
  assert_count "api.github.com/repos/Second-Loop/Server-CrawlStars/releases/latest" "$EVENTS" 1
  assert_not_contains "/releases/download/" "$EVENTS"
  assert_not_contains "tar|" "$EVENTS"
  assert_not_contains "systemctl|" "$EVENTS"
done
ok "malformed or mutable latest metadata fails before download"

setup_case "api-failure"
FAKE_CURL_FAIL="api"
run_case
assert_failure "latest API failure"
assert_not_contains "/releases/download/" "$EVENTS"
assert_not_contains "tar|" "$EVENTS"
ok "latest API failure stops deployment"

for failure_kind in package manifest; do
  setup_case "download-$failure_kind"
  FAKE_CURL_FAIL="$failure_kind"
  run_case
  assert_failure "$failure_kind download failure"
  assert_not_contains "sha256sum|--strict" "$EVENTS"
  assert_not_contains "tar|" "$EVENTS"
  assert_not_contains "systemctl|" "$EVENTS"
done
ok "asset download failures stop before verification and install"

for marker_case in text binary uppercase; do
  setup_case "checksum-$marker_case"
  case "$marker_case" in
    text) printf '%s  %s\n' "$PACKAGE_HASH" "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE" ;;
    binary) printf '%s *%s\n' "$PACKAGE_HASH" "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE" ;;
    uppercase)
      upper_hash="$(printf '%s' "$PACKAGE_HASH" | tr '[:lower:]' '[:upper:]')"
      printf '%s  %s\n' "$upper_hash" "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE"
      ;;
  esac
  run_case
  assert_success "valid $marker_case checksum"
done
ok "GNU text, binary, and uppercase checksums are accepted"

for manifest_case in missing duplicate malformed mismatch; do
  setup_case "manifest-$manifest_case"
  case "$manifest_case" in
    missing) printf '%s  other.tar.gz\n' "$PACKAGE_HASH" > "$MANIFEST_SOURCE" ;;
    duplicate)
      printf '%s  %s\n%s  %s\n' "$PACKAGE_HASH" "$CASE_ASSET_NAME" "$PACKAGE_HASH" "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE"
      ;;
    malformed) printf 'not-a-checksum  %s\n' "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE" ;;
    mismatch) printf '%064d  %s\n' 0 "$CASE_ASSET_NAME" > "$MANIFEST_SOURCE" ;;
  esac
  run_case
  assert_failure "invalid manifest $manifest_case"
  assert_not_contains "tar|" "$EVENTS"
  assert_not_contains "systemctl|" "$EVENTS"
done
ok "missing, duplicate, malformed, and mismatched checksums fail closed"

setup_case "token"
CASE_TOKEN="github_pat_secret_sentinel"
run_case
assert_success "token auth deploy"
assert_contains "header_mode=600" "$EVENTS"
assert_not_contains "token_env=set" "$EVENTS"
assert_not_contains "token_argv=set" "$EVENTS"
assert_not_contains "$CASE_TOKEN" "$EVENTS"
assert_not_contains "$CASE_TOKEN" "$CASE_OUTPUT"
ok "token uses a private header file without argv, env, or output leakage"

setup_case "token-newline"
CASE_TOKEN=$'github_pat_bad\nInjected: header'
run_case
assert_failure "token with newline"
[ ! -s "$EVENTS" ] || fail "invalid token reached mktemp or network"
assert_not_contains "$CASE_TOKEN" "$CASE_OUTPUT"
ok "token header injection fails before side effects"

setup_case "rollback"
mkdir -p "$INSTALL_ROOT_VALUE/releases/old"
ln -s "$INSTALL_ROOT_VALUE/releases/old" "$INSTALL_ROOT_VALUE/current"
FAKE_RESTART_FAIL=1
run_case
assert_failure "restart failure rollback"
assert_count "systemctl|restart crawl-stars-server" "$EVENTS" 2
rollback_target="$(readlink "$INSTALL_ROOT_VALUE/current")"
expected_rollback_target="$(readlink -f "$INSTALL_ROOT_VALUE/releases/old")"
[ "$rollback_target" = "$expected_rollback_target" ] ||
  fail "restart failure did not restore previous current symlink: got $rollback_target"
ok "restart failure preserves rollback behavior"

setup_case "rollback-smoke"
mkdir -p "$INSTALL_ROOT_VALUE/releases/old"
ln -s "$INSTALL_ROOT_VALUE/releases/old" "$INSTALL_ROOT_VALUE/current"
FAKE_SMOKE_FAIL=1
run_case
assert_failure "smoke failure rollback"
assert_count "systemctl|restart crawl-stars-server" "$EVENTS" 2
rollback_target="$(readlink "$INSTALL_ROOT_VALUE/current")"
expected_rollback_target="$(readlink -f "$INSTALL_ROOT_VALUE/releases/old")"
[ "$rollback_target" = "$expected_rollback_target" ] ||
  fail "smoke failure did not restore previous current symlink: got $rollback_target"
ok "smoke failure preserves rollback behavior"

echo "1..$TESTS"
