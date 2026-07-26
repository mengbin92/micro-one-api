# Micro-One-API v0.10.1 发布：国内订阅账户路由修复 + 模型发现 + 安全修复

> 2026-07-25 · 上一版：[v0.10.0](./release-v0.10.0.md)（2026-07-25）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.10.1)

v0.10.1 是 v0.10.0 的**补丁版本**，聚焦修复 v0.10.0 发版后暴露的 3 个问题：国内订阅账户（智谱 GLM / MiniMax / Kimi）路由无法命中、上游模型 ID 在大小写不敏感匹配时丢失、以及 gosec / Dependabot 安全告警。

本版**无新增业务表迁移**，**无 API 破坏性变更**，**无新增/删除端点**，所有修复均向后兼容，从 v0.10.0 平滑升级即可。

## 问题现象与根因

### 1. 国内订阅账户路由无法命中（fix(relay): route domestic subscription accounts）

v0.10.0 引入国内平台订阅账户后，用户在配置了 GLM / MiniMax / Kimi 账户并实际发起对应模型请求时，relay-gateway 报 `no available channel`，账户从未被调度。

根因有两处：

- **平台推断边界过严**：`subscriptionPlatformsForModel` 对未匹配任何前缀的模型默认返回 `["codex","claude"]`，而对 `glm-` / `minimax-` / `kimi-` 这类国内平台模型前缀未注册，导致进入不到国内账户的调度路径；即使匹配到平台，`lastErr` 的返回逻辑也导致无法回退到显式 abilities / model mapping 覆盖。
- **粘性会话校验过严**：`stickySubscriptionAccountValid` 强制要求账户所属 platform 能服务该模型，覆盖了账户显式 `Models` 列表的判断，使配置了显式模型列表（含别名）的国内账户在粘性续约时被误判为不可用。

### 2. 上游模型 ID 丢失（fix: preserve upstream model IDs and discover channel models）

v0.10.0 的模型大小写不敏感匹配让路由能命中配置的模型，但 `ResolvedModel` 直接采用了用户输入的大小写拼写。部分上游（如 Anthropic、智谱）对 model ID 大小写敏感，导致实际请求被上游拒绝或路由到错误模型。

同时，创建 API-key 渠道时若未手动填写模型列表，运维需要再到上游文档查询可用模型并回填，体验割裂。

### 3. gosec G115 整数溢出 + 依赖安全告警（fix(deps): resolve dependabot & code-scanning security alerts）

- gosec G115：`int64 → int32` 的直接强转在计数场景（如 `chMap` / `subMap` / `RowsAffected` / `AvgLatency`）存在溢出风险。
- google.golang.org/grpc v1.81.1 存在 GHSA-hrxh-6v49-42gf。
- web 侧 react-router-dom v7 存在 GHSA-qwww-vcr4-c8h2；brace-expansion、postcss、@hono/node-server 也有对应 CVE / GHSA。

## 修复内容

### 1. 国内订阅账户路由修复

- **`subscriptionPlatformsForModel`**：新增 `glm-` → `zhipu`、`minimax-`/`minimaxm-` → `minimax`、`kimi-`/`k3` → `kimi` 前缀映射；**默认分支返回 `nil`**（而非硬编码 `["codex","claude"]`），明确"模型前缀是路由提示而非硬边界"。
- **`selectSubscriptionAccountForModel`**：当无前缀提示（`len(platforms)==0`）时直接走 abilities 表调度；当有前缀但全部失败时，回退到无 platform 的 abilities 调度，允许显式跨平台 ability 或 model mapping 覆盖约定。
- **`stickySubscriptionAccountValid`**：改为"显式 `Models` 列表是真相源，支持运维定义别名；仅当账户没有模型列表时才从 platform 推断"，移除强制 platform 校验。
- **错误映射**：新增 `subscription account not found` 也按 503 `no available channel` 返回，与既有 `no available channel` / `channel not found` 一致，避免客户端误判为 500。

### 2. 上游模型 ID 保留 + 渠道模型自动发现

- **`ResolveChannelModel`**：替代原 `applyPerAccountModelMapping` 的单点调用，统一 `RelayPlan` / `SelectSubscriptionFailover` 等全部 5 处模型解析。规则：显式 per-channel mapping 权威；否则当选择匹配的是配置模型（大小写不敏感）时，**保留渠道配置的大小写拼写**，避免上游因大小写差异拒绝；通配符 abilities 仅用于路由，永远不会作为模型 ID 发出。
- **`accountServesModel`**：`==` 改为 `strings.EqualFold`，与大小写不敏感匹配语义一致。
- **`ChannelModelProbeService`**（新增）：创建 API-key 渠道时若模型列表为空，异步通过上游 `/v1/models` 端点发现并回填模型列表。best-effort 设计——探测失败时保留新建渠道不动，运维仍可手动填写；带 `markPending` 去重，避免同一渠道并发探测；通过 `SetChannelModelProbe` 提供测试 seam。
- **前端**：`ModelMultiSelect` 支持 `CreatableSelect` 的 `createOption` 回写与受控输入，配合自动发现的模型即时出现在下拉中。

### 3. 安全修复

- **gosec G115**：4 处 `int32(...)` 强转改为 `safecast.Int64ToInt32Saturating`（`app/channel/internal/data/model.go` 的 chMap/subMap/两处 RowsAffected；`internal/server/http_billing.go` 的 AvgLatency；`coding_plan_quota_probe.go` 的 resetAfterFromISO delta）。
- **google.golang.org/grpc**：v1.81.1 → v1.82.1（GHSA-hrxh-6v49-42gf）。
- **react-router**：`react-router-dom@7.18.1` → `react-router@8.3.0`（v8 移除 `react-router-dom` 包，20 个文件 import 改为 `react-router`，GHSA-qwww-vcr4-c8h2）。
- **web overrides**：brace-expansion ^5.0.7→^5.0.8（CVE-2026-14257）、postcss ^8.5.15→^8.5.18（GHSA-r28c-9q8g-f849）、@hono/node-server ^2.0.5（GHSA-frvp-7c67-39w9）。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.10.1

# 开发者环境：重新生成 proto（pb.go 不入库）
make init
make proto

# 部署环境：重新构建镜像并滚动重启
docker compose build
docker compose up -d
```

**注意事项：**

- **无数据库迁移**，无需执行 `make migrate`
- **国内订阅账户**：升级后 GLM / MiniMax / Kimi 模型请求将正确路由；已配置显式 `Models` 列表的账户在粘性续约时不再被误判不可用
- **渠道模型自动发现**：仅对 API-key 渠道、且创建时模型列表为空的情况触发；探测失败不影响渠道创建，仍可手动填写
- **前端**：react-router v8 改变了 import 路径，自有二次开发需同步调整 `react-router-dom` → `react-router` 的 import

## 兼容性说明

- **API**：无破坏性变更，无端点增删
- **数据库**：无迁移
- **配置**：无新增/删除环境变量
- **运行时**：与 v0.10.0 行为一致，仅在路由命中与上游模型 ID 大小写处理上修正

## 验证

发布前已确认：

- `go build` / `go vet` / `go test` 全部通过
- `internal/biz/relay_test.go` 新增国内平台路由与粘性账户校验用例
- `internal/server/http_response_test.go` 新增 `subscription account not found` → 503 映射用例
- `app/channel/internal/service/channel_model_probe_test.go` 覆盖自动发现 best-effort 行为
- `web` 端 npm build / test（90）/ lint 全部通过

## 完整变更日志

- 1432797 fix(relay): route domestic subscription accounts
- fb14e81 fix(deps): resolve dependabot & code-scanning security alerts
- ea6b3cc fix: preserve upstream model IDs and discover channel models

## 下一步

后续版本计划：

- 模型管理高级功能（批量操作、导入导出）
- 更多国内平台订阅账户支持
- 模型使用成本分析和优化建议
- 进一步性能优化和缓存策略改进

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
