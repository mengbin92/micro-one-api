# 发布后强制失败验证 Runbook（v0.11.0 §9.2 补完）

> 对应 `docs/design/v0.17-roadmap.md` §3 P1.3 与 `docs/design/v0.11.0-roadmap.md` §9.2
> 发布清单最后一项：**发布后强制一个普通渠道和一个订阅账号分别失败**。
> 目标：确认回退可观测、来源归属正确、账单只落一次。结论必须回写发布说明。

## 一、结论目标（三项验收）

1. **dashboard/metrics 显示正确回退原因**：relay `micro_one_api_routing_fallback_total{reason}`
   增长，routing-ops 视图 `fallback_rate > 0` 且 `partial=false`。
2. **最终 credential/usage/成本/账单只归属实际服务来源**：账本行只出现在实际服务来源的
   `channel_id` 或 `subscription_account_id` 维度上，被强制失败的来源无该请求的 consume 行。
3. **账单只落一次（无重复扣费）**：每个 `(reference_id, cost_source)` 恰好一行 consume
   ledger，`ledger_dedupe_key` 无重复；重试/回退不会重复扣费。

## 二、自动化脚本（推荐）

```bash
ADMIN_BASE=http://<admin>:3000 \
RELAY_BASE=http://<relay>:8080 \
ADMIN_TOKEN=<admin-token> \
API_TOKEN=<user-api-token> \
TEST_MODEL=gpt-3.5-turbo \
TEST_USER_ID=<user-id> \
CHANNEL_ID=<渠道id> \
SUB_ACCOUNT_ID=<订阅账号id> \
RECONCILE_DSN=<mysql-dsn> \
./scripts/verify-forced-failure.sh
```

脚本自动执行两个场景：

- **场景一**：禁用普通渠道 → 断言订阅账号实际服务、fallback 增长、账本归属 subscription、
  单次计费；
- **场景二**：禁用订阅账号 → 断言普通渠道实际服务、fallback 增长、账本归属 channel、
  单次计费。

退出码 `0` 全部通过。`DISABLE_CHANNEL_CMD` / `ENABLE_ACCOUNT_CMD` 等环境变量可覆盖
禁用/启用命令，适配不同环境的强制失败方式。

> 前置：验证环境流量安静（该用户 5 分钟内无其他请求）；渠道与订阅账号均配置了
> 可回退的另一来源；`BILLING_CACHE_CREATION_MODE=charge`。

## 三、手工执行清单（无脚本环境的等价步骤）

### 步骤 1：基准采样

```bash
# relay 回退计数（记录绝对值，之后对比）
curl -s $RELAY_BASE/metrics | grep '^micro_one_api_routing_fallback_total' | sort

# routing-ops 视图（记录 fallback_rate / partial）
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  $ADMIN_BASE/api/admin/routing-ops | python3 -m json.tool

# 用户基准 reservation id
mysql -e "SELECT COALESCE(MAX(id),0) FROM billing_reservations WHERE user_id='<user-id>'" oneapi
```

### 步骤 2：强制普通渠道失败（场景一）

```bash
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  $ADMIN_BASE/api/channel/disable/<渠道id>
sleep 3
# 发起一次请求（应回退到订阅账号成功）
curl -s -X POST $RELAY_BASE/v1/chat/completions \
  -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -d '{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"forced-failure channel"}]}'
```

### 步骤 3：断言回退可观测

```bash
curl -s $RELAY_BASE/metrics | grep '^micro_one_api_routing_fallback_total' | sort
# 期望：对比步骤 1，总数增长，reason 为上游失败类（如 upstream_5xx / timeout / circuit_open）
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  $ADMIN_BASE/api/admin/routing-ops | python3 -m json.tool
# 期望：rates.fallback_total > 0，partial=false，alerts 无 upstream_cost_missing
```

dashboard 侧：管理端「Routing Ops」页面 fallback 面板应出现增长，Grafana
`micro_one_api_routing_fallback_total` 增长；原因在指标 label 与结构化日志中可见。

### 步骤 4：断言归属与单次计费

```bash
go run ./scripts/verify/forced_failure_checks.go \
  -dsn "$RECONCILE_DSN" \
  -user '<user-id>' -after-reservation-id <步骤1的max-id> \
  -serving-kind subscription
```

等价 SQL：

```sql
-- 找到实际服务（committed）的 reservation
SELECT reservation_id, channel_id, status FROM billing_reservations
WHERE user_id = '<user-id>' AND id > <步骤1的max-id> AND status = 'committed'
ORDER BY id ASC LIMIT 1;

-- 账本：每个 (reference_id, cost_source) 恰好一行；dedupe key 无重复
SELECT reference_id, cost_source, COUNT(*) rows,
       GROUP_CONCAT(ledger_dedupe_key) dedupe_keys,
       COALESCE(SUM(ABS(amount)),0) charged
FROM billing_ledgers
WHERE type='consume' AND reference_id = '<committed_reservation_id>'
GROUP BY reference_id, cost_source;

-- 归属：只有订阅账号维度 > 0，channel 维度为 0；被禁用的渠道无该 reservation 的 consume 行
SELECT channel_id, subscription_account_id, model_name, prompt_tokens,
       completion_tokens, cache_read_tokens, cache_creation_5m_tokens,
       cache_creation_1h_tokens, shadow_cost
FROM billing_ledgers
WHERE type='consume' AND reference_id = '<committed_reservation_id>';
```

期望结果：

- `rows` 每 `cost_source` 为 `1`；`dedupe_keys` 无重复；
- `subscription_account_id > 0` 且 `channel_id = 0`；
- 被禁用的渠道在该 reservation 上无任何 consume 行（失败尝试的 reservation 状态为
  `released`，只产生 refund，不产生 consume）。

### 步骤 5：恢复并反向验证（场景二）

```bash
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" $ADMIN_BASE/api/channel/enable/<渠道id>
curl -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"status":0}' $ADMIN_BASE/v1/subscription-accounts/<订阅账号id>/status
sleep 3
# 再发一次请求（应回退到普通渠道成功），重复步骤 3/4，serving-kind 改为 channel
```

`ENABLE_ACCOUNT_CMD` 对应恢复：

```bash
curl -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"status":1}' $ADMIN_BASE/v1/subscription-accounts/<订阅账号id>/status
```

## 四、结论回写发布说明

发布说明（`docs/releases/release-vX.Y.Z.md` 的验证节）必须包含：

- 执行时间、环境（staging / 生产窗口）；
- 两个场景各自：被禁用的来源 id、实际服务来源 kind、fallback reason label、
  reservation id、账本行数与 charged quota；
- 三项验收全部 PASS 或具体失败项与处理结论；
- 使用的命令（引用本 runbook 与 `scripts/verify-forced-failure.sh`）。

## 五、关联

- 告警与 SQL 口径：[cache-creation-charge-monitoring.md](./cache-creation-charge-monitoring.md)
- 周期对账：[scripts/reconcile/README.md](../../scripts/reconcile/README.md)
