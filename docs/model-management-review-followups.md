# Model Management Code Review — Remaining Follow-ups

> 状态: 🔴 必须修复项已在本次提交中全部完成并通过测试。下表为剩余的 🟡 中等
> 优先级与 🟢 低优先级项，作为后续修复清单保存。

本次已修复的 🔴 必须修复项（P0–P3 行为契约）：

1. `BillingModelSource` 默认值改为 `upstream`（真 legacy），避免升级即改钱。
2. `RestrictModels`：`updateChannelDB` 的 Updates map 补 `restrict_models`；Create 路径缺省 `true`（legacy）。
3. `UpdateSubscriptionAccount` 的 Updates map 补 `model_mapping`。
4. 模型路由 `platform=''` 匹配：DB 与 Memory 路径改为 `platform = ? OR platform = ''`。
5. WRR 权重归一化：`effectiveWeight = weight × healthFactor × loadFactor / 10000`（account_selector.go + selector.go），并用 `totalEffectiveWeight` 求和。
6. `BillingModelSource` 漏改路径补齐：`/v1/responses`、OpenAI Responses WebSocket、orchestrator、anthropic streaming usage log、oneapi proxy。
7. Failover/重试路径 per-account/channel mapping 重算：`relay.go` 的 `SelectSubscriptionFailover` 与 channel fallback 路径；chat/raw/responses/anthropic/ws 的 RetryExecutor 闭包改为按当前 channel 重算 `currentResolvedModel`。

---

## 🟡 中等问题（后续修复）

| #  | 问题 | 位置 |
|----|------|------|
| 1  | `DeleteChannel` 不失效 `modelsListCache`（删渠道后 15s 内 `/v1/models` 返回已无上游的模型）；其它 5 个变更路径都失效了 | `app/channel/internal/biz/channel.go` (`DeleteChannel`) |
| 2  | `ModelUsecase` 的 registry/mapping CRUD 不失效 `ChannelUsecase` 缓存（两个独立 usecase，无事件），管理端"改了不生效"最长 15s | `app/channel/internal/biz/model.go` 各变更方法 |
| 3  | 多个特定通配符同时命中（`claude-*` vs `claude-sonnet-*`）时在 Go map 上随机迭代，同一请求两次可能路由到不同上游；`HasCapability` 又是 OR 语义，与 `GetEntry` 矛盾。应按非通配字符数排序取最优 | `internal/biz/model_mapping.go:153-167`、`internal/biz/relay.go:579-590` |
| 4  | DB 路径"精确优先"是层级互斥，内存路径是单趟合并——channel A `gpt-4o` + channel B `gpt-*` 时 DB 只选 A、内存 A/B 一起加权；内存下同 channel 的两条 pattern 还会双重加权 | `app/channel/internal/data/data.go:1334,1486` |
| 5  | `RetryExecutor` 兜底 `SelectChannel(false)` 绕过"failover 不回退 catch-all"契约，可能再次选中刚失败的 catch-all 渠道 | `internal/biz/retry.go:206-213` |
| 6  | 全部账号熔断时回退 `rand.Int` 均匀随机——熔断实际 fail-open，与"开路期账号被跳过"契约矛盾；且持续失败会不断后延熔断窗口 | `app/channel/internal/biz/channel.go:585-595` |
| 7  | 路由死端专属错误只在 abilities 层触发；quota 超限/临时封锁走普通 NotFound，且 DB/内存行为不一致——可观测性目标未达成 | `app/channel/internal/biz/channel.go:546-554` |
| 8  | 选择器健康反馈链无调用方：`RecordSubscriptionAccountHealth`/`Acquire`/`Release` 生产代码零调用，`healthFactor` 恒 100、熔断永不触发——文档 §12.2 称"healthFactor 已生效"与实现不符 | `app/channel/internal/biz/account_selector.go` |

## 🟢 低优先 / 文档与实现不符

- 跨实例缓存失效未实现：设计文档称依赖 `TopicChannelChanged` 做跨实例失效，实际该事件无订阅者调 `invalidateModelsListCache`（15s TTL 兜底，影响有界）。
- 通配符精确匹配的大小写不敏感未完全达成（配置 key 混合大小写时静默无映射）；`wildcard.Match` 无 memo 递归有 CPU 放大风险（管理员配置，风险低，建议加 `*` 数量上限）。
- 路由 `priority` 无负值校验，负值被当作"未路由"导致账号被静默丢弃；`p*1_000_000+accountPriority` 在大 priority 时串桶。
- SQLite `model_routings.enabled` 缺 `NOT NULL`，与 MySQL/Postgres 不一致。
- `reserveQuota` 新增注释声称内部应用 `BillingModelName`，与实际不符（`internal/server/http_billing.go:49-54`）——注释已随本次 🔴#5 更新，请复核。
- 死代码：`invalidateGroup`（`channel.go:329`）、`wildcard.FirstMatch`；`RemoveAccount` 无调用方导致选择器状态缓慢泄漏；已存在账号的 weight 运行期变更后不刷新。

## 建议后续修复顺序

1. 🟡#1 / 🟡#2（缓存失效）——删渠道 / 改注册表后让 `/v1/models` 即时收敛。
2. 🟡#3 / 🟡#4（通配符确定性与 DB/内存一致性）——避免同一请求两次路由到不同上游。
3. 🟡#5（RetryExecutor 兜底）——避免 failover 回退到 catch-all。
4. 🟡#6 / 🟡#7（熔断 fail-closed + 路由死端错误）——可观测性与契约对齐。
5. 🟡#8（健康反馈链）——需要新增 channel-service gRPC `RecordSubscriptionAccountHealth` + relay-gateway 调用点。
6. 🟢 低优先项按需处理。
