#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-crawl-stars-server}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/crawl-stars-server}"
SMOKE_URL="${SMOKE_URL:-http://127.0.0.1:8080/health}"

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  SUDO="${SUDO:-sudo}"
fi

if [ ! -L "${INSTALL_ROOT}/previous" ]; then
  echo "No previous release symlink found at ${INSTALL_ROOT}/previous" >&2
  exit 1
fi

previous_target="$(readlink -f "${INSTALL_ROOT}/previous")"
current_target=""
if [ -L "${INSTALL_ROOT}/current" ]; then
  current_target="$(readlink -f "${INSTALL_ROOT}/current")"
fi

if [ ! -x "${previous_target}/crawl-stars-server" ]; then
  echo "Previous release is not executable: ${previous_target}" >&2
  exit 1
fi

${SUDO} ln -sfn "${previous_target}" "${INSTALL_ROOT}/current"
if [ -n "${current_target}" ]; then
  ${SUDO} ln -sfn "${current_target}" "${INSTALL_ROOT}/previous"
fi

${SUDO} systemctl restart "${SERVICE_NAME}"
sleep 1
curl -fsS "${SMOKE_URL}" >/dev/null

echo "Rolled back to ${previous_target}"
echo "Smoke check passed: ${SMOKE_URL}"
