# P1 契约加固结论(v0.16)

> 评估日期:2026-08-06
> 评估依据:`.workbuddy/artifacts/next-roadmap.md` §P1,`docs/design/v0.11.0-roadmap.md` §9.1/§9.2
> 生产数据来源:`root@43.133.65.212` /opt/micro-one-api/docker-compose,Prometheus `prom/prometheus:v3.6.0`
> 提交:`385c7a5`(test(p1): add deterministic regression tests)

## 摘要

P1 的三项契约加固均已闭环。P1.1(同优先级精确回退)与 P1.2(并发 active 唯一约束)
代码与确定性回归测试全部就位;P1.3(订阅粘性收益)以生产指标数据落档,结论为
**维持现状(待数据)**。

---

## P1.1 — 同优先级精确回退(§9.1 风险 1):✅ 完成

### 问题

`ChannelClient.SelectChannel` 仅有 `excludeFirstPriority bool`,普通渠道失败后会整层
跳过同优先级的其他渠道。

### 实现确认(逐层)

| 层 | 文件 | 实现 |
|----|------|------|
| proto | `api/channel/v1/channel.proto:107,123` | `excluded_channel_ids` / `excluded_account_ids` |
| service | `app/channel/internal/service/channel.go:113,352` | DTO → excluded map → `SelectChannelExcluding` |
| biz | `app/channel/internal/biz/channel.go:486,648` | `SelectChannelExcluding` / `SelectSubscriptionAccountExcluding` |
| relay biz | `internal/biz/relay.go:41,78,145` | `RoutingSourceIdentity{Kind,ID}` + `SelectFallbackRoutingSource` |
| retry | `internal/biz/retry.go:167,316` | RetryExecutor 走 `excluded map[RoutingSourceIdentity]bool`,按 Kind 拆分 |
| WS | `internal/server/openai_ws_forwarder.go:551,577,718` | 失败源以 `RoutingSourceIdentityForChannel` 标记并传入 `maybeFailoverChannel` |
| data | `internal/data/adapters.go:131,214` / `data.go:99,156` | 跨服务 gRPC 透传 excluded 集合 |

### namespace 隔离(§9.2 验收项)

`SelectFallbackRoutingSource`(`relay.go:1188`)按 `source.Kind` 分别构建
`excludedChannels`(仅 `UpstreamRouteChannel`)和 `failedAccounts`(仅
`UpstreamRouteSubscription`),**同数值 ID 的渠道与订阅账号互不干扰**。WS 连接池
`openAIWSPoolKey` 亦以 `kind:id` 命名空间隔离。

### 回归测试(commit `385c7a5`)

| 测试 | 覆盖点 |
|------|--------|
| `TestRetryExecutor_ExecuteWithCandidates_APIKeyChannelSameTierFallback` | 同层兄弟渠道在首个失败后可达 |
| `TestRetryExecutor_ExecuteWithCandidates_APIKeyChannelExhaustsTierThenLower` | 全层耗尽才降层 |
| `TestSelectFallbackRoutingSourceCrossesSourceNamespaces`(既有,relay_test.go:947) | 同 ID=7 的渠道失败可回退到订阅账号,反之亦然 |
| `TestRetryExecutor_ExecuteWithCandidates_NamespaceLockProhibitsCrossSource`(既有) | namespace-lock 阻止跨源 |

---

## P1.2 — 并发 active 唯一约束(H10):✅ 完成

### 问题

`user_subscriptions` 表并发创建可能在 pre-check 与 INSERT 之间产生竞态,导致同一用户
出现多条 active 订阅。

### 实现确认

| 层 | 实现 |
|----|------|
| MySQL 迁移 | 生成列 `active_user_id`(status='active' 时 = user_id,否则 NULL)+ UNIQUE 索引(迁移 059) |
| PostgreSQL / SQLite | 部分唯一索引 `WHERE status = 'active'`(迁移 001/003) |
| data | `domain/subscription/data/subscription_repo.go:266,284` — duplicate-key 映射为 `biz.ErrSubscriptionAlreadyAssigned` |
| 错误识别 | `isDuplicateKeyErr`(`subscription_repo.go:759`)覆盖 MySQL `uniq_user_subs_active_user_id`、SQLite `UNIQUE constraint`、PG duplicate-key |
| biz | `domain/subscription/biz/subscription_usecase.go:63,116,205` 返回 `ErrSubscriptionAlreadyAssigned` |

### 回归测试(commit `385c7a5`)

**data 层(真实 SQLite 部分唯一索引)**:
| 测试 | 覆盖点 |
|------|--------|
| `H10UniqueIndexRejectsSecondActive` | 第二条 active INSERT 触发唯一约束,映射 `ErrSubscriptionAlreadyAssigned` |
| `H10UniqueIndexAllowsMultipleNonActive` | expired/revoked 不受约束 |
| `H10UniqueIndexDifferentUsers` | 不同用户各自可有 active |

**biz 层(竞态窗口模拟)**:
| 测试 | 覆盖点 |
|------|--------|
| `Assign_PropagatesDuplicateKeyFromDB` | 双 goroutine 竞态越过 pre-check,DB 拒绝 |
| `AssignOrExtend_PropagatesDuplicateKeyFromDB` | AssignOrExtend 路径同竞态 |

---

## P1.3 — 订阅粘性收益验证(#7 第一步):✅ 结论落档

### 问题

需依据 `micro_one_api_relay_subscription_sticky_total` 复用率指标评估上游 prompt
cache 收益,决定是否将 Responses/WS 入口统一进 schedulability-aware 账号粘性。

### 生产指标快照(2026-08-06)

| 指标 | 值 | 说明 |
|------|----|------|
| `micro_one_api_routing_selection_total`(按 sticky_hit) | **445**,仅 `{}` 单桶(无 `sticky_hit="true"` series) | 全部选择均非粘性命中 |
| `micro_one_api_routing_selection_planned_total`(按 source_kind) | subscription **432** + channel **14** | 97% 流量为单账号订阅 |
| `micro_one_api_relay_subscription_failover_total` | **2** | 极少触发回退 |
| `micro_one_api_relay_subscription_adaptor_requests_total` | **432** | 与订阅选择数一致 |
| `micro_one_api_relay_subscription_sticky_total` | **0 series(不存在)** | 计数器注册于代码(`platform/metrics/subscription.go:55`,在 `metrics.go:255` 注册)但因从未被 Inc(无客户端发送 `session_hash`),Prometheus 不暴露零值计数器 |

### 粘性链路确认

| 配置/组件 | 状态 |
|-----------|------|
| `session_sticky.enabled` | ✅ 容器内已设为 `true` |
| Redis-backed `wsSticky` store | ✅ 已初始化,`SetSessionAccountStore` 已 wire |
| `trySubscriptionSticky`(`relay.go:709`) | ✅ 代码完整:LookupSessionChannel → GetSubscriptionAccountByID → 校验 → RefreshTTL |
| `session_hash` 提取(`http_raw_helpers.go:117`) | ✅ 从 body 的 `session_hash`/`sessionHash` 提取 |
| Responses 调度器 `response_scheduler.go` | ✅ lookupWSStickySessionRoute → BindSessionRoute |

### 结论:维持现状(待数据)

**粘性子系统已就绪但未激活。** 根因是当前生产流量为单账号(zhipu/kimi)订阅,无多账号
fan-out 场景,客户端不发送 `session_hash`,因此:

- `RelaySubscriptionStickyTotal` 永远为 0 series,无法计算复用率
  `hit / (hit + rebind + miss)`
- prompt cache 收益无法评估

**决策**:维持现状,推迟 #7 第二步(Responses/WS 入口统一)。触发条件:
1. 配置多账号订阅池(同平台多账号),或
2. 客户端开始发送 `session_hash`(如 Codex CLI 带 `session_hash` header)

届时采集 `micro_one_api_relay_subscription_sticky_total` 按 `result` 分桶,计算
`hit / (hit + rebind + miss)`,若正向收益则推进入口统一。

### 不需要代码变更

粘性代码、指标、配置均已就绪,P1.3 仅需等待流量模式变化,无需当前 commit。

---

## 质量门禁

| 门禁 | 状态 |
|------|------|
| `make test-unit` | ✅ 全绿(cached) |
| `make test-sqlite` | ⚠️ 本地 sandbox 无法绑定 httptest 端口;P1 相关包单测全绿 |
| `./scripts/check-architecture.sh` | ✅ exit 0 |
| `make api-check`(`buf generate`) | ✅ 无 diff |
| P1 专项:`go test -run "APIKeyChannel\|H10UniqueIndex\|PropagatesDuplicateKey\|SelectFallbackRoutingSource\|NamespaceLock"` | ✅ 三包全绿 |

## v0.16 定义完成对照

- [x] 同优先级回退有确定性回归测试(P1.1)
- [x] 并发 active 有确定性回归测试(P1.2)
- [x] 粘性收益结论以指标数据落档(P1.3,本文档)
- [ ] P0 检查清单全部打勾(observe → charge 对账等,属 P0 范畴,非本文档)
- [ ] 发布说明(v0.16 release note,待 P0 闭环后撰写)
