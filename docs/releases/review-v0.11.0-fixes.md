# v0.11.0 Review 修复记录 — CRITICAL / HIGH

- **日期**:2026-07-29
- **对应报告**:[review-v0.11.0.md](review-v0.11.0.md)
- **验证**:`go build ./...`、`go test ./internal/... ./app/...` 全绿;web `tsc --noEmit` + `vite build` 通过;alerts.yml YAML 校验通过(环境无 promtool,建议 CI 补 `promtool check rules`)。

---

## C1 — RoutingOpsPage `sources` null 解引用白屏

**文件**:`web/src/pages/admin/RoutingOpsPage.tsx`

- 接口类型 `sources: RoutingOpsSource[] | null`(Go nil slice → JSON `null` 的真实契约)
- 渲染前归一化:`const sources = data.sources ?? []`,`:342`/`:362` 改用归一化后的局部变量(对齐 sub2api `res.items || []` 模式)

## H1 — WS 连接池跨命名空间串号

**文件**:`internal/server/openai_ws_pool.go`、`internal/server/openai_ws_forwarder.go`、`internal/server/openai_ws_pool_test.go`

- 池键由裸 `int64 channelID` 改为命名空间字符串 `"channel:<id>" / "subscription:<id>"`(经 `openAIWSPoolKey()` 从 `RoutingSourceIdentity` 派生),channel #5 与 subscription account #5 不再共享池桶
- 复用前校验连接指纹(`wsURL` + `Authorization` 头):凭证/URL 轮换后旧连接直接关闭驱逐,不会串用旧凭证(对齐 sub2api `openAIWSAcquireRequest` 携带完整目标上下文的设计)
- 新增回归测试:`TestConnPoolIsolatesNamespaces`(跨命名空间隔离)、`TestConnPoolRejectsRotatedCredential`(凭证轮换不复活旧连接)

## H2 — 故障转移提前终止

**文件**:`api/channel/v1/channel.proto`、`app/channel/internal/biz/channel.go`、`app/channel/internal/service/channel.go`、`internal/data/adapters.go`、`internal/data/data.go`、`internal/biz/relay.go` + 7 处测试 fake

- `SelectChannelRequest` 新增 `repeated int64 excluded_channel_ids = 4`(已 `buf generate` 重新生成)
- channel usecase 新增 `SelectChannelExcluding`:按候选逐个过滤已失败渠道(而非 `excludeFirstPriority` 整层跳过),任何优先级的健康渠道都可达;不做 catch-all 扩展(failover 语义不变)
- relay `SelectFallbackRoutingSource` 把请求级失败集作为**过滤器**传入选择,替代原来的"选择后置空"——sub2api `SelectAccountForModelWithExclusions` 同款模式
- 新增回归测试:
  - `TestChannelUsecase_SelectChannelExcluding_WalksLowerTiers`(10/5 层失败仍能落到 1 层;全部排除时报错而非挂起)
  - `TestChannelUsecase_SelectChannelExcluding_KeepsTierSiblings`(同层健康兄弟节点不受牵连)
  - `TestSelectFallbackRoutingSource_PassesExcludedChannelsToSelection`(排除集确实传入选择层)

## H3 — UpstreamCostMissing 告警永不触发

**文件**:`deploy/prometheus/alerts/alerts.yml`

- 表达式改为 `(A - B) or A`:某 provider_family 完全没有 priced 序列时,`or` 回退为全部成功流量(即 100% 未定价),不再被向量匹配静默丢弃

## H4 — CacheCreationShadowCostDrift 告警稳态 observe 下永不触发

**文件**:`deploy/prometheus/alerts/alerts.yml`

- 表达式改为 `(observe - charge) or observe`:稳态 observe 模式下 charge 侧为空向量时,漂移量回退为完整 observe 影子成本

---

## 遗留(未修,见报告备查清单)

- M1-M6、L1-L7:见 [review-v0.11.0.md](review-v0.11.0.md) §一
- sub2api 采纳项 #2(候选顺序预计算,重试按序推进)、#6(边缘桶归一)、#9(分桶成本持久化,charge 切换前必须)、#12(负载感知接线):roadmap
