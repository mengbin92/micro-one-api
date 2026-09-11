# Micro-One-API v0.28.1 发布：订阅适配器路径补齐模型健康记录

> 2026-09-11 · 上一版：[v0.28.0](./release-v0.28.0.md)（2026-09-10）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.28.1)

v0.28.1 是 v0.28.0 之后的 **PATCH 修复版本**，包含 1 个修复提交（另含 v0.28.0
发布后未合入 `main` 的 CI 稳定性修复）。修复 v0.28.0 引入的模型健康监测在**线上主
流量路径上从未生效**的问题：管理台"模型健康"页面在 v0.28.0 上线后一直为空，
`model_health_states` 表始终 0 行。

**无 proto 变更、无数据库迁移、无新增配置项**。纯新增观测写入，不改变渠道路由资格、
重试策略与计费决策。

## 1. 订阅适配器路径补齐模型健康记录

**症状**：线上管理端 `admin/model-health` 页面一直没有数据；`oneapi_channel.model_health_states`
表结构正常但 0 行。

**根因**：`/v1/responses` 入口存在三条互斥执行路径，它们**不共享遥测记录代码**：

| 路径 | 模型维度记录点 |
|------|----------------|
| RetryExecutor（legacy） | `biz/retry.go` 的 `recordHealth` |
| Responses WebSocket | `openai_ws_forwarder.go` 的 `onTurnComplete` |
| **订阅适配器（hybrid adaptor）** | **无（本次补齐）** |

线上 `configs/config.yaml` 中 `hybrid_adaptor.enabled=true`，订阅账号流量全部走
`handleSubscriptionAccountViaAdaptor` → `executeAndMeter`。该路径整体替换了
RetryExecutor，只记录**账号维度**健康（`RecordSubscriptionAccountHealth`），
从不记录**模型维度**，因此模型健康表永远为空。路由、计费与账号健康看起来都正常，
没有任何信号暴露这个缺口。

**修复**：

- `biz`：把 `RecordRoutingSourceModelHealth` 拆出共享的 `recordModelHealth` 尾部，
  并新增 `RecordSubscriptionModelHealth` 入口，把来源命名空间**显式钉在 `subscription`**。
  若沿用原方法，命名空间由 `Channel.SubscriptionAccountID` 推断，未投影的账号 ID 会被
  误记为 `channel/<id>` 幽灵行。
- `server`：`executeAndMeter` 每次真实上游尝试记录一条模型健康样本，以
  `upstreamAttempted` 守卫，本地准入失败（并发 / RPM / 会话窗口 / 凭据解析）不会污染
  健康的路由；并把上游状态码重新包成 typed `*relaybiz.RetryableError`——适配器只保留
  裸 `"upstream returned status N"`，共享的 `UpstreamStatus` 无法从该文本解析状态码，
  不重包会使所有失败都被判为"无证据"而静默丢弃。
- `data`：异步健康队列的失败日志从 `Debug` 提升为采样 `Warn`（每 100 次一条，首次立即
  输出），panic 恢复分支同样上报。此前线上 `LOG_LEVEL=info` 且计数未导出为指标，整条
  记录链路可以"全死但零信号"。
- `tests`：固化模型维度语义（来源命名空间、成功/失败映射、4xx 排除、本地准入守卫）与
  队列失败计数（传输错误与 `success:false` 两个分支）。

**影响服务**：`relay-gateway`（本次唯一需要更新的服务）。

## 兼容性说明

- **API / proto**：无变更。
- **数据库**：无迁移。
- **配置**：无新增配置项。
- **运行时**：渠道路由资格、重试策略与计费决策均不变；健康写入为异步 best-effort，
  失败不影响转发。429 在模型维度按失败计入（沿用 `modelHealthDisposition` 既有策略），
  与账号熔断刻意排除 429 的口径差异是有意保留的。
- **回滚**：旧镜像已打标签 `docker-compose-relay-gateway:rollback-20260911-174336`
  （`0a60ce492ebb`），回滚只需将 `:latest` 指回并重建容器；无需数据回退。

## 升级步骤

```bash
git fetch --tags
git checkout v0.28.1
```

1. 本地交叉构建 `linux/amd64` 的 `relay-gateway` 镜像（**不在资源受限服务器上构建**）。
2. 上传并 `docker load`；更新前先为线上现有镜像打回滚标签。
3. 仅重建该服务：`cd /opt/micro-one-api/docker-compose && docker compose up -d --no-deps relay-gateway`。
4. 升级后打开管理台"模型健康"页面，等真实订阅流量进入后确认开始累积记录。

## 验证

**已完成（发布前）**

- `make verify` 等价门禁全绿：Go unit、`-race`（subscription / biz / server / billing /
  admin / security-auth）、architecture 检查、migration-check。
- 反证：临时关闭记录调用后新增回归测试立即失败（`model health samples = [], want 1`），
  复现线上"零样本"症状。
- 镜像内容校验：二进制含 `RecordSubscriptionModelHealth`、`recordModelHealth`、
  `subscription adaptor upstream attempt failed`、`record panic` 等新代码特征串；
  镜像 `os/arch=linux/amd64`。

**已完成（发布后）**

- `relay-gateway` 容器已重建并运行新镜像
  `sha256:458464e1761f…`（构建于 2026-09-11T09:41Z，上线 2026-09-11 18:13 CST）。
- 容器状态 `running`，gRPC `:9003` / HTTP `:8080` 正常监听，`/metrics` 返回 200，
  日志无 panic；其余 14 个容器未受影响，仅 `relay-gateway` 被重建。
- 更早的验证：服务端 `RecordModelHealth` / `ListModelHealth` 读写路径经直连 gRPC
  验证正常，确认问题在调用方而非存储或查询层。

**待确认（需真实流量）**

- 管理台"模型健康"页面开始出现 `source_kind=subscription` 的行。空表在当前阶段属正常
  现象，数据随真实订阅请求产生；上线时刻线上无在途流量（10 分钟内 0 次路由选择）。
- 截至发布时 `model_health_states` 仅含 2 条人工 gRPC 验证插入的记录
  （`channel/1/healthcheck-test`、`subscription/5/k3`），非真实流量产物。

## 完整变更日志

- fix(health): record model health on the subscription adaptor path
