#!/usr/bin/env bash
set -euo pipefail

# Never let a caller's xtrace mode print credentials expanded by this script.
case "$-" in
  *x*) set +x ;;
esac

REPO="${REPO:-Second-Loop/Server-CrawlStars}"
SERVICE_NAME="${SERVICE_NAME:-crawl-stars-server}"
RUN_USER="${RUN_USER:-crawlstars}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/crawl-stars-server}"
ASSET_NAME="${ASSET_NAME-crawl-stars-server-linux-amd64.tar.gz}"
RELEASE_TAG="${RELEASE_TAG-latest}"
SMOKE_URL="${SMOKE_URL:-http://127.0.0.1:8080/health}"

fail() {
  echo "$*" >&2
  exit 1
}

if [ -z "${ASSET_NAME}" ] ||
  [ "${ASSET_NAME}" = "." ] ||
  [ "${ASSET_NAME}" = ".." ] ||
  [ "${ASSET_NAME}" = "SHA256SUMS" ] ||
  [ "${#ASSET_NAME}" -gt 255 ] ||
  [[ ! "${ASSET_NAME}" =~ ^[A-Za-z0-9._-]+$ ]]; then
  fail "ASSET_NAME must be a safe basename using only A-Z, a-z, 0-9, dot, underscore, or hyphen"
fi
if [ -z "${RELEASE_TAG}" ]; then
  fail "RELEASE_TAG must not be empty"
fi

token="${GH_TOKEN:-}"
if [[ "${token}" == *$'\r'* || "${token}" == *$'\n'* ]]; then
  fail "GH_TOKEN must not contain line breaks"
fi

for dependency in curl jq sha256sum tar mktemp; do
  command -v "${dependency}" >/dev/null 2>&1 || fail "${dependency} is required"
done

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  SUDO="${SUDO:-sudo}"
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

curl_args=(--fail --silent --show-error --location)
if [ -n "${token}" ]; then
  auth_header="${tmpdir}/github-auth-header"
  umask 077
  printf 'Authorization: Bearer %s\n' "${token}" > "${auth_header}"
  chmod 0600 "${auth_header}"
  curl_args+=(--header "@${auth_header}")
fi
unset GH_TOKEN token

resolved_tag="${RELEASE_TAG}"
if [ "${RELEASE_TAG}" = "latest" ]; then
  metadata_path="${tmpdir}/latest-release.json"
  tag_path="${tmpdir}/resolved-tag"
  curl "${curl_args[@]}" \
    --header "Accept: application/vnd.github+json" \
    --output "${metadata_path}" \
    "https://api.github.com/repos/${REPO}/releases/latest"
  if ! jq -er '.tag_name | select(type == "string" and length > 0)' "${metadata_path}" > "${tag_path}"; then
    fail "GitHub latest release response does not contain a valid tag_name"
  fi
  IFS= read -r resolved_tag < "${tag_path}" || true
  if [ -z "${resolved_tag}" ] || [ "${resolved_tag}" = "latest" ]; then
    fail "GitHub latest release must resolve to a non-latest immutable tag"
  fi
fi

encoded_tag_path="${tmpdir}/encoded-tag"
if ! jq -nr --arg tag "${resolved_tag}" '$tag | @uri' > "${encoded_tag_path}"; then
  fail "failed to URL-encode release tag"
fi
IFS= read -r encoded_tag < "${encoded_tag_path}" || true
if [ -z "${encoded_tag}" ]; then
  fail "release tag encoded to an empty value"
fi

release_url="https://github.com/${REPO}/releases/download/${encoded_tag}"
package_name="release-package.tar.gz"
manifest_name="release-SHA256SUMS"
check_name="SHA256SUMS.check"
package_path="${tmpdir}/${package_name}"
manifest_path="${tmpdir}/${manifest_name}"
check_path="${tmpdir}/${check_name}"
extract_dir="${tmpdir}/extract"
mkdir -p "${extract_dir}"

echo "Downloading ${ASSET_NAME} from release ${resolved_tag}"
curl "${curl_args[@]}" --output "${package_path}" "${release_url}/${ASSET_NAME}"
curl "${curl_args[@]}" --output "${manifest_path}" "${release_url}/SHA256SUMS"

manifest_lines="$(awk 'END { print NR }' "${manifest_path}")"
if [ "${manifest_lines}" -ne 1 ]; then
  fail "SHA256SUMS must contain exactly one record"
fi
manifest_line=""
IFS= read -r manifest_line < "${manifest_path}" || true
if [ "${#manifest_line}" -lt 67 ]; then
  fail "SHA256SUMS record is malformed"
fi
checksum="${manifest_line:0:64}"
marker="${manifest_line:64:2}"
manifest_asset="${manifest_line:66}"
if [[ ! "${checksum}" =~ ^[[:xdigit:]]{64}$ ]]; then
  fail "SHA256SUMS checksum must contain exactly 64 hexadecimal characters"
fi
if [ "${marker}" != "  " ] && [ "${marker}" != " *" ]; then
  fail "SHA256SUMS must use a GNU text or binary marker"
fi
if [ "${manifest_asset}" != "${ASSET_NAME}" ]; then
  fail "SHA256SUMS does not contain the requested asset filename"
fi
printf '%s%s%s\n' "${checksum}" "${marker}" "${package_name}" > "${check_path}"
(
  cd "${tmpdir}"
  sha256sum --strict -c "${check_name}"
)

tar -xzf "${package_path}" -C "${extract_dir}"

if [ ! -x "${extract_dir}/crawl-stars-server" ]; then
  echo "Package does not contain an executable crawl-stars-server binary" >&2
  exit 1
fi

version=""
if [ -f "${extract_dir}/VERSION" ]; then
  version="$(tr -cd '[:alnum:]._-' < "${extract_dir}/VERSION" | cut -c1-64)"
fi
if [ -z "${version}" ]; then
  version="$(date -u +%Y%m%d%H%M%S)"
fi

release_dir="${INSTALL_ROOT}/releases/${version}"
current_target=""
if [ -L "${INSTALL_ROOT}/current" ]; then
  current_target="$(readlink -f "${INSTALL_ROOT}/current")"
fi

${SUDO} mkdir -p "${release_dir}"
${SUDO} cp -R "${extract_dir}/." "${release_dir}/"
${SUDO} chown -R "${RUN_USER}:${RUN_USER}" "${release_dir}"
${SUDO} chmod 0755 "${release_dir}" "${release_dir}/crawl-stars-server"

if [ -n "${current_target}" ]; then
  ${SUDO} ln -sfn "${current_target}" "${INSTALL_ROOT}/previous"
fi
${SUDO} ln -sfn "${release_dir}" "${INSTALL_ROOT}/current"

set +e
${SUDO} systemctl restart "${SERVICE_NAME}"
restart_status=$?
sleep 1
curl -fsS "${SMOKE_URL}" >/dev/null 2>&1
smoke_status=$?
set -e

if [ "${restart_status}" -ne 0 ] || [ "${smoke_status}" -ne 0 ]; then
  echo "Deployment failed restart or smoke check. Restoring previous release if available." >&2
  if [ -n "${current_target}" ]; then
    ${SUDO} ln -sfn "${current_target}" "${INSTALL_ROOT}/current"
    ${SUDO} systemctl restart "${SERVICE_NAME}" || true
  fi
  exit 1
fi

echo "Deployed ${version} to ${release_dir}"
echo "Smoke check passed: ${SMOKE_URL}"
