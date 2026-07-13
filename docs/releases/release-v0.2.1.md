# Micro-One-API v0.2.1 发布公告(含 v0.1.1 / v0.2.0 重要能力回顾)

> 2026-06-12 · 上一版: [v0.2.0](./../CHANGELOG.md) (2026-06-10) · 上上次: [v0.1.1](./../CHANGELOG.md) (2026-05-09) · 上上上次: [v0.1.0](./../CHANGELOG.md) (2026-05-06)

![Micro-One-API community cover](./assets/micro-one-api-community-cover.svg)

本次公告覆盖三个版本,让新读者**通过这一份文档了解 v0.1.0 上线之后的全部重要变化**:

- **v0.1.1(2026-05-09)** — 兼容性补丁:多 provider 渠道余额刷新、多架构 Docker、admin-api 静态资源托管。
- **v0.2.0(2026-06-10)** — 一次**重要的能力扩展**:成本/利润分析、多维 SQL 用量聚合、渠道对账与告警、缓存 token 用量、zh-CN i18n。
- **v0.2.1(2026-06-12,本次)** — 面向**管理后台可视化**和**前端构建性能**的小补丁版,顺手把 e2e 测试与 compose 环境文档对齐。无数据库迁移、无破坏性 API 变更。

如果你正在评估或刚接入 micro-one-api,这一份文档能让你**一次性了解 v0.1.0 之后的全部重要变化**。

---

## 一图速览

```
v0.1.0  ─►  v0.1.1  ─►  v0.2.0  ─►  v0.2.1 (本次)
 上线       兼容补丁     成本/对账/i18n   Top 图表 / chunk 拆分 / e2e 对齐
```

| 版本 | 主题 | 关键点 |
|---|---|---|
| v0.1.1 | 兼容性 & 部署 | OpenAI / DeepSeek / OpenRouter / SiliconFlow 渠道余额刷新;Docker 多架构支持;admin-api 静态资源托管 |
| v0.2.0 | 成本 & 对账 | `upstream_cost` 字段、多维 SQL 聚合、`RunReconciliation` 渠道校验 + ledger/log 双写比对、对账差异告警、缓存 token 用量、zh-CN i18n |
| v0.2.1 | 可视化 & 构建 | 管理后台 Top N 用量图表、Vite chunk 拆分策略、e2e / compose env 文档对齐 |

---

## v0.1.1 能力回顾(2026-05-09,兼容性补丁)

> 主要目的是让 micro-one-api 在多 provider、多架构环境下**部署更稳、跑起来更顺**,没有破坏性变更。

### ✨ 新增

- **渠道余额刷新适配多 provider** — 在 `channel-service` 余额刷新流程中补齐对 OpenAI、DeepSeek、OpenRouter、SiliconFlow 等 provider 的适配,避免这些渠道在「余额检测」步骤出现误报或失败。
- **Docker 多架构支持** — `Dockerfile` builder stage 增加 `--platform=$BUILDPLATFORM`,amd64 / arm64 一份 Dockerfile 同时可用,arm 设备(Mac M 系列、树莓派、ARM 服务器)本地构建不再绕路。
- **`admin-api` 外部托管** — `admin-api` 支持托管外部构建好的管理前端产物,生产环境可以把前端独立部署到 CDN / 静态服务器,由 `admin-api` 反向代理或直接挂载,部署形态更灵活。

### 🔧 兼容性

- 无数据库 migration、无破坏性 API 变更,直接 `docker compose pull && up -d` 即可升级。

---

## v0.2.0 重要能力回顾(2026-06-10)

> 完整 diff 范围 `cd20e88` → `b09a598`,详细列表见 [CHANGELOG.md](./../CHANGELOG.md)。

### ✨ 新增

#### 1. 成本与利润分析(Phase 2)

`billing_ledgers` 新增 `upstream_cost` 字段,relay 在提交账本时按**渠道侧定价**计算上游成本并写入。账本聚合随之支持收入/成本/毛利三个维度,管理员可以直接在后台看到「这个渠道/模型/用户在赚钱还是亏钱」。

- 迁移:`migrations/029_add_billing_ledger_upstream_cost.sql`
- 影响范围:`relay-gateway`、`billing-service`、管理后台

#### 2. 多维 SQL 用量聚合(Phase 1)

`billing-service` 新增多维聚合 RPC,按 **用户 / 渠道 / 模型 / Token 类型 / 分组 / 时间粒度(小时 | 日)** 任意组合聚合,直接走 SQL 不再走内存抽样。

- 取代了原先 admin 后台 1000 条内存抽样的统计逻辑,数字更可信。
- 迁移:`migrations/028_add_billing_ledger_aggregation_indexes.sql`,新增 `(created_at)`、`(channel_id, created_at)`、`(model_name, created_at)` 索引。

#### 3. 渠道对账(Phase 3 起步)

`RunReconciliation` 增加两类校验:

- **渠道维度**:本地累计渠道用量/成本 vs 渠道 `used_quota`,防止账本与渠道状态漂移。
- **ledger/log 双写一致性**:比对账本流水和日志服务的请求记录,发现丢失或重复。

- 迁移:`migrations/030_add_reconciliation_run_phase3_fields.sql`

#### 4. 对账差异告警

`billing-service` 检测到对账差异时,会通过 gRPC 投递到 `notify-worker`,按 `RECON_ALERT_RECIPIENTS` 创建通知。

- 投递失败**仅记日志**,不阻塞对账任务,生产环境安全可降级。
- 退化为"仅日志模式"是默认行为,不配置 `NOTIFY_GRPC_ENDPOINT` 也能正常运行。

#### 5. 成本健康 Dashboard

管理后台新增「成本/毛利/渠道余额」健康面板,聚合自 `reconciliation_runs` + 账本,管理员可以一眼看到整体毛利和异常渠道。

#### 6. 缓存 Token 用量

- 迁移:`migrations/031_add_cache_read_token_usage_fields.sql`,新增 `cache_read_tokens` 字段。
- 用量统计与账本写入支持缓存命中,后台与 `/v1/usage` 路径可见**缓存命中率**相关指标。

#### 7. 管理后台 i18n(zh-CN)

关键文案本地化为简体中文,改善日常运维体验。

#### 8. `CHANGELOG.md` 引入

项目开始按 Keep a Changelog 风格记录发布,本次公告即对应 0.1.0 / 0.1.1 / 0.2.0 / 0.2.1 段。

### 🔧 调整

- 管理员日志/用量统计**从内存抽样改为调用 billing 真实聚合**,数字更可信。
- 对账任务支持通过 `WithNotifier` / `WithRecipients` 选项装配通知;不配置通知端点时退化为仅日志模式(向后兼容)。
- 修复 relay 流式响应 logger 偶发空指针 panic。
- dashboard token 趋势 Y 轴在数值跨度大时改为紧凑单位显示。

### ⚙️ 新增环境变量

| 变量 | 说明 |
|------|------|
| `NOTIFY_GRPC_ENDPOINT` | billing 对账告警投递目标;留空退化为仅日志 |
| `RECON_ALERT_ENABLED` | 是否启用对账差异告警 |
| `RECON_ALERT_RECIPIENTS` | 收件人列表(JSON 数组,如 `[admin,ops]`) |
| `RECON_ALERT_INTERVAL` | 对账任务执行间隔(如 `1h`、`30m`) |

### 📦 数据库迁移

```
028_add_billing_ledger_aggregation_indexes.sql
029_add_billing_ledger_upstream_cost.sql
030_add_reconciliation_run_phase3_fields.sql
031_add_cache_read_token_usage_fields.sql
```

部署前请按顺序应用,迁移脚本与代码一同发布在 v0.2.0 tag 中。

### 🧪 验证(v0.2.0 当时的回归)

- `make test` 全部通过(单元 + integration)。
- `internal/billing/biz` 10 个 `Reconciliation*` 测试覆盖 notifier 装配、失败处理、默认收件人。

---

## v0.2.1 详细变更(2026-06-12,本次)

> 范围 `b09a598` → `dbcec34`,共 13 文件 / +339 / −198。

### ✨ 新增

#### 管理后台 Top 用量图表

`OverviewPage` 新增 Top N **渠道 / 模型** 用量图表,管理员可以快速识别「谁在用什么、按什么模型结算成本」。

- 新增入口:左侧导航 `AppNavigation` 增加「用量排行」。
- 文件:`web/src/pages/admin/OverviewPage.tsx`、`web/src/components/AppNavigation.tsx`。
- 单测:`AppNavigation.test.tsx` 补齐导航项断言。

```text
# 入口位置
Overview → Top Channels  /  Top Models
```

### 🔧 优化

#### 前端 chunk 拆分

`web/vite.config.ts` 增加 `build.rollupOptions.output.manualChunks` 策略,关键拆包:

| Chunk | 内容 | 体积 (gzip) |
|---|---|---|
| `react` | react / react-dom / scheduler | ~87 KB |
| `charts` | ECharts 及子模块 | ~108 KB |
| `ui` | 公共 UI 组件 / 工具 | ~43 KB |
| `query` | React Query 及其依赖 | ~26 KB |
| 各 `*Page` | 按路由懒加载 | 平均 1–4 KB |

`OverviewPage` 从单文件拆到 **~16 KB (gzip ~4 KB)**,首屏不再被 ECharts 拖慢。

### 🐛 修复

#### e2e 与 compose env 文档对齐

- `deployments/docker-compose/.env.example`:补齐 e2e token / 通知相关变量,与 `.env.example` 保持一致。
- `test/e2e/main.go`:修正 token 创建 / 校验流程以匹配 `identity-service` 当前行为,解决 `127.0.0.1:9001 connection refused` 类环境差异导致的误失败。
- 同步更新:`.env.example`、`docs/deployment.md`、`README.md`。
- commit: `d1076a5 fix: align e2e token flow and compose env docs`

---

## 升级指南

### 从 v0.2.0 升级到 v0.2.1(本次)

无破坏性变更,基本是 `git pull` + 重新构建前端:

```bash
# 后端二进制 / 镜像无破坏性变更,常规替换即可
cd deployments/docker-compose
docker compose pull   # 拉新镜像
docker compose up -d  # 重启

# 前端如果用本地构建
cd web
npm install
npm run build
```

v0.2.1 无新增环境变量、无数据库 migration、无配置格式变更。

### 从 v0.1.0 / v0.1.1 升级到 v0.2.1(跨度升级)

1. **先升到 v0.2.0**,跑完 v0.2.0 的 4 个 migration(028~031),确认对账任务首次运行无差异告警。
2. **再升到 v0.2.1**,主要动作是重新构建前端 + 重启后端。
3. v0.2.0 引入的 4 个新环境变量是**可选**的(`NOTIFY_GRPC_ENDPOINT` 留空就退化为仅日志模式),不需要立刻配齐。
4. v0.1.1 的兼容性补丁可以**跳过**,v0.2.0 已包含 v0.1.1 的全部能力。

---

## 验证(本次 v0.2.1 发版前)

- `go test ./...`(排除 `test/e2e`)— 全部通过,无回归。
- `web/ npm run test` — 16 文件 / 44 用例全过。
- `web/ npm run build` — 成功,chunk 拆分符合上表预期。
- e2e(`make test-e2e`)在 compose 栈内验证修复后的 token 流程(沙箱内无法直连 3000 / 8080 端口,需要本地起服务栈)。

---

## 致谢与反馈

- 完整 diff 范围:`v0.2.0` (`b09a598`) → `v0.2.1` (`dbcec34`),共 13 文件 / +339 / −198。
- GitHub Release: <https://github.com/mengbin92/micro-one-api/releases/tag/v0.2.1>
- 上一个版本: [v0.2.0](https://github.com/mengbin92/micro-one-api/releases/tag/v0.2.0)
- v0.1.1: <https://github.com/mengbin92/micro-one-api/releases/tag/v0.1.1>

如果你在使用中遇到问题,或者有功能建议,欢迎通过以下方式反馈:

- GitHub Issues: <https://github.com/mengbin92/micro-one-api/issues>
- 项目内已有功能可以在管理后台「系统配置」页提交反馈。

> 项目不提供任何第三方模型账号、订阅、API Key 或代理资源,部署者需自行确保上游凭证来源合法,详见仓库 `DISCLAIMER.md`。
