#!/usr/bin/env bash
# Read-only database preflight for upgrading New API rc.24 -> upstream/main.
# Default: print SQL only. Never DROP/TRUNCATE. Never print SQL_DSN.
#
#   scripts/preflight-db.sh
#   SQL_DSN='postgresql://...' scripts/preflight-db.sh --read-only
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EXECUTE=0
if [[ "${1:-}" == "--read-only" ]]; then
  EXECUTE=1
fi

cat <<'SQL'
-- 鑫元宝 New API 升级预检（只读）。在生产执行前先 pg_dump。
-- 1) 额度列必须已是 bigint，否则官方 rc.36+ 会拒绝启动。
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = 'users'
  AND column_name IN ('quota', 'used_quota', 'aff_quota', 'aff_history');

-- 2) tokens.key 重复会使 migrateTokenKeyUniqueness 失败。
SELECT key, COUNT(*) AS n
FROM tokens
GROUP BY key
HAVING COUNT(*) > 1;

-- 3) options 主键 / 重复 key（上游 4fc9d1f1f 会重建主键；重复行要心里有数）。
SELECT kcu.column_name, tc.constraint_type
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema = kcu.table_schema
WHERE tc.table_schema = 'public'
  AND tc.table_name = 'options'
  AND tc.constraint_type IN ('PRIMARY KEY', 'UNIQUE');

SELECT key, COUNT(*) AS n
FROM options
GROUP BY key
HAVING COUNT(*) > 1;

-- 4) 站点配置（不要把支付密钥贴到聊天）。
SELECT key, left(value, 200) AS value_head
FROM options
WHERE key IN (
  'SystemName', 'ServerAddress', 'Logo', 'TaskPluginEnabled'
);

-- 5) 豆包视频渠道（type=54）必须还在。
SELECT id, name, type, status
FROM channels
WHERE type = 54;

-- 6) 用户/令牌数量，升级后对照。
SELECT
  (SELECT COUNT(*) FROM users) AS users,
  (SELECT COUNT(*) FROM tokens) AS tokens;
SQL

if [[ "$EXECUTE" -eq 0 ]]; then
  echo
  echo "Printed SQL only. To run against a database:"
  echo "  SQL_DSN='postgresql://USER:PASS@HOST:5432/newapi' $0 --read-only"
  echo "Do not put the DSN into git or chat logs."
  exit 0
fi

if [[ -z "${SQL_DSN:-}" ]]; then
  echo "ERROR: SQL_DSN is empty; refusing to guess from docker-compose.yml" >&2
  exit 1
fi
command -v psql >/dev/null || { echo "ERROR: psql not found" >&2; exit 1; }

psql "$SQL_DSN" -v ON_ERROR_STOP=1 <<'SQL'
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = 'users'
  AND column_name IN ('quota', 'used_quota', 'aff_quota', 'aff_history');

SELECT key, COUNT(*) AS n
FROM tokens
GROUP BY key
HAVING COUNT(*) > 1;

SELECT kcu.column_name, tc.constraint_type
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema = kcu.table_schema
WHERE tc.table_schema = 'public'
  AND tc.table_name = 'options'
  AND tc.constraint_type IN ('PRIMARY KEY', 'UNIQUE');

SELECT key, COUNT(*) AS n
FROM options
GROUP BY key
HAVING COUNT(*) > 1;

SELECT key, left(value, 200) AS value_head
FROM options
WHERE key IN (
  'SystemName', 'ServerAddress', 'Logo', 'TaskPluginEnabled'
);

SELECT id, name, type, status
FROM channels
WHERE type = 54;

SELECT
  (SELECT COUNT(*) FROM users) AS users,
  (SELECT COUNT(*) FROM tokens) AS tokens;
SQL
