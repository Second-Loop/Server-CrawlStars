#!/usr/bin/env bash
set -euo pipefail

REPO="${REPO:-Second-Loop/Server-CrawlStars}"
SERVICE_NAME="${SERVICE_NAME:-crawl-stars-server}"
RUN_USER="${RUN_USER:-crawlstars}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/crawl-stars-server}"
ASSET_NAME="${ASSET_NAME:-crawl-stars-server-linux-amd64.tar.gz}"
RELEASE_TAG="${RELEASE_TAG:-latest}"
SMOKE_URL="${SMOKE_URL:-http://127.0.0.1:8080/health}"

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  SUDO="${SUDO:-sudo}"
fi

download_url() {
  if [ "${RELEASE_TAG}" = "latest" ]; then
    printf 'https://github.com/%s/releases/latest/download/%s\n' "${REPO}" "${ASSET_NAME}"
  else
    printf 'https://github.com/%s/releases/download/%s/%s\n' "${REPO}" "${RELEASE_TAG}" "${ASSET_NAME}"
  fi
}

curl_auth_args=()
if [ -n "${GH_TOKEN:-}" ]; then
  curl_auth_args=(-H "Authorization: Bearer ${GH_TOKEN}")
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

package_path="${tmpdir}/${ASSET_NAME}"
extract_dir="${tmpdir}/extract"
mkdir -p "${extract_dir}"

echo "Downloading $(download_url)"
curl -fL "${curl_auth_args[@]}" -o "${package_path}" "$(download_url)"
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
