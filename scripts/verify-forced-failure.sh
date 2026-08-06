#!/usr/bin/env bash
# 发布后强制失败验证（v0.17 roadmap P1.3 / v0.11.0 roadmap §9.2）
#
# 对一个普通渠道 + 一个订阅账号分别强制失败，确认三项结论：
#   1. dashboard/metrics 显示正确回退原因（fallback 计数增长 + routing-ops 回退率）；
#   2. 最终 credential/usage/成本/账单只归属实际服务来源（账本 attribution）；
#   3. 账单只落一次（每个 (reference_id, cost_source) 恰好一行，dedupe key 无重复）。
#
# 场景一：禁用普通渠道 → 订阅账号实际服务（serving-kind=subscription）
# 场景二：禁用订阅账号 → 普通渠道实际服务（serving-kind=channel）
#
# 前置条件：
#   - 已部署的验证环境（本地 docker-compose test stack 或 staging），普通渠道与
#     订阅账号均已配置且可互相回退；
#   - 环境中流量安静（该用户 5 分钟内无其他请求），否则 -user/-after-id 关联会串。
#
# 环境变量（自动从仓库根 .env 读取 ADMIN_TOKEN / DATABASE_DSN）：
#   ADMIN_BASE     管理端 base（默认 http://127.0.0.1:3000）
#   RELAY_BASE     relay-gateway base（默认 http://127.0.0.1:8080）
#   ADMIN_TOKEN    管理端 Bearer token
#   API_TOKEN      用于发测试请求的用户 API token
#   TEST_MODEL     测试模型（默认 gpt-3.5-turbo）
#   TEST_USER_ID   API_TOKEN 对应的 user_id（用于 DB 关联）
#   CHANNEL_ID     普通渠道 id（将被强制失败）
#   SUB_ACCOUNT_ID 订阅账号 id（将被强制失败）
#   RECONCILE_DSN / DATABASE_DSN  计费库 MySQL DSN
#   DISABLE_CHANNEL_CMD / ENABLE_CHANNEL_CMD   覆盖渠道禁用/启用命令（默认走 admin API）
#   DISABLE_ACCOUNT_CMD / ENABLE_ACCOUNT_CMD   覆盖账号禁用/启用命令
#
# 退出码：0 全部通过；1 任一项断言失败；2 配置/运行错误
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[verify-failure]${NC} $*"; }
warn() { echo -e "${YELLOW}[verify-failure][warn]${NC} $*"; }
err()  { echo -e "${RED}[verify-failure][error]${NC} $*"; }

load_env_var() {
  local key="$1"
  local value
  value=$(grep -E "^${key}=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true)
  if [ -n "$value" ] && [ -z "${!key:-}" ]; then
    export "$key=$value"
  fi
}

if [ -f "$ENV_FILE" ]; then
  load_env_var "ADMIN_TOKEN"
  load_env_var "DATABASE_DSN"
fi

ADMIN_BASE="${ADMIN_BASE:-http://127.0.0.1:3000}"
RELAY_BASE="${RELAY_BASE:-http://127.0.0.1:8080}"
TEST_MODEL="${TEST_MODEL:-gpt-3.5-turbo}"
RECONCILE_DSN="${RECONCILE_DSN:-${DATABASE_DSN:-}}"

FAILED=false
check() { # check <desc> <cmd...>
  local desc="$1"
  shift
  if "$@"; then
    log "PASS: $desc"
  else
    err "FAIL: $desc"
    FAILED=true
  fi
}

preflight() {
  for v in ADMIN_TOKEN API_TOKEN TEST_USER_ID CHANNEL_ID SUB_ACCOUNT_ID RECONCILE_DSN; do
    if [ -z "${!v:-}" ]; then
      err "$v is required"
      exit 2
    fi
  done
  curl -fsS "$RELAY_BASE/healthz" >/dev/null || { err "relay $RELAY_BASE unreachable"; exit 2; }
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$ADMIN_BASE/api/status" >/dev/null || { err "admin $ADMIN_BASE unreachable"; exit 2; }
}

fallback_total() {
  curl -fsS "$RELAY_BASE/metrics" | awk '/^micro_one_api_routing_fallback_total\{/ { s += $NF } END { print s+0 }'
}

send_chat() {
  local marker="$1"
  curl -fsS -X POST "$RELAY_BASE/v1/chat/completions" \
    -H "Authorization: Bearer $API_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$TEST_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"forced-failure-verify $marker $RANDOM\"}]}" >/dev/null
}

max_reservation_id() {
  go run ./scripts/verify/forced_failure_checks.go -dsn "$RECONCILE_DSN" -max-reservation-id -user "$TEST_USER_ID"
}

routing_ops_fallback() {
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$ADMIN_BASE/api/admin/routing-ops" | python3 -c '
import json, sys
d = json.load(sys.stdin)
rates = d.get("rates") or {}
print(rates.get("fallback_total", 0))
'
}

# run_scenario <name> <disable_cmd...> <enable_cmd...> <serving-kind>
run_scenario() {
  local name="$1"
  local serving_kind="$2"
  local disable_cmd="$3"
  local enable_cmd="$4"

  log "── scenario: $name（期望实际服务来源=$serving_kind）"

  local before_fb after_fb before_id reservation
  before_fb=$(fallback_total)
  before_id=$(max_reservation_id)

  if ! eval "$disable_cmd"; then
    err "failed to force failure: $disable_cmd"
    eval "$enable_cmd" >/dev/null 2>&1 || true
    FAILED=true
    return
  fi

  # 等待健康/选路看到禁用状态
  sleep 3

  if ! send_chat "$name"; then
    err "$name: fallback request failed — no candidate served (check routing config)"
    FAILED=true
    eval "$enable_cmd" >/dev/null 2>&1 || true
    return
  fi

  sleep 3
  after_fb=$(fallback_total)
  check "$name: relay fallback counter increased (before=$before_fb after=$after_fb)" \
    bash -c "[ $after_fb -gt $before_fb ]"

  local ro_fb
  ro_fb=$(routing_ops_fallback)
  check "$name: routing-ops shows fallback_total > 0 (got $ro_fb)" \
    bash -c "[ $ro_fb -gt 0 ]"

  local rc=0
  set +e
  reservation=$(go run ./scripts/verify/forced_failure_checks.go -dsn "$RECONCILE_DSN" \
    -user "$TEST_USER_ID" -after-reservation-id "$before_id" -serving-kind "$serving_kind")
  rc=$?
  set -e
  if [ "$rc" -eq 0 ]; then
    log "PASS: $name: ledger attribution + single billing (reservation $reservation)"
  else
    err "FAIL: $name: ledger attribution / single-billing assertion failed"
    FAILED=true
  fi

  eval "$enable_cmd" >/dev/null 2>&1 || warn "$name: re-enable command failed: $enable_cmd"
}

DISABLE_CHANNEL_CMD="${DISABLE_CHANNEL_CMD:-curl -fsS -X POST -H \"Authorization: Bearer $ADMIN_TOKEN\" \"$ADMIN_BASE/api/channel/disable/$CHANNEL_ID\" >/dev/null}"
ENABLE_CHANNEL_CMD="${ENABLE_CHANNEL_CMD:-curl -fsS -X POST -H \"Authorization: Bearer $ADMIN_TOKEN\" \"$ADMIN_BASE/api/channel/enable/$CHANNEL_ID\" >/dev/null}"
DISABLE_ACCOUNT_CMD="${DISABLE_ACCOUNT_CMD:-curl -fsS -X PUT -H \"Authorization: Bearer $ADMIN_TOKEN\" -H 'Content-Type: application/json' -d '{\"status\":0}' \"$ADMIN_BASE/v1/subscription-accounts/$SUB_ACCOUNT_ID/status\" >/dev/null}"
ENABLE_ACCOUNT_CMD="${ENABLE_ACCOUNT_CMD:-curl -fsS -X PUT -H \"Authorization: Bearer $ADMIN_TOKEN\" -H 'Content-Type: application/json' -d '{\"status\":1}' \"$ADMIN_BASE/v1/subscription-accounts/$SUB_ACCOUNT_ID/status\" >/dev/null}"

preflight

# 场景一：禁用普通渠道 → 订阅账号实际服务
run_scenario "channel-fails-subscription-serves" "subscription" "$DISABLE_CHANNEL_CMD" "$ENABLE_CHANNEL_CMD"

# 场景二：禁用订阅账号 → 普通渠道实际服务
run_scenario "subscription-fails-channel-serves" "channel" "$DISABLE_ACCOUNT_CMD" "$ENABLE_ACCOUNT_CMD"

if [ "$FAILED" = true ]; then
  err "forced-failure verification FAILED — fix before declaring the release clean"
  exit 1
fi
log "forced-failure verification PASSED — 结论回写发布说明（§9.2）"
exit 0
