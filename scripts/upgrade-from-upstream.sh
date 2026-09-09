#!/usr/bin/env bash
# Merge official New API into this worktree, keep production compose, apply site overlay.
# Does NOT: touch SQL, docker compose up, git push, overwrite docker-compose.yml from upstream.
#
# Usage:
#   scripts/upgrade-from-upstream.sh
#   scripts/upgrade-from-upstream.sh --target=upstream/main --mode=branding
#   scripts/upgrade-from-upstream.sh --mode=full
#   scripts/upgrade-from-upstream.sh --skip-fetch --mode=branding
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/common.sh
source "$ROOT/scripts/common.sh"
cd "$ROOT"

TARGET="upstream/main"
MODE="branding"
SKIP_FETCH=0
for arg in "$@"; do
  case "$arg" in
    --target=*) TARGET="${arg#--target=}" ;;
    --mode=branding|--mode=full) MODE="${arg#--mode=}" ;;
    --skip-fetch) SKIP_FETCH=1 ;;
    -h|--help)
      sed -n '2,16p' "$0"
      exit 0
      ;;
    *) xyb_die "unknown argument: $arg" ;;
  esac
done

command -v git >/dev/null || xyb_die "git is required"
git rev-parse --is-inside-work-tree >/dev/null || xyb_die "not a git repository"
git remote get-url upstream >/dev/null 2>&1 || xyb_die "git remote 'upstream' missing (expect https://github.com/QuantumNous/new-api.git)"

# Allow dirty compose / overlay / docs / VERSION; refuse other tracked dirt.
while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  case "$path" in
    docker-compose.yml|docker-compose.site-build.yml|VERSION|Dockerfile|Dockerfile.dev|.gitignore|Makefile|makefile)
      continue
      ;;
    升级改动必看.md|交接给部署AI.md)
      continue
      ;;
    site-overlay/*|scripts/*|web/src/i18n/locales/*)
      continue
      ;;
    *) xyb_die "worktree has unexpected tracked changes: $path  (commit or stash them first)" ;;
  esac
done < <(git status --porcelain --untracked-files=no | cut -c4-)

if [[ "$SKIP_FETCH" -eq 0 ]]; then
  xyb_info "git fetch upstream --tags"
  git fetch upstream --tags
fi

git rev-parse "$TARGET" >/dev/null 2>&1 || xyb_die "unknown revision $TARGET"

xyb_info "merging $TARGET (no-commit). Production compose will be kept from the pre-merge working copy, not from upstream."
compose_bak="$(mktemp)"
cp "$ROOT/docker-compose.yml" "$compose_bak"
trap 'rm -f "$compose_bak"' EXIT

set +e
git merge --no-commit --no-ff "$TARGET"
merge_status=$?
set -e

cp "$compose_bak" "$ROOT/docker-compose.yml"
xyb_info "restored docker-compose.yml from pre-merge working copy (upstream compose discarded)"
if git ls-files --error-unmatch docker-compose.yml >/dev/null 2>&1; then
  git add -- docker-compose.yml || true
fi

conflicts="$(git diff --name-only --diff-filter=U || true)"
if [[ "$merge_status" -ne 0 || -n "$conflicts" ]]; then
  echo "ERROR: merge reported conflicts or non-zero status." >&2
  echo "$conflicts" >&2
  echo "Keep docker-compose.yml as ours. Resolve other files, then:" >&2
  echo "  scripts/apply-site-overlay.sh --mode=$MODE" >&2
  echo "  scripts/write-version.sh" >&2
  echo "Do not git merge --abort if you already need the conflict list — inspect git status first." >&2
  exit 1
fi

"$ROOT/scripts/apply-site-overlay.sh" --mode="$MODE"
"$ROOT/scripts/write-version.sh"

xyb_info "code merge + overlay done. This script did NOT start Docker or migrate the database."
echo "Next: read 交接给部署AI.md  (dump, preflight, staging, then compose build)."
echo "VERSION=$(tr -d '\n' < "$ROOT/VERSION")"
