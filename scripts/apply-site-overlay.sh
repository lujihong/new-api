#!/usr/bin/env bash
# Apply 鑫元宝 site overlay onto the current worktree.
# Does not touch docker-compose.yml, .env, data/, logs/, backups/, or SQL.
#
# Usage:
#   scripts/apply-site-overlay.sh --mode=branding
#   scripts/apply-site-overlay.sh --mode=full
#   scripts/apply-site-overlay.sh --mode=full --dry-run
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/common.sh
source "$ROOT/scripts/common.sh"
cd "$ROOT"

MODE="branding"
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --mode=branding|--mode=full) MODE="${arg#--mode=}" ;;
    --dry-run) DRY_RUN=1 ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    *) xyb_die "unknown argument: $arg" ;;
  esac
done

MANIFEST="$ROOT/site-overlay/manifest.json"
[[ -f "$MANIFEST" ]] || xyb_die "missing $MANIFEST"
command -v python3 >/dev/null || xyb_die "python3 is required"
command -v git >/dev/null || xyb_die "git is required"

copy_list() {
  python3 - "$MANIFEST" "$1" <<'PY'
import json, sys
manifest = json.load(open(sys.argv[1], encoding="utf-8"))
key = sys.argv[2]
for path in manifest.get(key, []):
    print(path)
PY
}

copy_one() {
  local rel="$1"
  xyb_is_protected "$rel" && xyb_die "refusing to overlay protected path: $rel"
  local src="$ROOT/site-overlay/files/$rel"
  local dst="$ROOT/$rel"
  [[ -f "$src" ]] || xyb_die "overlay source missing: site-overlay/files/$rel"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "DRY copy $rel"
    return
  fi
  mkdir -p "$(dirname "$dst")"
  cp -a "$src" "$dst"
  echo "copied $rel"
}

xyb_info "apply-site-overlay mode=$MODE dry_run=$DRY_RUN"

while IFS= read -r rel; do
  [[ -z "$rel" ]] && continue
  copy_one "$rel"
done < <(copy_list exactFiles)

if [[ "$MODE" == "full" ]]; then
  while IFS= read -r rel; do
    [[ -z "$rel" ]] && continue
    copy_one "$rel"
  done < <(copy_list productUiFiles)
else
  xyb_info "skipping productUiFiles (pricing sheet / task log columns). Use --mode=full to include."
fi

python3 - "$MANIFEST" "$ROOT" "$DRY_RUN" <<'PY'
import json, os, subprocess, sys
manifest = json.load(open(sys.argv[1], encoding="utf-8"))
root = sys.argv[2]
dry = sys.argv[3] == "1"
merger = os.path.join(root, "scripts", "merge_i18n.py")
for delta, target in manifest.get("i18nDelta", {}).items():
    delta_path = os.path.join(root, delta)
    target_path = os.path.join(root, target)
    print(f"i18n {delta} -> {target}")
    if dry:
        continue
    raise_code = subprocess.call(["python3", merger, delta_path, target_path])
    if raise_code != 0:
        raise SystemExit(raise_code)
PY

apply_patch() {
  local patch="$1"
  local abs="$ROOT/$patch"
  [[ -f "$abs" ]] || xyb_die "missing patch $patch"
  if git apply --check "$abs" >/dev/null 2>&1; then
    if [[ "$DRY_RUN" -eq 1 ]]; then
      echo "DRY apply $patch"
      return
    fi
    git apply "$abs"
    echo "applied $patch"
    return
  fi
  if git apply -R --check "$abs" >/dev/null 2>&1; then
    echo "already applied $patch (reverse-check passed), skip"
    return
  fi
  echo "ERROR: patch does not apply: $patch" >&2
  echo "This overlay was exported against $MODE base ${MODE:+}site-overlay/manifest.json patchBase." >&2
  echo "Do not git apply --reject and ship .rej files. Rebase the customization onto the new upstream file, then scripts/refresh-site-overlay.sh" >&2
  git apply --check "$abs" || true
  exit 1
}

if [[ "$MODE" == "full" ]]; then
  while IFS= read -r patch; do
    [[ -z "$patch" ]] && continue
    apply_patch "$patch"
  done < <(python3 - "$MANIFEST" <<'PY'
import json, sys
manifest = json.load(open(sys.argv[1], encoding="utf-8"))
for path in manifest.get("patches", []):
    print(path)
PY
)
else
  xyb_info "skipping backend patches in branding mode (video-proxy / topup / relay). Use --mode=full after rebase."
fi

xyb_info "overlay apply finished (mode=$MODE)"
