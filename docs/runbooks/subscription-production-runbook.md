# 订阅生产发布、回滚与排障 Runbook

> 对应 `docs/design/subscription-follow-up-roadmap.md` 阶段 4：文档与 Runbook。
> 范围：订阅系统（套餐、用户订阅、订阅账号、额度治理、Relay 订阅路径）的生产发布、回滚与排障。
> 相关 runbook：[OAuth 绑定](./subscription-oauth-binding-runbook.md)、[套餐配置](./subscription-plan-runbook.md)、[账号额度治理](./subscription-account-quota-governance-runbook.md)、[Redis 多副本](./subscription-redis-multi-replica-runbook.md)、[Relay 压测](./relay-stress-runbook.md)。
> 通用部署见 [部署运维文档](./deployment.md)。

本 runbook 让新部署人员只按本文档即可完成订阅系统的数据库迁移、滚动发布、回滚与生产排障。release 文档（`docs/releases/release-*.md`）只保留发布摘要，长期操作细节集中在本 runbook 与上述专题 runbook。

## 一、前置条件

1. **备份**：发布前 `mysqldump --single-transaction --routines oneapi > backup.sql`。`scripts/deploy.sh` 默认迁移前自动备份（`DEPLOY_SKIP_DB_BACKUP=0`）。
2. **镜像已构建**：`make build` 或 `scripts/deploy.sh` 的镜像构建步骤。多架构用 `DEPLOY_TARGET_PLATFORM`。
3. **维护窗口**：订阅迁移涉及 `payment_orders` / `subscription_accounts` 加列，大表可能锁；建议低峰。
4. **回滚镜像就绪**：保留上一版本镜像 tag，回滚时切回。

## 二、必填配置

发布前确认下列配置与环境变量已就位（订阅系统相关）：

| 配置文件 / 环境变量 | 必填 | 说明 |
| --- | --- | --- |
| `DATABASE_DSN` / `MIGRATIONS_DSN` | ✅ | 迁移与各服务数据库连接 |
| `REDIS_ADDR` / `REDIS_PASSWORD` | ✅（多副本） | 共享 Redis，多副本并发/sticky/blocker |
| `ADMIN_TOKEN` | ✅ | 管理后台访问 |
| `configs/relay-gateway.yaml` `hybrid_adaptor.enabled: true` | ✅ | 订阅号路径总开关 |
| `configs/relay-gateway.yaml` `session_sticky.enabled` | ⬜ | session sticky（验证 sticky 时必开） |
| `configs/relay-gateway.yaml` `subscription.enabled: true` | ✅ | 用户订阅额度中间件 |
| `configs/relay-gateway.yaml` `hybrid_adaptor.runtime_block.*` | ⬜ | 冷却时长，默认 429=5s/401=2m/5xx=2m/529=30s |
| `configs/billing-service.yaml` `payment.alipay.enabled` | ⬜（在线支付时 ✅） | 支付宝支付通道 |
| `.env` `ALIPAY_APP_ID` / 密钥 / 证书 | ⬜（在线支付时 ✅） | 支付宝凭证 |
| `PAYMENT_QUOTA_PER_UNIT` | ⬜ | quota↔金额换算，默认 500000 |
| `NOTIFY_GRPC_ENDPOINT` | ⬜ | 告警投递（额度告警/对账告警） |

完整环境变量参考见 [部署运维文档](./deployment.md) §4。

## 三、数据库迁移（必读）

### 2.1 迁移工具

```bash
# 本地 / 直连
make migrate           # apply 全部 pending
make migrate-status    # 只看状态

# 远程（deploy.sh 内置）
MIGRATIONS_DSN=... go run ./cmd/migrate -dir ./migrations
```

DSN 读取顺序：`MIGRATIONS_DSN` → `SQL_DSN`。驱动：`MIGRATIONS_DRIVER` → `SQL_DRIVER`，空则从 DSN 推断。MySQL 自动加 `multiStatements=true`。

### 2.2 订阅相关迁移清单（按顺序）

| 迁移 | 作用 | 风险 |
| --- | --- | --- |
| `034_create_subscription_accounts.sql` | 订阅账号表 + abilities | 新表，无风险 |
| `035_add_subscription_account_quota_fields.sql` | 账号额度字段 | 加列 |
| `036_add_subscription_account_id_to_billing_ledgers.sql` | ledger 加账号 id | 加列 + 索引 |
| `037_add_subscription_account_id_to_logs.sql` | logs 加账号 id | 加列 |
| `038_add_subscription_account_id_to_billing_reservations.sql` | reservation 加账号 id | 加列 |
| `039_create_user_subscriptions.sql` | 用户订阅表 | 新表 |
| `040_create_subscription_groups.sql` | 订阅分组 | 新表 |
| `041_create_account_quota_snapshots.sql` | Codex 配额快照 | 新表 |
| `042_add_subscription_group_pricing.sql` | 分组定价 | 加列 |
| `043_add_group_id_to_payment_orders.sql` | 订单加 group_id | 加列 + 索引 |
| `044_add_reservation_subscription_fields.sql` | reservation 订阅字段 | 加列 |
| `045_add_ledger_cost_source_and_dedupe_key.sql` | ledger 成本来源 + 去重键 | 加列 + 唯一索引 |
| `046_create_account_receivables.sql` | 应收账款 | 新表 |
| `047_backfill_empty_ledger_dedupe_keys.sql` | 回填去重键 | 数据回填 |
| `048_increase_subscription_usage_precision.sql` | 用量精度 | 改列类型 |
| `049_backfill_subscription_usage_from_ledgers.sql` | 从 ledger 回填用量 | 数据回填 |
| `050_create_subscription_plans.sql` | 套餐表 + 订单加 plan_id | 新表 + 加列 |
| `051_add_subscription_account_local_quota.sql` | 账号本地额度 | 加列 |
| `052_create_subscription_account_quota_events.sql` | 额度事件表 | 新表 |
| `053_add_subscription_account_5h_quota.sql` | 5h quota | 加列 |
| `054_add_subscription_account_rpm_limit.sql` | RPM 限制 | 加列 |
| `055_add_subscription_account_session_window_limit.sql` | 会话窗口 | 加列 |
| `056_add_subscription_account_quota_reset_config.sql` | reset 配置 | 加列 |
| `057_add_plan_snapshot_to_payment_orders.sql` | 订单加 plan_snapshot | 加列 |
| `057_create_subscription_account_quota_reset_runs.sql` | fixed 策略额度重置运行记录 | 新表 + 唯一索引 |
| `058_add_subscription_id_to_payment_orders.sql` | 订单记录实际发放的 subscription_id | 加列 |
| `059_enforce_single_active_subscription.sql` | 单用户单 active 订阅约束 | 生成列 + 唯一索引 |

> SQLite / Postgres 部署以 `migrations/sqlite/000_create_full_schema.sql` 与 `migrations/postgres/000_create_full_schema.sql` 为全量 schema；方言专用增量见 `migrations/sqlite/` 与 `migrations/postgres/`。

### 2.3 迁移前预检

`059_enforce_single_active_subscription.sql` 会创建单用户单 active 订阅唯一约束。执行迁移前必须确认不存在重复 active 订阅：

```sql
SELECT user_id, COUNT(*) AS active_count
FROM user_subscriptions
WHERE status = 'active'
GROUP BY user_id
HAVING COUNT(*) > 1;
```

若查询返回数据，先按业务规则保留一条 active 订阅，将其余记录改为 `expired` 或 `revoked`，再执行迁移。

### 2.4 迁移验证

```bash
make migrate-status   # 所有迁移 applied
docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" oneapi -e \
"SELECT COUNT(*) FROM subscription_accounts;
 SELECT COUNT(*) FROM subscription_plans;
 SELECT COUNT(*) FROM user_subscriptions;
 SHOW COLUMNS FROM payment_orders LIKE 'plan_snapshot';
 SHOW COLUMNS FROM payment_orders LIKE 'subscription_id';
 SHOW INDEX FROM user_subscriptions WHERE Key_name='uniq_user_subs_active_user_id';"
```

## 四、滚动发布

### 3.1 Docker Compose（单机 / 小规模）

```bash
cd deployments/docker-compose
# 1. 迁移（先于服务重启）
MIGRATIONS_DSN="$DATABASE_DSN" go run ./cmd/migrate -dir ../../migrations
# 2. 滚动重启（按依赖顺序，healthcheck 就绪才继续）
docker compose up -d mysql redis
docker compose up -d identity-service channel-service billing-service
docker compose up -d admin-api relay-gateway log-service monitor-worker notify-worker
# 3. 验证
docker compose ps
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:3000/healthz
```

`scripts/deploy.sh` 自动化上述：备份 → 迁移 → 上传镜像 → `docker compose up -d`。开关：
- `DEPLOY_SKIP_MIGRATIONS=1` 跳迁移。
- `DEPLOY_SKIP_RESTART=1` 跳重启。
- `DEPLOY_SKIP_DB_BACKUP=1` 跳备份（不推荐）。
- `DEPLOY_BUILD_PARALLEL` 并行构建。

### 3.2 Kubernetes（生产）

```bash
kubectl create namespace one-api   # 首次
kubectl apply -f deployments/k8s/deployment.yaml
kubectl apply -f deployments/k8s/identity-service.yaml
kubectl apply -f deployments/k8s/channel-service.yaml
kubectl apply -f deployments/k8s/billing-service.yaml
kubectl apply -f deployments/k8s/admin-api.yaml
kubectl apply -f deployments/k8s/phase2-services.yaml   # log/monitor/notify
kubectl apply -f deployments/k8s/ingress.yaml
```

滚动更新（镜像 tag 变更触发）：

```bash
kubectl set image deployment/relay-gateway relay-gateway=micro-one-api/relay-gateway:v0.5.0 -n one-api
kubectl rollout status deployment/relay-gateway -n one-api
# 依次更新其他服务，每步等 rollout 完成
```

建议顺序：identity → channel → billing → admin → relay → log/monitor/notify。relay 最后更新，减少迁移期请求打到未就绪的下游。

### 3.3 发布后冒烟

```bash
# 健康
for svc in 8080 3000 8001 8002 8004; do curl -s http://127.0.0.1:$svc/healthz; echo; done
# metrics
curl -s http://127.0.0.1:8080/metrics | grep micro_one_api_
# 订阅账号可调度
curl -s http://127.0.0.1:3000/v1/subscription-accounts -H "Authorization: Bearer $ADMIN_TOKEN" | jq '.data[] | {id,name,status,platform}'
# 端到端
curl http://127.0.0.1:8080/v1/chat/completions -H "Authorization: Bearer $API_TOKEN" \
  -d '{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}]}' | head
```

## 五、回滚

### 4.1 代码回滚（镜像回退）

```bash
# Docker Compose：改 image tag 或用旧镜像
docker compose up -d   # 用旧 tag

# K8s
kubectl rollout undo deployment/relay-gateway -n one-api
# 或指定版本
kubectl set image deployment/relay-gateway relay-gateway=micro-one-api/relay-gateway:v0.4.0 -n one-api
kubectl rollout status deployment/relay-gateway -n one-api
```

### 4.2 迁移回滚（谨慎）

SQL 迁移**多数不可逆**（加列可 DROP，但加列前的数据不丢；改列类型 / 回填类迁移回滚会丢数据）。原则：

1. **代码先回滚**到能与当前 schema 共存的版本（新代码通常向后兼容旧 schema 的加列）。
2. **尽量不回滚迁移**：加列类迁移对旧代码无害（旧代码不读新列）。
3. 只有「改列类型 / 删列 / 破坏性回填」才需考虑 `DROP COLUMN`，且必须确认无数据依赖。
4. 回滚迁移前**再次备份**。

加列类回滚（仅必要时）：

```sql
ALTER TABLE payment_orders DROP COLUMN plan_snapshot;
ALTER TABLE payment_orders DROP COLUMN subscription_id;
ALTER TABLE user_subscriptions DROP INDEX uniq_user_subs_active_user_id;
ALTER TABLE user_subscriptions DROP COLUMN active_user_id;
ALTER TABLE payment_orders DROP COLUMN plan_id;
-- 等等，按迁移逆序
DELETE FROM schema_migrations WHERE version='057_add_plan_snapshot_to_payment_orders';
DELETE FROM schema_migrations WHERE version='058_add_subscription_id_to_payment_orders';
DELETE FROM schema_migrations WHERE version='059_enforce_single_active_subscription';
```

### 4.3 配置回滚

```bash
# 关闭新增的订阅路径（紧急止血）
# configs/relay-gateway.yaml: hybrid_adaptor.enabled: false
# configs/relay-gateway.yaml: subscription.enabled: false
# 重启 relay-gateway
```

`hybrid_adaptor.enabled=false` 会让订阅号路径完全不启用，请求回落到 provider-factory 直连（需有对应 API Key 渠道）。`subscription.enabled=false` 关闭用户订阅额度中间件（fail-open，用户无 active 订阅也放行）。

## 六、回归门槛

每个涉及订阅的分支发布前至少执行：

```bash
make test-unit
cd web && npm test && npm run lint
git diff --check
```

涉及 Relay 行为追加：

```bash
go test ./internal/relay/... ./internal/channel/...
make test-e2e-suite
```

涉及支付/续费/退款/冲正追加：

```bash
go test ./internal/billing/... ./internal/subscription/... ./internal/admin/...
```

E2E：

```bash
./scripts/test-e2e-flow.sh          # compose 启停 + 冒烟
./scripts/test-e2e-flow.sh --suite  # Go test 套件
```

发版前建议预发环境完整跑一轮：订阅套餐购买 → 支付回调 → 订阅扣费 → 订阅账号 failover（见各专题 runbook 的验证小节）。

## 七、常见故障与排障

### 6.1 服务起不来：MySQL/Redis 未就绪

```bash
docker compose logs <service>
# 常见：depends_on healthcheck 未过、CONF_PATH 错、端口冲突
docker compose ps   # 看 unhealthy
```

### 6.2 relay-gateway 502「upstream service error」

订阅号路径未启用：`hybrid_adaptor.enabled=false`，请求回落到 provider-factory 用空 channel key。

**恢复**：开 `hybrid_adaptor.enabled: true` 重启 relay-gateway。见 [OAuth 绑定 Runbook](./subscription-oauth-binding-runbook.md) §五。

### 6.3 relay-gateway 启动报「create subscription repository」

`subscription.enabled: true` 但 `SQL_DSN` / `SUBSCRIPTION_SQL_DSN` / `DATABASE_DSN` 都没配。`NewRepositoryFromEnv` 找不到 DSN 时降级内存 repo（非生产用法）。

**恢复**：配 `DATABASE_DSN` 或关 `subscription.enabled`。

### 6.4 支付回调不发放订阅

见 [套餐 Runbook](./subscription-plan-runbook.md) §六.2。查 billing-service 日志 `assign subscription after payment`，确认 `asset_type='subscription'`、`plan_id`/`group_id` 有效、`plan_snapshot` 可解码。

### 6.5 订阅账号全不可调度

见 [额度治理 Runbook](./subscription-account-quota-governance-runbook.md) §六.1。查 `status`、额度窗口、runtime block、group/models 匹配。

### 6.6 多副本并发超限 / sticky 不生效

见 [Redis 多副本 Runbook](./subscription-redis-multi-replica-runbook.md) §五。

### 6.7 gRPC 连接失败

```bash
docker compose exec relay-gateway ping channel-service
docker compose exec channel-service netstat -tlnp   # 9002
```

确认 `CHANNEL_GRPC_ENDPOINT` 等指向正确服务名:端口。

### 6.8 迁移卡住 / 锁表

大表加列（如 `payment_orders`）在 MySQL 8.0 多数是 INSTANT/INPLACE，但仍建议低峰。若卡住：

```bash
SHOW PROCESSLIST;          -- 找 migration 连接
-- 必要时 KILL <id>，修复后重跑（schema_migrations 会跳过已 applied）
```

### 6.9 安全扫描不绿

发布前跑 `make security-scan`（gosec / govulncheck / gitleaks）。常见误报处理见 `docs/releases/release-v0.5.0.md` 的 Fixed 小节：文档示例用 `${API_TOKEN}` / `${ADMIN_TOKEN}`、公开 OAuth client id 标注、历史命中进 `.gitleaksignore`。

## 八、订阅文档索引

| 文档 | 范围 |
| --- | --- |
| [订阅系统后续规划路线图](./subscription-follow-up-roadmap.md) | 总路线图与阶段划分 |
| [订阅账号配置与导入实操指南](./subscription-account-setup-guide.md) | 字段对照 + 脚本导入实操 |
| [上游账号额度后续工作说明](./subscription-account-quota-follow-up.md) | 额度实现边界与数据流 |
| [订阅优先扣费设计](./subscription-priority-deduction-design.md) | 计费语义 |
| [订阅升级方案](./subscription-upgrade-plan.md) | 跨业务+Relay 总方案 |
| [续费语义](./subscription-renewal-semantics.md) | 续费行为与幂等 |
| [退款/冲正语义](./subscription-refund-reversal-semantics.md) | 退款账务与幂等 |
| [OAuth 绑定 Runbook](./subscription-oauth-binding-runbook.md) | OAuth 授权码绑定 |
| [套餐配置与购买发放 Runbook](./subscription-plan-runbook.md) | 套餐上下架与购买 |
| [账号额度治理 Runbook](./subscription-account-quota-governance-runbook.md) | 额度重置与恢复 |
| [Redis 多副本部署 Runbook](./subscription-redis-multi-replica-runbook.md) | 多副本共享状态 |
| [Relay 稳定性压测与 Runbook](./relay-stress-runbook.md) | 压测分层与门槛 |
| [生产发布、回滚与排障 Runbook](./subscription-production-runbook.md) | 本文 |
