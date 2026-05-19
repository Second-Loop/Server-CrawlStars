#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-crawl-stars-server}"
RUN_USER="${RUN_USER:-crawlstars}"
INSTALL_ROOT="${INSTALL_ROOT:-/opt/crawl-stars-server}"
UNIT_SOURCE="${UNIT_SOURCE:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/crawl-stars-server.service}"
UNIT_TARGET="${UNIT_TARGET:-/etc/systemd/system/${SERVICE_NAME}.service}"

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  SUDO="${SUDO:-sudo}"
fi

if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemctl is required on the target VM" >&2
  exit 1
fi

if [ ! -f "${UNIT_SOURCE}" ]; then
  echo "systemd unit template not found: ${UNIT_SOURCE}" >&2
  exit 1
fi

if ! id "${RUN_USER}" >/dev/null 2>&1; then
  ${SUDO} useradd --system --home-dir "${INSTALL_ROOT}" --shell /usr/sbin/nologin "${RUN_USER}"
fi

${SUDO} mkdir -p "${INSTALL_ROOT}/releases"
${SUDO} chown -R "${RUN_USER}:${RUN_USER}" "${INSTALL_ROOT}"
${SUDO} chmod 0755 "${INSTALL_ROOT}" "${INSTALL_ROOT}/releases"

${SUDO} install -m 0644 "${UNIT_SOURCE}" "${UNIT_TARGET}"
${SUDO} systemctl daemon-reload
${SUDO} systemctl enable "${SERVICE_NAME}"

if [ -x "${INSTALL_ROOT}/current/crawl-stars-server" ]; then
  ${SUDO} systemctl restart "${SERVICE_NAME}"
else
  echo "Installed ${SERVICE_NAME}. Run scripts/deploy/pull-latest.sh before starting it."
fi
