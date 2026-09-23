#!/usr/bin/env bash
# 端到端测试环境：全新 SQLite、假上游、后端。不碰生产库，也不碰仓库里的 one-api.db。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
E2E=/tmp/e2e
mkdir -p "$E2E/evidence" "$E2E/screenshots"

# 生产库连接串一律不许出现在这个环境里
unset SQL_DSN LOG_SQL_DSN REDIS_CONN_STRING

pkill -f "$E2E/new-api" 2>/dev/null || true
pkill -f "mock_upstream.py" 2>/dev/null || true
sleep 1
rm -f "$E2E"/e2e.db*

(cd "$ROOT" && go build -o "$E2E/new-api" .)

python3 "$ROOT/scripts/e2e/mock_upstream.py" 18080 > "$E2E/mock.log" 2>&1 &

cd "$E2E"   # 后端会在工作目录下写 logs/，别写进仓库
env PORT=3300 \
  SQLITE_PATH="$E2E/e2e.db" \
  SESSION_SECRET=e2e-session-secret \
  CRITICAL_RATE_LIMIT_ENABLE=false \
  GLOBAL_API_RATE_LIMIT_ENABLE=false \
  GLOBAL_WEB_RATE_LIMIT_ENABLE=false \
  SEARCH_RATE_LIMIT_ENABLE=false \
  NO_PROXY=127.0.0.1,localhost \
  KYC_ENCRYPT_KEY=00000000000000000000000000000000000000000000000000000000deadbeef \
  KYC_HASH_KEY=00000000000000000000000000000000000000000000000000000000cafebabe \
  "$E2E/new-api" > "$E2E/backend.log" 2>&1 &

for i in $(seq 1 60); do
  if curl -sf -m 2 http://127.0.0.1:3300/api/status >/dev/null; then
    echo "backend up"; exit 0
  fi
  sleep 1
done
echo "backend failed to start"; tail -30 "$E2E/backend.log"; exit 1
