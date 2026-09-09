#!/usr/bin/env bash
# Write VERSION from git describe. Empty VERSION makes the binary report v0.0.0.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
command -v git >/dev/null || { echo "git is required" >&2; exit 1; }
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "not a git repository" >&2
  exit 1
fi
version="$(git describe --tags --always)"
if [[ -z "$version" ]]; then
  echo "git describe returned empty VERSION" >&2
  exit 1
fi
printf '%s\n' "$version" > "$ROOT/VERSION"
echo "VERSION=$version"
