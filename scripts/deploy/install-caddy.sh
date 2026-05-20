#!/usr/bin/env bash
set -euo pipefail

CADDYFILE_SOURCE="${CADDYFILE_SOURCE:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/Caddyfile}"
CADDYFILE_TARGET="${CADDYFILE_TARGET:-/etc/caddy/Caddyfile}"
SKIP_CADDY_INSTALL="${SKIP_CADDY_INSTALL:-0}"

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  SUDO="${SUDO:-sudo}"
fi

if [ ! -f "${CADDYFILE_SOURCE}" ]; then
  echo "Caddyfile template not found: ${CADDYFILE_SOURCE}" >&2
  exit 1
fi

install_caddy_debian() {
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "Caddy is not installed and apt-get was not found. Install Caddy 2 first, then rerun with SKIP_CADDY_INSTALL=1." >&2
    exit 1
  fi

  ${SUDO} apt-get update
  ${SUDO} apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | ${SUDO} gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    | ${SUDO} tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
  ${SUDO} apt-get update
  ${SUDO} apt-get install -y caddy
}

if ! command -v caddy >/dev/null 2>&1; then
  if [ "${SKIP_CADDY_INSTALL}" = "1" ]; then
    echo "Caddy is not installed and SKIP_CADDY_INSTALL=1 was set." >&2
    exit 1
  fi
  install_caddy_debian
fi

${SUDO} mkdir -p "$(dirname "${CADDYFILE_TARGET}")"
if [ -f "${CADDYFILE_TARGET}" ] && ! cmp -s "${CADDYFILE_SOURCE}" "${CADDYFILE_TARGET}"; then
  backup_path="${CADDYFILE_TARGET}.bak.$(date -u +%Y%m%d%H%M%S)"
  ${SUDO} cp -a "${CADDYFILE_TARGET}" "${backup_path}"
  echo "Backed up existing Caddyfile to ${backup_path}"
fi

${SUDO} install -m 0644 "${CADDYFILE_SOURCE}" "${CADDYFILE_TARGET}"
${SUDO} caddy fmt --overwrite "${CADDYFILE_TARGET}"
${SUDO} caddy validate --config "${CADDYFILE_TARGET}"
${SUDO} systemctl enable caddy

if ${SUDO} systemctl is-active --quiet caddy; then
  ${SUDO} systemctl reload caddy
else
  ${SUDO} systemctl restart caddy
fi

echo "Caddy configured with ${CADDYFILE_TARGET}"
