# 分组与订阅合约生产运维 Runbook（路由分组 / 订阅合约 / 结算模式 / 有序候选组）

> 相关 runbook：[生产发布、回滚与排障总入口](./subscription-production-runbook.md)、[订阅套餐配置与购买发放](./subscription-plan-runbook.md)、[表分区](./table-partitioning-runbook.md)。
> 设计依据：[group-redesign-v2.md](../design/group-redesign-v2.md) 及阶段 [B](../design/group-redesign-v2-phase-b.md) / [C](../design/group-redesign-v2-phase-c.md) / [D](../design/group-redesign-v2-phase-d.md) / [E](../design/group-redesign-v2-phase-e.md) / [F](../design/group-redesign-v2-phase-f.md) 完成记录。

本 runbook 让运维人员只按本文档即可完成分组重设计（routing group v2）与订阅合约
（subscription contracts / entitlements）在生产环境的迁移核对、开关启用、验证与回退。

**当前生产基线（2026-09-12）**：代码与迁移已全部部署到生产，但 **所有阶段开关默认关闭**——
即「新代码 + 旧行为」。在不打开任何开关的情况下，系统行为与重设计前完全一致
（所有用户走服务端默认组、legacy 订阅合同、inherit 模式）。本文的启用步骤是
**逐阶段、可灰度、可回退** 的；不要在同一窗口内跳阶段全开。

## 一、能力地图（这一阶段族到底交付了什么）

| 能力 | 阶段 | 默认状态 | 说明 |
| --- | --- | --- | --- |
| 组实体 / 成员关系 / 双写 | B | 已启用（`CHANNEL_ROUTING_GROUP_DUAL_WRITE`） | `routing_groups` 及 channel/account 成员表；B 的组回填已完成 |
| 请求快照 v2（冻结价格与上下文） | C | 关 | billing 按冻结快照结算，在途改价/换组不影响已预扣请求 |
| identity 路由事实（默认组 / 授权 / revision） | C | 关 | 用户与 Token 携带路由事实，鉴权返回 `routing_context_version=2` |
| fixed 组 Key / 多组授权 / 可用目录 / outbox 事件 | D | 关（创建开关） | Key 可绑定固定组；授权变更经 outbox 投递 |
| 订阅合约 / 覆盖组 / 结算模式 | E | 关 | 冻结合同 + `wallet_only` / `subscription_first` / `subscription_only` |
| 有序候选组（ordered/Auto）/ 用户专属倍率 / 组内优先级权重覆盖 | F | 关 | Key 携带显式有序组列表；用户倍率替换组基础倍率 |

## 二、迁移清单（091–100）与 per-schema 执行

> 编号以仓库 `migrations/` 实际文件名为准。设计文档主文件 §8.2 正文使用的是
> 旧编号叙述（C 写作 091/092 等），以各阶段完成记录与本表为准。

| 迁移 | 归属 schema（ownership） | 阶段 | 作用 | 风险 |
| --- | --- | --- | --- | --- |
| `091_create_model_health_states` | channel | （模型健康，非本族） | 按来源的模型健康快照 | 新表 |
| `092_create_routing_groups` | channel | B | 组实体 + channel/account 成员 + backfill 表 | 新表 + 加列 |
| `093_add_request_snapshots` | billing | C | reservation 请求快照列 + `subscription_window_charges` | 加列 + 新表 |
| `094_add_identity_routing_facts` | identity | C | users/tokens 路由事实列 + `user_routing_group_grants` | 加列 + 新表 |
| `095_create_routing_change_outbox` | identity + channel + billing | D（E 复用） | 授权/生命周期事件 outbox，**三个 schema 各建一份** | 新表 |
| `096_add_subscription_contracts` | billing | E | 合同 / 覆盖组 / 权益版本 / 幂等回执 | 新表 + 加列 |
| `097_create_routing_billing_policies` | billing | E | 价格/模式版本与合同引用互斥行 | 新表 |
| `098_identity_ordered_token_groups` | identity | F | Token 显式有序候选组列表 | 新表 |
| `099_billing_user_routing_price_overrides` | billing | F | 用户专属组倍率 heads + history | 新表 |
| `100_channel_routing_relation_overrides` | channel | F | 成员关系 priority/weight 覆盖（可空列） | 加列 |

全部为 additive（新表/加列/可空列），无回填型改写；093/094 的 **用户路由事实回填**
由独立工具完成（见 §四 阶段 C 步骤 3），不是迁移文件的一部分。

### 2.1 生产执行方式（schema 隔离部署，无 Go 环境）

生产 MySQL 为 per-service schema（`oneapi_identity` / `oneapi_channel` / `oneapi_billing` / …），
迁移必须 **逐 schema 带 `-ownership` 执行**，且只应用编号迁移——
`phase1_indexes.sql` / `phase3_partitioning.sql` / `schema_split.sql` 是参考 DDL，
绝不能进入自动迁移目录（见 [deployment.md](../deployment.md) §10）。

服务器上没有 Go 工具链，使用静态编译的 `migrate` 二进制（本地交叉构建）经
`docker run` 在容器网络内执行：

```bash
# 本地（Apple Silicon）交叉构建一次，产物上传到 /opt/micro-one-api/migrate
docker buildx build --platform linux/amd64 --output type=local,dest=/tmp/migrate-out -f - . <<'EOF'
FROM golang:1.27-alpine AS builder
RUN apk add --no-cache git ca-certificates build-base sqlite-libs sqlite-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags='-s -w -extldflags "-static"' -o /out/migrate ./cmd/migrate
FROM scratch
COPY --from=builder /out/migrate /migrate
EOF
scp /tmp/migrate-out/migrate $DEPLOY_REMOTE_SERVER:/opt/micro-one-api/migrate

# 服务器上：先 dry-run 看 pending，再去掉 -status 应用
PW=$(grep -m1 '^MYSQL_ROOT_PASSWORD=' /opt/micro-one-api/docker-compose/.env | cut -d= -f2-)
for pair in identity:oneapi_identity channel:oneapi_channel billing:oneapi_billing; do
  svc=${pair%%:*}; db=${pair##*:}
  docker run --rm --network docker-compose_backend \
    -v /opt/micro-one-api:/opt/micro-one-api \
    -e MIGRATIONS_DSN="root:${PW}@tcp(mysql:3306)/${db}?charset=utf8mb4&parseTime=True&loc=Local" \
    mysql:8.0 /opt/micro-one-api/migrate -dir /opt/micro-one-api/migrations -ownership "$svc" -status
done
```

执行前的强制前置：

1. **备份全部服务 schema**（不只 `oneapi` 主库）：
   ```bash
   docker exec mysql sh -c 'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --quick \
     --databases oneapi oneapi_identity oneapi_channel oneapi_billing oneapi_log \
     oneapi_config oneapi_monitor oneapi_notify oneapi_admin' | gzip \
     > /opt/micro-one-api/backups/oneapi-schemas-$(date +%Y%m%d-%H%M%S).sql.gz
   ```
2. `-status` 输出人工核对：pending 应只包含本次新增编号；如出现 `phase*` 或
   `schema_split`，说明 `-dir` 指错或被加入了参考 DDL，停下排查。

### 2.2 已知坑：老 schema_migrations 的 `applied_at` 无默认值（2026-09-12 实录）

早期建立的 per-service `schema_migrations` 表是 `applied_at BIGINT NOT NULL`（无默认值），
而 runner 只插 `version` 列，会报：

```
error: apply 0xx_...: record applied: Error 1364 (HY000): Field 'applied_at' doesn't have a default value
```

此时该迁移的 DDL **已经提交**（MySQL DDL 隐式提交），只是没记录。处置：

```sql
-- 1) 给每个服务 schema 补默认值（表达式默认值，MySQL 8.0.13+）
ALTER TABLE oneapi_<svc>.schema_migrations
  MODIFY applied_at BIGINT NOT NULL DEFAULT (UNIX_TIMESTAMP());

-- 2) 逐表确认该迁移创建的对象已存在后，手动补录版本记录
INSERT INTO oneapi_<svc>.schema_migrations (version) VALUES ('0xx_<name>');

-- 3) 重跑 migrate，剩余迁移正常应用
```

切勿在补录前直接重跑：迁移文件不带 `IF NOT EXISTS` 的会因对象已存在而报错。

## 三、运行时开关总表

| 开关 | 服务 | 默认 | 阶段 | 作用与前置 |
| --- | --- | --- | --- | --- |
| `CHANNEL_ROUTING_GROUP_DUAL_WRITE` | channel-service | （B 起应为 `true`） | B | 成员变更双写组关系；C/D/E/F 全程必须保持 `true` |
| `BILLING_REQUEST_SNAPSHOT_V2` | billing-service | `false` | C | 预扣写 v2 请求快照、按快照结算。前置：093、全部 billing 实例与异步消费者已升级 |
| `IDENTITY_ROUTING_V2` | identity-service | `false` | C | 鉴权返回 `routing_context_version=2`。前置：094 + 用户回填完成 |
| `IDENTITY_DEFAULT_ROUTING_GROUP` | identity-service | `default` | C | （非布尔）开启 v2 后新用户的服务端默认组 |
| `RELAY_ROUTING_CONTEXT_V2` | relay-gateway | `false` | C | relay 使用 v2 路由上下文。前置：identity/channel/billing 能力检查通过 |
| `ADMIN_ROUTING_FIXED_KEYS` | admin-api | `false` | D | 开放新建 fixed Key / fixed 改组 / 新增管理员授权（只控制「新增」） |
| `SUBSCRIPTION_ENTITLEMENTS_V2` | admin-api + billing-service + relay-gateway | `false` | E | 订阅合约与结算模式；**三处必须一致开启** |
| `ADMIN_ROUTING_ORDERED_KEYS` | admin-api | `false` | F | 开放创建 ordered Key |
| `RELAY_ROUTING_ORDERED` | relay-gateway | `false` | F | relay 服务 ordered 候选组 |

## 四、生产启用顺序

总原则（设计文档 §8.3）：先部署支持新字段的 channel、billing、identity（保留旧契约），
再部署 relay / admin 与前端；**开启任何开关前必须检查能力版本**——旧实例可能静默忽略
新字段，不能只依赖 proto 向后兼容。每阶段完成后观察并验证，再进入下一阶段。

### 阶段 C（请求快照 + identity 路由事实）

1. 确认 B 已完成组回填，所有 channel 写实例 `CHANNEL_ROUTING_GROUP_DUAL_WRITE=true`；
   应用 093（billing）、094（identity）。
2. **先将所有 billing 实例和异步消费者升级到能读 v2 快照的代码**，再开
   `BILLING_REQUEST_SNAPSHOT_V2=true`。不可留下会忽略新快照的旧提交消费者。
3. 在暂停用户注册、管理员改组等 identity 写入的窗口执行用户回填（经 channel RPC
   读组映射；演练整体回滚，`--apply` 才提交；需要 `IDENTITY_SQL_DSN`、
   `CHANNEL_GRPC_ENDPOINT`、`SERVICE_TOKEN`）：
   ```bash
   go run ./app/identity/cmd/routing-backfill --driver=mysql            # 演练（含事务写入与锁，非只读）
   go run ./app/identity/cmd/routing-backfill --driver=mysql --apply    # 提交
   ```
   所有 identity 写实例开 `IDENTITY_ROUTING_V2=true` 后恢复写入。
4. 能力检查（见 §五.1）：identity 返回 `routing_context_version=2`、billing 返回
   快照能力后，relay 开 `RELAY_ROUTING_CONTEXT_V2=true`。混合版本缺少契约会拒绝新请求。
5. 用原组请求做小额验证：金额、订阅/钱包分账、Key 额度及会话，全部一致再进入 D。

### 阶段 D（fixed Key / 多组授权 / outbox）

1. 保持 C 前提；应用 095（identity/channel/billing 三 schema 各一份）；identity/channel
   启动就绪检查会包含 outbox 表。
2. 先升级所有 billing 实例与提交消费者（确认版本 2 快照及 `fixed_routing` 能力），
   再升级 channel、identity、relay、admin。各服务 Redis 指向共享的事件集群。
3. 确认四个既有开关保持 `true`（DUAL_WRITE / IDENTITY_ROUTING_V2 /
   BILLING_REQUEST_SNAPSHOT_V2 / RELAY_ROUTING_CONTEXT_V2）。
4. 更新前端静态文件；完成当前资格、模型目录、实际价格和一个小额 fixed 请求的
   路由/预扣/账单核对后，才设 `ADMIN_ROUTING_FIXED_KEYS=true`。
5. 上线监控：outbox 待投递记录、事件投递错误、权威 RPC 延迟与失败率，执行实际负载验证。

### 阶段 E（订阅合约 / 结算模式）

1. 备份并在 billing 的共享订阅数据库应用 096/097；admin/billing/relay 必须使用同一
   订阅数据库（合同表不能分放在独立 admin schema）。
2. 部署含 D/E 契约的 channel、identity、billing，再部署 admin/relay 与前端；
   D 的回填与开关条件已全部满足。
3. 开 `BILLING_REQUEST_SNAPSHOT_V2`（应已开）、`RELAY_ROUTING_CONTEXT_V2`（应已开），
   admin/billing/relay 三处一致开 `SUBSCRIPTION_ENTITLEMENTS_V2=true`。
   **检查 billing 能力 `request_snapshot_version=2`、`fixed_routing=true`、
   `subscription_contracts=true` 后才创建新合同。**
4. 先用受控测试套餐验证购买、撤权和实际组账单，再开放销售和策略修改。

### 阶段 F（有序候选组 / 用户倍率 / 关系覆盖）

1. 应用 098（identity）、099（billing）、100（channel），纯增量无需回填。
2. 开 `ADMIN_ROUTING_ORDERED_KEYS=true`（relay 未开时 ordered Key 暂不能被服务；
   identity 对旧 relay fail-closed，绝不下钻为 inherit）。
3. 部署 F 版 relay 并开 `RELAY_ROUTING_ORDERED=true`；按能力位 `user_price_overrides`
   逐步开放用户专属倍率与组内优先级/权重覆盖。

## 五、验证

### 5.1 能力版本检查（开开关前必做）

- relay 预扣前经 billing RPC `GetRoutingCapabilities` 探测；能力缺失直接拒绝。
  成功预扣后核对快照版本与上下文摘要，不匹配则补偿释放预扣，不调用上游。
- identity 鉴权响应应含 `routing_context_version=2`；identity 启动时自检新增 schema
  与未映射用户，缺能力或迁移未完成会报错（先看日志）。
- fixed 预扣需 billing 显式返回 `fixed_routing=true`；仅理解 C 阶段版本号的实例
  无法通过此检查。
- E 阶段门槛：`request_snapshot_version=2` + `fixed_routing=true` +
  `subscription_contracts=true`；F 阶段看 `user_price_overrides` 能力位。

### 5.2 业务小额核对（每阶段的「步骤 5」）

用真实小额请求核对：路由落到预期组、预扣/账单金额正确、订阅/钱包分账正确、
Key 额度与会话行为不变。账单详情应显示实际组 ID/键与快照摘要
（缺快照标「未知」，不允许用当前默认组补造）。

### 5.3 页面

- 管理后台「分组」（`/admin/routing-groups`）：组 CRUD、成员、资源覆盖（F）。
- Tokens 页：各组模型 / 资格来源 / 有效倍率 / 价格来源版本 / 订阅费用覆盖。
- 套餐管理（E）：覆盖组选择与结算策略版本编辑；用户侧售卖卡片与订阅进度。

### 5.4 指标

outbox 待投递记录数、事件投递错误、权威 RPC（identity/billing/channel）延迟与失败率；
D 阶段起纳入日常看板。

## 六、回退与排空

**通用原则**：保留 additive 字段与历史证据，永不以删表/删列回滚；回退目标必须是
「支持 v2 契约、可关闭新增功能」的兼容版本——可停止新建，但仍须服务已有对象。

- **阶段 C**：先停止新 v2 准入并排空 v2 reservation，再关快照开关或回退 billing。
  新代码提交已有 v2 reservation 时始终读快照（即使开关已关）；**旧二进制不理解
  该证据，不能用它结算在途 v2 请求**。异步 worker 停止接收后以最长 30 秒的独立
  单笔结算 context 排空队列，再关库。
- **阶段 D**：关 `ADMIN_ROUTING_FIXED_KEYS` 只停新增。仍有 fixed Key 或 v2 预扣时
  不得关闭运行时能力或回滚到不理解的旧实例。顺序：关创建开关 → 停止新建需回退的
  配置 → 盘点 fixed Key 引用并明确迁移 → 排空/确认在途预扣及异步结算 → 按 C 的
  混部限制回退服务。不得把 fixed Key 静默解释为用户默认组；只需停某组新调用时
  优先停用组或撤销相应来源。
- **阶段 E**：一旦产生 selected_groups 合同或仅订阅策略，**不得关闭 E 回退旧二进制**；
  先停止销售/新分配，保留理解现有合同、模式与在途快照的兼容版本。
- **阶段 F**：迁移与 admin 开关步骤可自由回退；一旦有 ordered Key 在跑，旧 relay
  无法服务（identity fail-closed），回退前须先把 ordered Key 改回 inherit/fixed。
  用户倍率与关系覆盖数据对旧服务惰性安全（可空列，旧代码按全 NULL 处理）。
- **不可回退边界（设计 §8.4）**：仅 inherit + legacy 合同阶段可回退读旧字段。出现
  固定组 Key、多组授权或 selected_groups 权益后，旧程序无法完整表达语义；不能把
  所有 fixed Key 降级为 inherit，也不能让旧 billing 忽略仅订阅模式继续扣钱包。

## 七、常见故障

| 现象 | 原因 | 处置 |
| --- | --- | --- |
| 开 relay v2 后新请求被拒 | 混合版本缺契约（identity 未回 v2 / billing 能力缺失） | 按 §五.1 检查各服务能力；确认 C 步骤 3–4 顺序 |
| ordered Key 全部 401/403 | relay 未开 `RELAY_ROUTING_ORDERED`（identity fail-closed） | 开 relay 开关，或把 Key 改回 inherit/fixed |
| `SUBSCRIPTION_ENTITLEMENTS_V2` 只开了部分服务 | 三处未一致 | admin/billing/relay 补齐一致后重建受控测试订单验证 |
| outbox 待投递持续增长 | 事件消费者/Redis 未指向共享集群 | 检查各服务 Redis 配置与投递错误日志（§五.4） |
| 迁移报 `applied_at doesn't have a default value` | 老 schema_migrations 表结构 | 按 §2.2 处置，禁止直接重跑 |
| 账单详情组信息「未知」 | 该请求无冻结快照（C 前的历史行） | 正常展示行为，不是故障；禁止用当前默认组补造 |

## 八、个人版（SQLite Lite）说明

Lite 部署（`deployments/docker-compose/docker-compose.lite.yml`）的 `migrate` 一次性
容器会按 `MIGRATIONS_DRIVER=sqlite3` 自动选用 `migrations/sqlite/` 目录，091–100 的
SQLite 方言文件已随仓库提供——新装与升级都会自动应用，无需手工步骤。上述全部开关
在 Lite 中同样默认关闭；个人用户不设置即保持旧行为。如需试用分组能力，按 §三、§四
相同顺序在 Lite 环境变量中开启即可（回填工具同样可用，`--driver=sqlite3`）。
