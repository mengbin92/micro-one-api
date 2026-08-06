#!/usr/bin/env bash
# 周期对账一键执行脚本（v0.17 roadmap P1.2）
#
# 两步完成一次周期对账，输出差异报告；无差异时 exit 0：
#   1. 触发 billing-service 全量对账（POST /v1/reconciliation，Bearer SERVICE_TOKEN）
#      并输出 JSON 差异报告（account/channel/log/subscription/receivable/refund/stuck）。
#   2. 执行 DB 侧检查（scripts/reconcile/checks.go）：ledger dedupe key、
#      cache-creation counted-but-unbilled、缓存命中率口径、毛利、可选供应商账单对比。
#
# 用法:
#   ./scripts/reconcile/reconcile.sh
#   ./scripts/reconcile/reconcile.sh --vendor-bill vendor_bill.csv --since 7d
#   ./scripts/reconcile/reconcile.sh --skip-trigger          # 只跑 DB 检查
#   ./scripts/reconcile/reconcile.sh --skip-db-checks        # 只触发 billing 对账
#
# 环境变量（自动从仓库根 .env 读取）:
#   SERVICE_TOKEN            触发 billing 对账的 token
#   BILLING_RECON_ENDPOINT   默认 http://127.0.0.1:8004/v1/reconciliation
#   RECONCILE_DSN / DATABASE_DSN  计费库 DSN（MySQL）
#
# 退出码: 0 无差异; 1 有差异; 2 配置/运行错误
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[reconcile]${NC} $*"; }
warn() { echo -e "${YELLOW}[reconcile][warn]${NC} $*"; }
err()  { echo -e "${RED}[reconcile][error]${NC} $*"; }

SKIP_TRIGGER=false
SKIP_DB=false
VENDOR_BILL=""
SINCE="24h"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-trigger) SKIP_TRIGGER=true; shift ;;
    --skip-db-checks) SKIP_DB=true; shift ;;
    --vendor-bill) VENDOR_BILL="${2:-}"; shift 2 ;;
    --since) SINCE="${2:-}"; shift 2 ;;
    -h|--help)
      sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      err "unknown argument: $1"
      exit 2
      ;;
  esac
done

load_env_var() {
  local key="$1"
  local value
  value=$(grep -E "^${key}=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true)
  if [ -n "$value" ] && [ -z "${!key:-}" ]; then
    export "$key=$value"
  fi
}

if [ -f "$ENV_FILE" ]; then
  load_env_var "SERVICE_TOKEN"
  load_env_var "DATABASE_DSN"
  load_env_var "BILLING_RECON_ENDPOINT"
fi

BILLING_RECON_ENDPOINT="${BILLING_RECON_ENDPOINT:-http://127.0.0.1:8004/v1/reconciliation}"
RECONCILE_DSN="${RECONCILE_DSN:-${DATABASE_DSN:-}}"

has_failures=false

trigger_reconciliation() {
  if [ "$SKIP_TRIGGER" = true ]; then
    log "skipping billing reconciliation trigger (--skip-trigger)"
    return
  fi
  if [ -z "${SERVICE_TOKEN:-}" ]; then
    err "SERVICE_TOKEN is required to trigger POST $BILLING_RECON_ENDPOINT"
    has_failures=true
    return
  fi
  log "triggering billing reconciliation: POST $BILLING_RECON_ENDPOINT"
  local tmp
  tmp=$(mktemp)
  if ! curl -fsS --max-time 120 -X POST \
      -H "Authorization: Bearer $SERVICE_TOKEN" \
      "$BILLING_RECON_ENDPOINT" > "$tmp"; then
    err "billing reconciliation request failed (see $tmp)"
    has_failures=true
    return
  fi

  local rc=0
  set +e
  python3 - "$tmp" <<'PY'
import json, sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    data = json.load(f)

keys = [
    "account_inconsistencies",
    "channel_inconsistencies",
    "log_inconsistencies",
    "subscription_inconsistencies",
    "receivable_inconsistencies",
    "refund_inconsistencies",
    "stuck_issuance_inconsistencies",
]
total = 0
print("== billing-service reconciliation result ==")
print(f"  run_at:         {data.get('run_at', 'n/a')}")
print(f"  expired_cleaned:{data.get('expired_cleaned', 0)}")
print(f"  accounts:       {data.get('total_accounts', 0)}")
print(f"  channels:       {data.get('total_channels', 0)}")
print(f"  subscriptions:  {data.get('total_subscriptions', 0)}")
for k in keys:
    n = len(data.get(k) or [])
    total += n
    if n:
        print(f"  {k}: {n}  <-- discrepancy")
        for item in (data.get(k) or [])[:5]:
            print(f"      {json.dumps(item, ensure_ascii=False)[:200]}")
if total:
    print(f"  RESULT: FAIL ({total} discrepancies)")
    sys.exit(1)
print("  RESULT: PASS (no discrepancies)")
sys.exit(0)
PY
  rc=$?
  set -e
  rm -f "$tmp"
  if [ "$rc" -ne 0 ]; then
    has_failures=true
  fi
}

run_db_checks() {
  if [ "$SKIP_DB" = true ]; then
    log "skipping DB checks (--skip-db-checks)"
    return
  fi
  if [ -z "$RECONCILE_DSN" ]; then
    err "no database DSN: set RECONCILE_DSN or DATABASE_DSN (or --skip-db-checks)"
    has_failures=true
    return
  fi
  log "running DB checks (window: $SINCE)"
  local args=(-dsn "$RECONCILE_DSN" -since "$SINCE")
  if [ -n "$VENDOR_BILL" ]; then
    args+=(-vendor-bill "$VENDOR_BILL")
  fi
  if ! go run ./scripts/reconcile/checks.go "${args[@]}"; then
    has_failures=true
  fi
}

trigger_reconciliation
run_db_checks

if [ "$has_failures" = true ]; then
  err "reconciliation finished with discrepancies — investigate before declaring the period clean"
  exit 1
fi
log "reconciliation finished clean: no discrepancies"
exit 0
