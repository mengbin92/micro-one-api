# 系统性代码审查复核与修复报告（2026-08-25）

> 原始材料：`.claude/review-2026-08-25.md`\
> 复核日期：2026-08-25\
> 复核基线：`develop@efe93ca`\
> 状态：✅ 本轮方案复核、修复与验证已完成。executor 生产 7 天观察仍在进行，
> 不属于本轮完成状态。

## 1. 复核结论

原报告对迁移期双轨、channel 数据层体积、流取消和 CORS 默认值的风险识别总体有效，
但原短期方案不是最优执行方案，不能原样实施：

1. P1-1 正确识别了“上游成功后不能再次调用上游”，但“继续返回成功响应，再由
   cleanup / reconciliation 补齐”缺少持久化事实。现有 cleanup 会释放过期 reservation，
   reconciliation 不能从 reservation 还原实际 usage；照原方案实施可能产生免费调用。
2. P2-1 只从通用状态表移除 405 不会改变行为，因为原
   `IsProtocolCapabilityMismatch` 把所有 405 都视为能力不匹配。必须先引入 endpoint-aware
   类型，再移除通用 405。
3. P2-5 建议用跨服务一致性测试继续守护两份字符串，仍保留了双事实源。共享契约常量
   才能在编译期消除漂移。
4. A1、A2、P2-3 都是多批次演进，不应与本轮资金 / 并发正确性修复混在一个完成边界。

本轮采用的原则是：先修可复现的正确性问题；不在 executor 生产观察窗口内进行大范围
机械搬迁；没有可靠补偿事实时，不把计费失败伪装成业务成功。

## 2. 逐项复核与优化方案

| 原条目 | 复核结论 | 优化后的处理 | 状态 |
|--------|----------|--------------|------|
| A1 executor / legacy 双轨 | 风险成立，但双轨是 ADR 明确规定的灰度机制，已有观察和退场条件 | 继续执行 7 天观察、默认开启观察和独立 legacy 删除；当前不改构造 API | 🟡 按既有路线图进行 |
| A2 channel data 上帝文件 | 3152 / 1883 行事实成立；“纯机械、风险低”判断过于乐观 | 在对应聚合发生业务改动时分 PR 拆；每次保持 Repository 与事务边界不变 | ⏸ 独立治理 |
| A3 `domain/` 名不副实 | 不成立；仓库规范明确其为跨服务共享领域 | 保留目录名；把真实跨服务契约放入 `domain/` | ✅ 本轮已落一个契约 |
| P1-1 Commit 失败触发重复调用 | 部分成立；普通错误已有不重试测试，但带网络特征的 billing 错误会被重试策略误判 | 上游响应完成后的本地错误标记为 `PostForwardError`：不可重试、不可污染渠道健康；仍向客户端返回结算失败 | ✅ 已修复 |
| P1-2 字符串错误分类 | 严重程度被高估；provider / adaptor 已返回类型化 `UpstreamHTTPError`，但 `IsRetryable` 未复用既有类型化提取，模型权限仍是普通字符串 | `IsRetryable` 统一走 `UpstreamStatus`；模型权限在 biz 源头改为 `ReasonModelForbidden` | ✅ 已修复 |
| P1-3 chunk 发送缺取消分支 | 成立 | `readChunks` 接收请求 context，发送使用 `select` 监听取消 | ✅ 已修复 |
| P2-1 通用 405 重试 | 成立，但原修复不完整 | 仅 Responses executor 把 405 包装为类型化能力错误；通用配置与默认表移除 405 | ✅ 已修复 |
| P2-2 CORS 占位默认值 | 成立，但当前构造接口不能无侵入地返回启动错误 | 未配置时使用空 allowlist 并告警；Compose / K8s 模板不再注入占位域名 | ✅ 已修复 |
| P2-3 localStorage token | 风险成立，当前无危险 HTML 注入点，迁 cookie 需要 CSRF 与身份接口联合设计 | 保持现状，另立会话迁移方案，不在本轮半迁移 | ⏸ 独立设计 |
| P2-4 双 `RelayRequest` | 迁移期重复成立 | 与 legacy 删除同批收敛，避免观察期同时改执行模型 | ⏸ 随 executor 退场 |
| P2-5 构造函数冗余 | 原报告数量错误：当前是 3 个公开构造函数，不是 5 个 | legacy 调用方删除后再收敛；现在改 functional options 只会增加迁移噪声 | ⏸ 随 executor 退场 |
| P2-5 source-kind lockstep | 成立 | 新增 `domain/billing` 单一事实源，relay 与 billing 使用编译期常量别名 | ✅ 已修复 |

## 3. P1-1 的正确失败语义

非流式执行的关键顺序是：

```text
Reserve → Forward（上游响应已完成）→ Commit
```

Commit 报错后存在三个选择：

- **重新 Forward**：错误，会产生重复上游成本和不一致响应；
- **直接向客户端返回成功**：当前也不安全，relay 没有持久 settlement outbox，无法保证
  billing 收到实际 usage；
- **终止请求且不 failover**：本轮采用。reservation 保留其不确定状态，由 billing 现有
  幂等 / 异步结算或过期治理处理，同时避免第二次上游调用。

若未来希望 Commit 不可用时仍向客户端返回成功，前置条件是先落地持久化补偿事实，至少
包含 reservation ID、实际 usage、success、cost/source 维度和幂等键，并证明 worker 能在
进程退出后恢复。不能只加进程内 goroutine 或依赖现有 reconciliation 推断。

## 4. 本轮实际修改

### 4.1 Relay 重试与结算边界

- 新增 `PostForwardError` 类型；重试策略遇到它立即终止；
- 渠道健康把该错误视为上游已成功，避免 billing 网络错误误开上游熔断器；
- orchestrator 在 Commit 失败时关闭未交付响应并返回该终止错误；
- 回归用例使用带 `dial tcp / connection refused` 特征的错误，证明不会切换渠道或再次
  Forward。

### 4.2 类型化 405 与错误状态

- `RetryPolicy.IsRetryable` 使用 `UpstreamStatus`，优先读取 `RetryableError` 和
  `UpstreamHTTPError`；
- 模型权限失败由 biz 直接返回 `ReasonModelForbidden`，三个传输入口不再匹配
  `"not allowed"` 文案；
- 新增 `ProtocolCapabilityError`；只有 Responses executor 的 405 被显式包装；
- Chat Completions 等入口的普通 405 不再进入通用重试；
- `configs/config.yaml` 和默认策略删除 405。

### 4.3 流取消、CORS 与共享契约

- chunk channel 发送同时监听 `ctx.Done()`，消费者放弃读取后 goroutine 能退出并关闭
  upstream body；
- `CORS_ALLOWED_ORIGINS` 缺失时不再允许占位域名，默认空 allowlist；
- Compose 读取显式环境变量，K8s 基础 ConfigMap 保持空值；生产部署必须提供真实 HTTPS 来源；
- `domain/billing.SourceKindChannel / SourceKindSubscription` 成为 relay 与 billing 的
  单一事实源。

## 5. 明确不纳入本轮的工作

- 不在生产 7 天观察完成前删除 legacy handler、WebSocket 路径或双 `RelayRequest`；
- 不为 3 个迁移期构造函数引入 functional options；
- 不以“大文件行数”为唯一依据拆 channel Repository；拆分必须先标明聚合、事务和测试边界；
- 不在缺少 CSRF / refresh / logout 契约时把一部分 token 迁入 cookie；
- 不新增 relay 进程内 Commit 重试队列，它不能提供崩溃恢复，还会与 billing 异步队列形成
  双写语义。

## 6. 验证记录

> 状态：✅ 通过（2026-08-25）。Go 测试使用 `/tmp` 构建缓存；需要本机临时监听的
> 测试在获准的非沙箱测试进程中执行。

| 验证 | 结果 |
|------|------|
| 新增用例的定向回归（retry / commit failure / stream cancel / CORS / capability 405） | ✅ 通过 |
| `go test ./internal/biz/... ./internal/server/... ./platform/middleware/... ./app/billing/internal/biz/...` | ✅ 通过 |
| `go test -race ./internal/biz/... ./internal/server/... ./platform/middleware/... ./app/billing/internal/biz/...` | ✅ 通过 |
| `go test ./cmd/relay-gateway ./internal/conf` | ✅ 通过；真实 relay config 可加载 |
| `./scripts/check-architecture.sh` | ✅ 通过 |
| `./scripts/check-deployment-docs.sh` | ✅ 通过；Compose / K8s 资源与文档链接有效 |
| `python3 scripts/check-markdown-links.py` | ✅ 通过；142 份 Markdown 文档 |
| `git diff --check` | ✅ 通过 |

## 7. 完成条件

本轮只有在下列条件全部满足后才能标记完成：

1. `internal/biz`、`internal/server`、`platform/middleware` 和 billing biz 相关测试通过；
2. `./scripts/check-architecture.sh` 通过；
3. Markdown 本地链接和 `git diff --check` 通过；
4. [executor 观察手册](./v0.23-executor-observation.md) 的用户现有记录保持不变；
5. 本文状态和验证表更新为最终事实，不把尚未结束的生产观察误报为完成。

以上条件已于 2026-08-25 全部满足。本轮状态关闭；A1 / A2 / P2-3 / P2-4 等延期项
继续受 v0.23 路线图、executor 观察门槛或后续独立设计约束。
