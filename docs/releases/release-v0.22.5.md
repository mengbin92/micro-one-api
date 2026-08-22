# Micro-One-API v0.22.5 发布：用量核算对账与安全扫描修复

> 2026-08-22 · 上一版：[v0.22.4](./release-v0.22.4.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.22.5)

v0.22.5 是 v0.22.4 的 **PATCH 账务正确性与安全修复版本**：分离支付与执行核算口径，补齐订阅流量的上游成本、为 channel 用量和 usage log 写入增加事务级幂等键，并关闭 gosec / gitleaks 安全扫描阻塞项。

本版本包含 **三个向后兼容数据库迁移**：`080_add_billing_ledger_cost_audit_fields`、`081_create_channel_usage_events`、`082_create_log_ingest_dedupe_claims`。升级必须先完成 080-082 迁移，再按顺序部署 `billing-service`、`channel-service`、`log-service` 和 `relay-gateway`。

## 1. 支付与执行核算分离，订阅成本不再遗漏

**根因**：对账把订阅账本的应收金额直接与钱包余额比较，口径混用；订阅吸收请求费用时，上游成本没有随账本固化；历史 consume 账本缺少执行来源与上游模型审计维度，导致渠道对账、供应商成本审计和毛利口径不可靠。

**修复**：将支付侧应收与执行侧成本拆分为独立字段；账本新增 `source_kind`、`upstream_model_id`、`cost_audit_status`，新写入按请求固化来源和模型，历史行标记为 `legacy` 而不用当前价格伪造成本；渠道对账按 reservation 去重后同时核对用量和上游成本。

**影响服务**：`billing-service`（biz / data / reconciliation）、迁移 `080`；`relay-gateway` 传递来源与上游模型。

## 2. Channel 用量计数事务级幂等

**根因**：billing 结算后的 channel 用量上报没有稳定幂等键，重试或异步结算路径可能重复累加 `channels.used_quota`，形成持续 channel-vs-ledger 漂移。

**修复**：`RecordChannelUsageRequest` 新增 `reservation_id`，relay 以 billing reservation 作为稳定生产者键；channel-service 在同一事务内插入 `channel_usage_events` claim 并更新渠道计数，重复 reservation 直接幂等返回。

**影响服务**：`relay-gateway`、`channel-service`、迁移 `081`；proto 仅新增字段，兼容旧客户端。

## 3. Usage Log 事务级幂等

**根因**：relay 的集中式 usage log 投递没有持久化幂等键，日志服务或网络瞬断后的重试可能重复写入 consume 日志，反过来污染 ledger/log 双写对账。

**修复**：`IngestLogRequest` 新增 `dedupe_key`，relay 使用 `consume:<user_id>:<request_id>`；log-service 在写入日志与 dedupe claim 的同一事务内裁决重复，历史 consume 日志由 `082` 回填 claim。relay 对投递增加有限重试并记录 dedupe key。

**影响服务**：`relay-gateway`、`log-service`、迁移 `082`；前端生成类型同步补充 `dedupeKey`。

## 4. 历史漂移修复脚本与安全扫描修复

**根因**：存量数据已经存在 channel 计数与 usage log 缺口，直接上线新幂等逻辑只能阻止新增漂移；另外 gosec 将两个 SSE passthrough 判定为 G705 污点 sink，前端测试中的高熵合成 API key 触发 gitleaks 全历史扫描失败。

**修复**：新增 `scripts/reconcile/repair_usage_accounting.sql`，以去重后的 billing ledger 为权威来源修复历史 channel `used_quota` 与缺失 consume 日志；脚本默认 `ROLLBACK`，预览确认后才改为 `COMMIT`。对两个已验证为不透明 SSE 字节流的写入点补充精确 G705 suppression；测试常量改为显式 `TEST-PLATFORM-KEY`，并在 `.gitleaksignore` 保留历史 fingerprint。

**影响服务**：`relay-gateway`、Web 测试资产、安全扫描配置；历史修复脚本需 DBA / 运营审批后执行。

## 兼容性说明

- **API / proto**：无破坏性变更。`RecordChannelUsageRequest.reservation_id = 3` 与 `IngestLogRequest.dedupe_key = 19` 均为新增字段，旧客户端可继续省略；省略时保留旧的非幂等行为或由服务端生成语义。
- **数据库**：新增迁移 080（billing ledger 三个审计列并回填 `source_kind`）、081（`channel_usage_events`）、082（`log_ingest_dedupe_claims` 并回填历史 consume claim）。均为新增列 / 新增表，不删除旧数据。
- **配置**：无新增配置项。
- **服务影响**：需更新 `billing-service`、`channel-service`、`log-service`、`relay-gateway`；`admin-api`、`identity-service`、`config-service`、`monitor-worker`、`notify-worker` 无二进制变更。Web 仅有生成类型刷新，无用户可见变化，可不重新发布 `web/dist`。
- **历史修复**：`repair_usage_accounting.sql` 默认回滚，只会在人工确认预览并改为 `COMMIT` 后生效；历史账本保留 `cost_audit_status=legacy`，不用当前价格回填成本。

## 升级步骤

```bash
git fetch --tags
git checkout v0.22.5
```

**必须先迁移、后部署**：

1. 备份 billing、channel、log schema。
2. 在 billing schema 应用 `080`。
3. 在 channel schema 应用 `081`。
4. 在 log schema 应用 `082`。
5. 依次部署 `billing-service`、`channel-service`、`log-service`。
6. 最后部署入口 `relay-gateway`，开始传递 reservation / dedupe key。
7. 观察对账结果；如需修复历史漂移，按 `scripts/reconcile/README.md` 执行 rollback-first 脚本并人工确认预览。

```bash
./scripts/deploy-update.sh billing-service channel-service log-service relay-gateway
```

## 验证

- 后端单测：`internal/server`、`app/billing/internal/biz`、`app/billing/internal/data`、`app/channel/internal/biz`、`app/channel/internal/data`、`app/log/internal/biz`、`app/log/internal/data` 通过。
- Web：`CCSwitchDialog.test.tsx` 通过；`npm run generate:api` 无漂移；`npm run build` 通过。
- 安全：按 CI 参数运行 `gosec -exclude-generated -exclude=G104 ./...` 为 0 issue；本地全历史 `gitleaks detect` 为 0 leaks；GitHub Security Pipeline `32563455046` 全部通过。
- CI：Backend、Frontend、MySQL / PostgreSQL migration smoke、loopback integration、amd64 Docker matrix 通过；arm64 matrix 按 release 前既定验证记录继续执行，不阻塞本发布说明定稿。
- 生产预部署验证：080-082 已在 schema-isolated MySQL 应用，4 个受影响服务健康，真实流量持续写入，`log_ingest_dedupe_claims` 历史回填 31,479 条。

## 完整变更日志

- fix: harden usage accounting reconciliation
- fix(security): close security scan findings
- chore(web): regenerate log API types
