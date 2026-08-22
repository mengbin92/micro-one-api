# Micro-One-API v0.22.4 发布：订阅空钱包、Anthropic 工具调用与缓存 Token 修复

> 2026-08-22 · 上一版：[v0.22.3](./release-v0.22.3.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.22.4)

v0.22.4 是 v0.22.3 的 **PATCH 生产修复版本**，包含订阅零余额误扣、Anthropic 工具调用丢失、Responses 回退缓存 Token 丢失和订阅续费操作入口四项修复。

本版本包含 **一次向后兼容的数据库迁移（`079_add_balance_amount_to_billing_reservations`）**，为账单预扣新增规范化 `balance_amount` 列并回填存量数据；RPC 协议新增 `ReserveQuotaResponse.balance_amount` 字段，旧字段 `balance_amount_quota` 保留为兼容别名。升级需先迁移数据库、再滚动重启相关服务。

## 1. 订阅套餐不再误扣空钱包

**根因**：订阅覆盖的定点金额被先转换成 `float64` USD 再向下取整，可能把一个最小单位的金额泄漏到钱包扣款，导致零余额订阅用户被误判为余额不足而返回 HTTP 402。后续的订阅优先扣款改动还重新引入了已废弃的 quota 术语和过期的 relay 转换因子。

**修复**：全订阅覆盖的成本保持原始整数金额不变，无限订阅显式表达为无上限；内部结算统一使用 `AmountScale`，新增规范化 `balance_amount` 字段，同时保留旧 `balance_amount_quota` 作为 RPC/数据库兼容别名，并补充迁移测试。

**影响服务**：`billing-service`；共享库 `domain/subscription` 与迁移清单同步更新。

## 2. Anthropic 工具调用经 adaptor 不再丢失

**根因**：旧 `/v1/messages` 流式桥接只处理思考块、文本块和结束原因，从不读取 OpenAI 兼容的 `delta.tool_calls`，上游发出的工具调用增量被静默丢弃；同时服务层重复实现了一次协议转换。

**修复**：将 API Key 的 `/v1/messages` 请求统一走 adaptor 管线，保留原生 Anthropic 行为，并为 OpenAI 兼容、Azure、Gemini 渠道补齐经验证的工具调用、用量、重试、超时与流式错误处理。

**影响服务**：`relay-gateway`（`internal/server`、`internal/adaptor`、`internal/apicompat`、`domain/upstream/provider`）。

## 3. Responses 回退保留缓存 Token

**根因**：Responses 转 Chat 回退在流式与非流式转换中都丢弃了 prompt token 明细，导致 Codex 用量日志记录的缓存命中始终为 0。

**修复**：将缓存明细带入 Responses usage，合并被拆分的流式 usage，并保留 Responses 上报的总量以避免缓存输入被重复计数；订阅 adaptor 流量同步应用相同的总量语义，并新增回归测试覆盖。

**影响服务**：`relay-gateway`（`internal/server`）。

## 4. 订阅续费改用日期时间选择器

**根因**：管理后台订阅账号页面使用浏览器 `prompt` 要求运营输入原始 Unix 时间戳，续费入口易错且不直观。

**修复**：替换为带原生日期时间选择器的对话框，同时保持现有 `expires_at` Unix 秒 API 载荷不变，并为时间换算补充回归测试。

**影响服务**：Web 前端 `web/dist`。

## 兼容性说明

- **API / proto**：无破坏性变更。`ReserveQuotaResponse` 新增字段 `balance_amount = 8`，原 `balance_amount_quota = 6` 标记为 `deprecated` 并继续双写，旧客户端不受影响。
- **数据库**：新增向后兼容迁移 `079_add_balance_amount_to_billing_reservations`，为 `billing_reservations` 增加 `balance_amount` 列并用存量 `balance_amount_quota` 回填；应用侧双写新旧两列，回滚安全。
- **配置**：无新增配置项。
- **其他服务**：`admin-api`、`identity-service`、`channel-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker` 无二进制变更；仅 `relay-gateway`、`billing-service` 和 Web 前端需要更新。

## 升级步骤

```bash
git fetch --tags
git checkout v0.22.4
```

按 [部署文档](../deployment.md) 完成以下步骤，**先迁移数据库、再重启 billing-service**：

1. 执行 `079` 迁移（billing 服务 schema 所有权见 `migrations/ownership.yaml`）。
2. 重新构建并部署 `billing-service`。
3. 重新构建并部署 `relay-gateway`。
4. 重新构建并发布 `web/dist`。

```bash
./scripts/deploy-update.sh billing-service relay-gateway
cd web && npm run build   # 发布 web/dist 到 /opt/web/dist
```

## 验证

- `go build ./...`：通过。
- `go vet ./app/billing/... ./app/channel/... ./internal/server/... ./internal/adaptor/... ./internal/apicompat/... ./domain/upstream/provider/...`：通过。
- `go test ./app/billing/... ./domain/subscription/...`：通过。
- `go test ./internal/server/... ./internal/adaptor/... ./internal/apicompat/... ./domain/upstream/provider/...`：通过。
- Web `SubscriptionsAdminPage.test.tsx`：4 个用例通过。
- Wire 注入器已随本版本重新生成。

## 完整变更日志

- fix(billing): keep subscription charges off empty wallets
- fix(web): add date picker for subscription extension
- fix: preserve Anthropic tool calls through adaptors
- fix: preserve cached tokens in responses fallback
- docs(observation): close v0.22 post-release observation as PASS
- chore(wire): regenerate injectors after make all
