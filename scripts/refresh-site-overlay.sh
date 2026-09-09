#!/usr/bin/env bash
# Copy current customized files back into site-overlay/files after a successful rebase.
# Does not refresh backend .diff patches; export those with git diff after you finish Go edits.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/common.sh
source "$ROOT/scripts/common.sh"
cd "$ROOT"
MANIFEST="$ROOT/site-overlay/manifest.json"
[[ -f "$MANIFEST" ]] || xyb_die "missing $MANIFEST"

python3 - "$MANIFEST" "$ROOT" <<'PY'
import json, os, shutil, sys
manifest = json.load(open(sys.argv[1], encoding="utf-8"))
root = sys.argv[2]
keys = ["exactFiles", "productUiFiles"]
missing = []
copied = 0
for key in keys:
    for rel in manifest.get(key, []):
        src = os.path.join(root, rel)
        dst = os.path.join(root, "site-overlay", "files", rel)
        if not os.path.isfile(src):
            missing.append(rel)
            continue
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copy2(src, dst)
        copied += 1
        print(f"refresh {rel}")
if missing:
    print("ERROR missing working-tree files:", file=sys.stderr)
    for rel in missing:
        print(f"  {rel}", file=sys.stderr)
    raise SystemExit(1)
print(f"refreshed {copied} overlay files")
print("Backend patches were NOT rewritten. After Go customizations compile, run:")
print("  git diff upstream/main -- <paths> > site-overlay/patches/00x-....diff")
PY
