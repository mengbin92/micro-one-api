# 模型管理累计代码审查报告

> 审查日期：2026-07-25  
> 基准分支：`develop`  
> 审查时 HEAD：`05dbcef`  
> 累计范围：`977bea7^..05dbcef`
>
> 下文“已修复”均指本次审查后的当前工作区状态，尚未创建新提交。

## 1. 审查目标与范围

本次审查以 [`docs/model-management-design.md`](../model-management-design.md) 的
分层、模型映射、路由、选择器、缓存与计费契约为准，审查累计差异而不是只看
最新提交。纳入的设计相关提交如下：

| 提交 | 日期 | 主题 |
|---|---|---|
| `977bea7` | 2026-07-22 | 动态模型发现 `/v1/models` |
| `94b5a81` | 2026-07-22 | 独立模型管理 Sprint 1 |
| `9319958` | 2026-07-22 | Sprint 2 前端与首轮 review fix |
| `4d33cda` | 2026-07-23 | Sprint 3 模型管理集成 |
| `32b07d5` | 2026-07-23 | Sprint 4 使用统计、别名、大小写匹配 |
| `e860573` | 2026-07-23 | 测试 stub 与 model id 行为修正 |
| `d0fb428` | 2026-07-23 | per-account mapping 与 `/v1/models` cache |
| `cabaf34` | 2026-07-24 | wildcard matching 与 `RestrictModels` |
| `a37815c` | 2026-07-24 | model routing、账号选择器、计费模型来源 |
| `1c2f134` | 2026-07-25 | P0–P3 must-fix |
| `05dbcef` | 2026-07-25 | follow-up review fixes |

祖先范围中混入的下列依赖更新或合并提交与本设计无关，未作为 finding 归因对象：

- `4b790a5`、`51c8773`
- `12bbd93`、`2b87ec9`
- `be6569b`

## 2. 审查方法

1. 对照设计文档 §1–§14 逐项检查累计 diff、当前实现和测试。
2. 按仓库 `AGENTS.md` 检查 DTO/DO/PO 边界：
   - service 只做 DTO ↔ DO；
   - biz 拥有 DO、usecase、repo interface；
   - data 负责 DO ↔ PO 和驱动错误映射；
   - cmd 负责跨层 wiring。
3. 重点追踪下列端到端数据流：
   - proto3 标量 presence 与 partial update；
   - requested model → global model → channel/account mapped model；
   - HTTP、Responses、WebSocket、sticky route 与 retry/failover；
   - subscription account 真实上游结果 → 健康反馈 → selector；
   - `/v1/models` L1 cache 的失效频率与跨实例收敛；
   - model usage、reserve/commit 与 `BillingModelSource` 兼容性；
   - memory/SQLite/PostgreSQL 三种仓储路径的一致性。
4. 对每个 correctness finding 增加或补强回归测试，再实施最小修复。

## 3. Findings 与处理状态

| 严重度 | Finding | 影响 | 处理状态 |
|---|---|---|---|
| P0 | subscription account health 被当作 channel health 上报；另一 client 实现为 no-op | 正常情况下反馈丢失；ID 碰撞时可能污染真实 channel 的健康状态，账号熔断器长期失真 | **已修复**：增加专用 `RecordSubscriptionAccountHealth` RPC，并由 channel-service 更新账号 selector |
| P0 | adaptor 将本地并发/RPM/session-window 拒绝当作账号上游失败 | 健康账号会因本地 admission policy 被错误降权或熔断 | **已修复**：仅在实际执行 `http.Client.Do` 后反馈，并区分 attempted/succeeded |
| P0 | `RelayPlan` 只保存首个渠道映射后的 `ResolvedModel` | retry/failover 可能在渠道 A 的结果上再次应用渠道 B mapping；sticky/WS 路径可能得到空模型或叠加映射 | **已修复**：新增 `GlobalModel`/`BaseModel()`，所有 retry、Responses、WS 与内存 sticky route 保留全局模型 |
| P0 | `CreateModel` 把 `status == 0` 同时解释为“未传”和 disabled | 无法显式创建 disabled model | **已修复**：`CreateModelRequest.status` 改为 `optional int32`，默认值只在字段缺席时应用 |
| P1 | `restrict_models`、mapping/routing `enabled` 使用无 presence 的 proto3 bool | partial update 无法区分“未传”与 explicit false，后台健康/余额更新可能静默改变业务配置 | **已修复**：字段改为 `optional`；service 保留 presence；data 更新时只改显式字段，insert 未传默认 true |
| P1 | selector 在整数除法后计算动态权重 | 默认配置权重 1 经健康降权后变成 0，账号/渠道可能永久饿死；简单 floor=1 又会把 100%:20% 拉平成 1:1 | **已修复**：使用 `int64` 固定点有效权重，保留健康/延迟比例 |
| P1 | smooth-WRR 总权重包含已跳过的熔断/过载候选 | 唯一可用候选的 `currentWeight` 会持续负漂移；候选恢复后会产生长期不公平或突发流量 | **已修复**：总权重只统计本轮实际可选候选，并增加回归测试 |
| P1 | selector 只在首次见到 ID 时缓存 channel/account 配置 | 管理端修改 weight/priority、model mapping 或 channel snapshot 后，进程可能长期继续使用旧配置 | **已修复**：每次 Select 刷新配置快照，同时保留运行期 health/currentWeight/inflight 状态 |
| P1 | `RetryExecutor.ExecuteWithAccountHealth` 把后续 generic fallback channel 的结果归因给初始 subscription account | fallback API-key channel 的成功/失败会污染原账号健康分 | **已修复**：generic executor 只记录已知 initial account；account-aware adaptor failover 按实际账号逐次记录 |
| P1 | failover 找不到低优先级替代渠道时立即终止 retry | 单渠道的瞬时 502/网络错误无法使用配置的 retry budget；集成测试稳定返回 502 | **已修复**：不调用 `SelectChannel(false)`，而是直接重试当前渠道，兼顾 retry 与“不扩大到 catch-all”契约 |
| P1 | `RecordUsage`/`RecordHealth` 高频清空 `/v1/models` cache 并广播 change event | 正常流量下缓存命中率趋近于零，并放大跨实例事件流量 | **已修复**：只有会改变可见模型集合的 mutation 才失效缓存 |
| P2 | 文档称账号选择器已 load-aware，但生产链路未调用 `Acquire`/`Release` | 运维和容量预期与实际行为不一致 | **文档已修复**：当前明确为 health-aware；`loadFactor` 是未接线的预留 hook |
| P2 | 文档把 `BillingModelSource` 默认值写为 `requested`，代码和历史行为实际为 `upstream` | 升级兼容性判断错误，可能错误调整计费 key | **文档已修复**：默认/未知值均为 `upstream`，保持 legacy 行为 |
| P2 | Redis Streams consumer group 被用于 model cache invalidation | consumer group 是竞争消费而非每实例广播；不是每个实例都会收到每条 invalidation | **剩余风险**：L1 主要依靠 15 秒 TTL 最终收敛，见 §6 |
| P2 | Redis sticky value 只持久化 channel ID | 跨实例 continuation 若客户端完全省略 model，无法恢复 `GlobalModel`/`ResolvedModel` 元数据 | **剩余风险**：本轮保持 value schema 兼容，见 §6 |
| P3 | 生产源码引用不存在的 `docs/model-management-review-followups.md` 和临时 review 编号 | 注释不可追溯，维护者无法定位真实契约 | **已修复**：关键注释改为行为说明并指向本设计文档；生产源码中的临时 review 标记已清理，测试注释仅保留回归背景 |

## 4. 分阶段修复记录

### 阶段 1：P0 correctness

- 增加 subscription account 专用健康反馈 RPC。
- 仅对真实上游调用结果反馈账号健康，本地 admission failure 不反馈。
- 为 `RelayPlan` 增加 pre-channel mapping 的 `GlobalModel`，修复 HTTP、Responses、
  Anthropic、adaptor、OpenAI WebSocket、retry 与 sticky route 重建。
- 修复 `CreateModel` 显式 disabled 的 presence。
- 修复 `restrict_models` 与 mapping/routing `enabled` 的 partial-update presence。

### 阶段 2：selector 与反馈一致性

- 将 channel/account selector 改为 `int64` 固定点 smooth-WRR。
- 保留 health/latency factor 的真实比例，避免 weight=1 归零或 floor 拉平。
- 总权重排除本轮已跳过的熔断/过载候选。
- Select 时刷新最新配置快照但保留运行期状态。
- 阻止 generic fallback 结果被归因给初始 subscription account。
- 无低优先级替代渠道时直接重试当前渠道，不扩大到 catch-all 候选。
- selector 当前 tier 无候选时扫描低优先级 tier；全部不可用时 fail closed。

### 阶段 3：缓存、兼容性、文档与注释

- 高频 usage/health 写入不再使 `/v1/models` cache 失效。
- 校正 `BillingModelSource` legacy 默认值。
- 明确生产选择器当前是 health-aware，而非完整 load-aware。
- 删除不存在的 review 文档引用，设计文档记录实际行为和剩余风险。

### 阶段 4：生成、格式与验证

- proto 通过 `make api` 生成，不手改 generated Go 文件。
- 对变更 Go 文件执行 `gofmt`，并运行 `git diff --check`。
- 按层运行 targeted tests，再运行需要 localhost 临时端口的完整测试、race、vet
  与前端测试/构建。

最终执行结果在 §5 更新。

## 5. 验证结果

| 命令/范围 | 结果 |
|---|---|
| `make api` | ✅ 通过，proto stubs 已重新生成 |
| `gofmt`（全部变更 Go 文件）+ `git diff --check` | ✅ 通过 |
| 设计相关 8 个 Go 包完整测试 | ✅ 通过：channel biz/data/service、admin service/server、relay biz/data/server |
| `go test ./internal/integration -run '^TestChatCompletions_RetryOnFailure$' -count=3` | ✅ 通过；该测试在审查中先暴露 retry 提前终止问题，修复后连续 3 次通过 |
| `go test -race ./app/channel/internal/biz ./internal/biz` | ✅ 通过 |
| `go vet ./...` | ✅ 通过 |
| 除 `test/e2e/suite` 外的仓库级 `go test` | ✅ 全部通过 |
| `go test ./...` | ⚠️ 代码包完成；`test/e2e/suite` 因本地未启动 identity/admin/relay/billing 服务而连接拒绝，不是单元/集成回归 |
| `web: npm test` | ✅ 26 files / 90 tests |
| `web: npm run lint` | ✅ 通过 |
| `web: npm run build` | ✅ TypeScript + Vite build 通过 |

## 6. 剩余风险与后续建议

1. **跨实例 model cache invalidation 不是广播。** Redis Streams consumer group
   会把一条消息交给一个 consumer，而不是每个 channel-service 实例。当前正确性
   依赖 L1 的 15 秒 TTL。后续应改为 Pub/Sub、每实例独立 consumer group，或显式
   version key；在此之前不要把事件流描述为强一致广播。
2. **Redis sticky schema 不包含模型元数据。** 当前只存 channel ID。跨实例的
   `previous_response_id`/session continuation 如果完全不带 model，只能恢复渠道，
   不能完整恢复 requested/global/resolved model。后续可版本化 sticky value，并
   同时保存模型三元组；需要兼容旧 value。
3. **`loadFactor` 尚未进入生产调用链。** `Acquire`/`Release` 只有进程内实现，
   relay-gateway 没有对应 RPC/lease。当前选择是 health-aware，不应基于该字段承诺
   跨实例负载均衡。
4. **`channel_mapped` 目前与 `upstream` 等价。** 这是扩展点而不是独立语义；若
   后续实现“跳过 global mapping、只按 channel mapping”，需要重新定义 reserve、
   usage log 和 model usage stats 的一致性测试。
5. **健康反馈是 best-effort。** relay 当前忽略反馈 RPC 错误以避免健康遥测阻断
   用户请求；应通过指标/日志监控反馈失败率，避免 channel-service 不可达时静默
   退化为无健康反馈。

## 7. 结论

累计实现基本遵守 DTO → DO → PO 的分层边界，主要问题集中在 proto3 presence、
多阶段模型名的语义丢失、健康反馈归因以及 smooth-WRR 动态权重。P0/P1 correctness
问题已在当前工作区按阶段修复并增加回归测试；剩余事项主要是需要 schema/分布式
一致性设计的跨实例 cache/sticky/load feedback，不宜在本轮以局部补丁扩展协议。
