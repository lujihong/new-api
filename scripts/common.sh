#!/usr/bin/env bash
# Shared helpers for site overlay / upgrade scripts.
set -euo pipefail

xyb_root() {
  local here
  here="$(cd "$(dirname "${BASH_SOURCE[1]}")" && pwd)"
  cd "$here/.." && pwd
}

xyb_is_protected() {
  local rel="$1"
  case "$rel" in
    docker-compose.yml|docker-compose.override.yml|.env|VERSION)
      return 0
      ;;
    data|data/*|logs|logs/*|backups|backups/*|site-overlay|site-overlay/*|web/pnpm-lock.yaml)
      return 0
      ;;
  esac
  return 1
}

xyb_die() {
  echo "ERROR: $*" >&2
  exit 1
}

xyb_info() {
  echo "==> $*"
}
