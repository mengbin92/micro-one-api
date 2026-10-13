# Micro-One-API v0.34.18 发布：上游健康归因与熔断恢复加固

> 2026-10-13 · 上一版：[v0.34.17](./release-v0.34.17.md)（2026-10-12）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.18)

v0.34.18 是 v0.34.17 的 **PATCH 可靠性修复版本**：区分真实上游故障、本地调用失败、容量限制和配置错误，避免限流误开熔断、中性结果清除失败及独立探活错误授权恢复。增加具名执行许可、配置与事故版本校验、共享重试预算和渐进恢复，并记录部署后的真实模型推理验收。

**无公开 API 破坏性变更，无新增数据库迁移或业务配置项**。新增内部 RPC 和兼容 proto 字段，运行时涉及 `channel-service`、`relay-gateway`、`monitor-worker`；必须先更新 Channel。当前生产已部署业务提交 `4c83d2a4`，前端不变。

## 修复内容

### 1. 健康证据与本地失败分离

**根因**：原有成功/失败计数混入上游限流、配置错误和转发前本地失败；将中性结果表示成成功会清除失败计数，`/models` 独立探活成功也不能证明真实推理恢复。

**修复**：引入成功、失败、中性三态及转发阶段标记。已确认的上游 5xx、传输失败和上游超时计可用性失败；调用方取消、预算到期、本地准入、计费依赖和输入/政策拒绝保持中性。未知错误没有充分证据时不推断故障。monitor 与人工探活仅提供诊断，不关闭推理熔断。

**影响服务**：`relay-gateway`、`channel-service`、`monitor-worker`。

### 2. 容量、配置和模型独立隔离

**根因**：429 与上游故障共用计数器会放大限流；错误凭证或单个模型不可用也不代表整个来源不可用。

**修复**：429/423/529 使用独立容量冷却，遵守 Retry-After，最长 5 分钟；401/403、代理路径 404 及明确配置错误隔离当前配置版本，相关配置变化后重新评估；已识别的模型不可用仅暂停来源与实际上游模型组合 30 秒。新增转发前 `ClaimUpstreamExecution` 和结构化 `RecordUpstreamObservation` RPC，保留具名服务身份、实际模型映射及 sticky/cached/pinned/preselected 路径准入。

**影响服务**：`channel-service`、`relay-gateway`；monitor 通过受限探活观察入口提供诊断。状态码本身不能证明根因，错误域名和 DNS 故障仍需结合配置与诊断判断。

### 3. 重试预算和可靠恢复

**根因**：独立请求重试会放大过载；没有配置、事故与探针身份校验时，旧成功、重复结果及迟到续租可能错误关闭当前熔断。流式响应头也不能证明推理完成。

**修复**：每个 Relay 进程共享重试预算，启动 10 次额度，每 10 次初始实际调用补充 1 次，最多存 10 次，并对退避加随机抖动。半开只放行一个具名探针，2 分钟租约随执行 heartbeat 续期，过期租约不可复活；校验配置版本、事故时间、观察 ID 与探针身份。验证 SSE 终态和消费完成后才报告成功，恢复因子经五个确认成功结果按 20% 至 100% 提升，恢复期间真实失败立即重新开路。

**影响服务**：`relay-gateway`、`channel-service`。新增观察决策、投递失败与重试预算拒绝指标；观察 RPC 使用 200ms 上限。

### 4. 线上部署与真实推理留证

**根因**：单元与集成测试不能替代实际部署后的鉴权、真实上游、流终止及账务闭环验证。

**修复**：2026-10-13 在本机交叉构建 linux/amd64 后，依次部署三个受影响服务；09:25 CST 使用限定模型、配额及 15 分钟有效期的临时 Token，通过普通渠道调用 `qwen3.8-flash` 非流式与 SSE Chat。两次均完整返回 `OK`，SSE 收到 `[DONE]`；两笔预留各结算一次、来源与消费日志一致，健康接受计数增加 2、投递失败保持 0。验收后删除 Token 并回查确认不存在。

**影响服务**：上述三个服务；[脱敏证据](../runbooks/evidence/upstream-real-inference-acceptance-2026-10-13.json)及[运行手册](../runbooks/upstream-health-controls.md)记录验收范围。

## 兼容性说明

- 公开接口兼容；内部新增执行许可与观察 RPC，渠道/账号信息新增配置指纹和探针 ID。先更新 Channel，再更新 Relay 和 monitor，避免新调用方访问旧服务端。
- 无数据库 schema 迁移、前端更新或新增业务环境变量；保留既有凭证、密钥和计费配置。
- `channel-service` 必须保持单实例：容量、配置、模型暂停、探针与恢复斜坡在进程内，重启会丢失这些运行态，持久健康仍保留。扩容前需共享状态与原子许可。
- 每次转发新增权威 RPC，owner 快照、配置、执行许可及观察目前串行同步；高流量延迟和吞吐仍需实测。
- 旧 Relay 的无版本成功不能清除暂停状态，不宜长期混跑；回滚应让三个服务的恢复协议配套。

## 升级步骤

1. 备份三个服务的实际镜像与 Compose，核对 Channel 为单实例及既有服务身份配置。
2. 拉取本版源码，在本机交叉构建 linux/amd64 并运输到生产，禁止在生产构建镜像：

   ```bash
   git fetch --tags
   git checkout v0.34.18
   ./scripts/deploy-update.sh channel-service relay-gateway monitor-worker
   ```

3. 按 Channel → Relay → monitor 顺序替换，核对镜像 ID、源码摘要、健康/指标接口、重启次数及观察投递失败指标。
4. 使用限定模型与预算的测试 Token 验证完整推理、SSE 终止、账务归属与单次结算，再删除临时 Token。
5. 当前生产已部署等价业务提交并通过上述验收，无需仅为发布文档重复重启。已保留 `rollback-20261013-090748` 镜像与 Compose 备份，回滚三个服务应配套执行。

## 验证

- 本地 `make test-unit`、完整 `internal/integration`、相关包及上游集成场景 race、相关 `go vet`、格式/架构/迁移/RBAC/IAM 契约检查通过；API 与前端 API 类型已再生成。
- 提交前 gosec 扫描通过，新增 retry jitter 仅用于退避，不参与凭证或安全决策。
- 三个生产服务均运行、无异常重启，健康与指标接口正常，镜像均对应 `4c83d2a4`；实际普通渠道 legacy Chat 非流式/SSE 与账务核对通过，上游总 token 分别为 93、91。
- 此次线上验收未覆盖订阅、Messages、Responses、WS、orchestrator、429/配置错误/5xx 故障注入或半开恢复；健康决策指标为聚合口径，保留并发流量贡献的限制。未扩大 executor allowlist，不作为七天准入证据。
- 外部 E2E 本地因服务栈未启动未通过；另一轮全量 integration race 暴露既有 IAM 测试修改全局 `time.Local` 的竞态。正式 Release 的 E2E 和多架构制品由标签触发流水线执行，不用上述局部验收替代。

## 完整变更日志

- `docs: confirm v0.34.17 release and observation boundary [skip ci]`
- `docs: arm executor seven-day observation [skip ci]`
- `fix(relay): separate upstream health and execution controls`
- `docs: record production upstream inference acceptance`
- `docs(release): v0.34.18`
