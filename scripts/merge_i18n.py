#!/usr/bin/env python3
"""Merge overlay i18n keys into a New API locale file without rewriting untouched keys.

Locale files are `{ "translation": { "Key": "value", ... } }`.
Existing keys are replaced in place so a 5000-line locale does not become a noisy dump.
Missing keys are inserted at the top of the translation object.
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: merge_i18n.py <delta.json> <locale.json>", file=sys.stderr)
        return 2
    delta_path = Path(sys.argv[1])
    target_path = Path(sys.argv[2])
    if not delta_path.is_file() or not target_path.is_file():
        print(f"missing {delta_path} or {target_path}", file=sys.stderr)
        return 1

    delta = json.loads(delta_path.read_text(encoding="utf-8"))
    if not isinstance(delta, dict) or not delta:
        print("delta must be a non-empty JSON object", file=sys.stderr)
        return 1

    text = target_path.read_text(encoding="utf-8")
    updated = 0
    inserted = 0
    insert_blob_parts: list[str] = []

    for key, value in delta.items():
        key_json = json.dumps(key, ensure_ascii=False)
        value_json = json.dumps(value, ensure_ascii=False)
        pattern = re.compile(
            r"(" + re.escape(key_json) + r"\s*:\s*)(?:\"(?:\\.|[^\"\\])*\"|'(?:\\.|[^'\\])*')"
        )
        new_text, count = pattern.subn(r"\1" + value_json, text, count=1)
        if count == 1:
            text = new_text
            updated += 1
        elif count == 0:
            insert_blob_parts.append(f"    {key_json}: {value_json},")
            inserted += 1
        else:
            print(f"ambiguous key matches for {key_json} in {target_path}", file=sys.stderr)
            return 1

    if insert_blob_parts:
        blob = "\n".join(insert_blob_parts) + "\n"
        marker = '"translation": {'
        idx = text.find(marker)
        if idx == -1:
            print(f"cannot insert keys; no translation object in {target_path}", file=sys.stderr)
            return 1
        insert_at = idx + len(marker)
        text = text[:insert_at] + "\n" + blob + text[insert_at:]

    target_path.write_text(text, encoding="utf-8")
    print(f"i18n {target_path}: updated={updated} inserted={inserted}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
