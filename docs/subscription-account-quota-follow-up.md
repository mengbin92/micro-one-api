# 上游账号额度后续待办

本文档记录第二阶段完成后仍未纳入本次提交的后续工作。

## 已完成基线

- 上游订阅账号已支持本地总额、24h、7d USD 限额和 `rate_multiplier`。
- channel-service 选路会跳过本地额度耗尽的账号。
- relay-gateway 成功提交计费后，会按 billing `committed_amount` 折算 USD 并回写账号本地用量。
- admin 订阅账号页面可查看、编辑、重置本地用量。

## 待办

1. **账本快照账号倍率**
   - 现状：billing ledger 已记录 `subscription_account_id`、`subscription_cost`、`balance_cost`。
   - 缺口：尚未把账号级 `rate_multiplier` 或本地额度扣减快照写入 ledger。
   - 价值：后续做精细对账时，可复原当时使用的账号倍率和计费口径。

2. **分布式并发和运行时状态**
   - 现状：账号并发限制、runtime block 当前以 relay 进程内存为主。
   - 缺口：多副本部署下，同一账号的并发槽和短 TTL blocker 不能跨实例共享。
   - 建议：使用 Redis 计数器和带 TTL 的 block key，保持失败降级为本地内存或正常选路。

3. **更细的上游窗口策略**
   - 现状：本地额度支持总额、滚动 24h、滚动 7d；Codex 5h/7d 快照独立记录。
   - 缺口：尚未支持 sub2api 风格的 5h 成本窗口、RPM、会话窗口等可配置策略。
   - 建议：先确认真实运营需求，再新增字段，避免把低频限制写进调度热路径。

4. **本地额度幂等增强**
   - 现状：账号额度回写发生在 billing commit 成功之后，billing 自身负责 reservation 幂等。
   - 缺口：relay 重试同一次成功 commit 时，账号本地额度没有独立 dedupe key。
   - 建议：如出现重复回写风险，新增 `subscription_account_quota_events`，以 reservation id + cost source 幂等累计。

5. **管理端批量能力**
   - 现状：单账号可编辑额度和重置用量。
   - 缺口：暂无批量设置额度、批量重置、按平台/分组快速套模板。
   - 建议：等账号规模上来后，再在 admin 页面增加批量操作。

## 验证建议

- 增加 e2e：创建两个同优先级订阅账号，一个日额度耗尽，一个未耗尽，确认请求只落到可用账号。
- 增加 e2e：发起订阅账号请求，确认 billing ledger 和 `subscription_accounts.quota_*_used_usd` 同步增长。
- 多副本场景如引入 Redis 状态，需要补并发竞争测试和 Redis 失效降级测试。
