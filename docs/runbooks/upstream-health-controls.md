# 上游健康、容量与配置隔离

更新：2026-10-12。适用服务：channel-service、relay-gateway、monitor-worker。

## 证据与动作

| 结果 | 健康计数 | 独立动作 |
| --- | --- | --- |
| 已完成的真实上游成功 | 成功样本 | 匹配半开探针时开始渐进恢复 |
| 已确认的上游 5xx、传输失败、上游超时 | 失败样本 | 可用性熔断 |
| 429、423、529 | 不计成功或失败 | 来源容量冷却；遵守 Retry-After，最长 5 分钟 |
| 401、403、代理路径 404、明确的地址/渠道配置错误 | 不计成功或失败 | 隔离该配置版本，修改相关配置后重新评估 |
| 已识别的模型不可用/模型 404 | 来源健康中性 | 来源＋实际上游模型暂停 30 秒；其他模型继续可用 |
| 内容政策拒绝、输入/协议问题、本地准入、计费/凭证依赖错误、调用方取消/预算到期 | 中性 | 不清失败，不授权恢复；本地阶段不会归因给尚未调用的上游 |
| monitor `/models` 或人工测试探活 | 诊断 | 不改变推理健康和熔断状态 |

未知错误没有足够的可用性证据时保持中性。错误码不是根因证明：例如错误域名和 DNS 故障仍可能难以仅从一次失败分辨。转发准备失败通过阶段标记与真实上游结果分开。

## 权威与版本

`ClaimUpstreamExecution` 由专属 Relay 服务身份在转发前调用。它检查来源状态、当前配置指纹、实际上游模型的暂停状态，以及半开租约。普通选路、预选候选、sticky、固定 Responses 来源和后续 WS turn 都经过该执行入口；目录/授权用的 `CanRoute` 保持只读。

选路与观察携带来源配置指纹、请求开始时间、观察 ID 和半开探针 ID。指纹不暴露原始凭证。旧配置、旧事故、重复观察、过期/错误探针均不能关闭当前熔断。观察持久化失败不消费探针，可重投相同观察。传统无版本成功事件不能清除 unavailable 状态。

实际模型在 Relay 完成全局映射后解析，再由执行许可检查；旧 abilities 的空 `upstream_model_id` 和 catch-all 不会被写成覆盖全局映射的强制模型名。

## 恢复与重试

默认可用性门限沿用现有设置：持久渠道健康连续 3 次失败暂停 5 分钟；selector 在 60 秒窗口至少 10 个样本且失败率超过 50%，或连续 5 次失败，开路 30 秒。

半开仅允许一个具名探针。租约与执行 slot 使用相同 ID，初始有效期 2 分钟；现有执行 heartbeat 只续期仍有效的当前探针，因此长 SSE 不受固定 2 分钟完成上限约束。失联/过期租约无法被迟到续租复活。取消和无法确认完成的流式响应退休探针，但不计健康失败。

SSE 收到响应头不算完成；验证终止帧并完成消费后才上报成功。WS 按真实终止 turn 记录；复用连接的后续 turn 若需要新的恢复探针，会退还中性租约并要求重连，让新连接获得可续期的执行 slot。

匹配探针成功后，恢复因子按五个确认成功结果逐步升至 20%、40%、60%、80%、100%；neutral 和容量事件不推进。恢复期间的真实可用性失败立即重新开路；恢复完成后回到原有失败门限。现有健康/延迟/负载权重仍同时生效。

每个 Relay 进程共享重试预算：启动允许 10 次额外尝试，此后每 10 次初始实际调用补充 1 次重试，最多存 10 次。切换来源和原地重试都受预算约束，并对退避加 jitter；本地执行许可拒绝不会消耗上游重试额度。预算耗尽保留原始错误，过载期间可能直接返回 429/5xx，避免重试放大负载。

## 观察指标

- `micro_one_api_channel_upstream_observation_decisions_total{source_kind,kind,result}`：可用性、容量、配置、模型证据的 accepted/ignored/error。
- `micro_one_api_relay_retry_budget_rejected_total`：共享预算拒绝的额外调用。
- `micro_one_api_relay_upstream_observation_failures_total`：未成功送达 channel owner 的观察；同时记录警告日志。
- 现有 `channel_health_probe` 指标继续描述独立探活，不能拿探活成功代替推理恢复证据。

## 部署与限制

没有数据库 schema 迁移。必须先部署 channel-service，再部署 relay-gateway 和 monitor-worker，使新内部 RPC、具名身份和观察语义配套生效。旧 Relay 在过渡期仍可提供已有流量，但无版本成功无法恢复暂停渠道；不要长期混跑新旧恢复协议。

按仓库标准在本机交叉构建 linux/amd64 镜像，通过 `scripts/deploy-update.sh channel-service relay-gateway monitor-worker` 运输并重建；发布前逐服务保留原镜像及回滚标签。回滚也应让三个服务的恢复协议配套。本次代码验证不代表已经部署。

channel-service 必须保持单实例：selector 的容量、配置、模型暂停、探针和恢复斜坡目前在进程内，重启会丢失这些运行态；持久健康保留在数据库。每来源保留最近 1024 个观察 ID，超过该窗口的去重需要依赖事故/配置/探针检查。扩容前应将这些状态及原子许可迁入共享存储。

同一 owner 的快照读取、配置更新、执行许可及观察使用互斥来防止哈希版本被旧快照回滚。转发每次新增一个权威 RPC，观察 RPC 以 200ms 为限且不阻断结算。当前低流量部署优先保证一致性；大流量上限和新增延迟应在扩容前实测，再引入来源级同步或数据库配置代次。

## 验证

聚焦测试覆盖三态/阶段归因、两类来源的容量与配置隔离、实际模型暂停、租约与迟到结果、重复事件、写入失败重投、渐进恢复、sticky/cached/pinned/preselected 执行许可、SSE 终止帧和 WS 重连。

```sh
make test-unit
go test ./internal/integration
go test -race ./internal/biz ./internal/server ./internal/data ./app/channel/internal/biz ./app/channel/internal/data ./app/channel/internal/service
make format-check migration-check rbac-contract-check iam-browser-gate-check
./scripts/check-architecture.sh
```

外部 `test/e2e/suite` 需要启动 identity/admin/relay/billing 等完整服务栈，未启动依赖时的连接拒绝不能计为上游恢复逻辑的通过结果。

2026-10-12 本地结果：`make test-unit`、完整 `internal/integration`、上述相关包及上游集成场景的 race、格式/架构/迁移/RBAC/IAM 契约、相关 `go vet` 通过；API 与前端 API 类型已再生成。另一次全量 `internal/integration -race` 暴露既有 `iam_self_usage_test.go` 在 gRPC 后台运行时写全局 `time.Local` 的竞态，未将这个无关测试改写算入本次变更；完整外部 E2E 因服务栈未启动没有通过。

### 2026-10-13 线上真实推理验收

三个服务已部署 `4c83d2a4`。09:25 CST 通过普通渠道 1 调用 `qwen3.8-flash`，分别执行一次非流式和一次 SSE Chat 请求，每次限制 128 输出 token；均 HTTP 200、返回 `OK`、finish_reason 为 `stop`，SSE 收到 `[DONE]`。上游报告总 token 分别为 93、91。

两次请求各有一笔 committed 预留、一条 consume 账本和一条来源一致的消费日志；usage 为 verified、去重键无重复、账本扣费与预留 actual_cost 一致。channel 的 availability/accepted 计数增加 2，Relay 观察投递失败计数保持 0；渠道仍 healthy，三个服务无重启。临时 Token 限定目标模型、4096 配额及 15 分钟有效期，验收后删除并回查确认不存在。

[脱敏验收证据](evidence/upstream-real-inference-acceptance-2026-10-13.json)记录请求 ID、usage、账务关联及指标前后值。此轮实际路径为 legacy，未更改 executor allowlist；健康指标为聚合口径。通过结论仅覆盖普通渠道的正常 Chat 非流式/SSE 链路，未覆盖订阅、Messages、Responses、WS、orchestrator 或故障注入和半开恢复。
