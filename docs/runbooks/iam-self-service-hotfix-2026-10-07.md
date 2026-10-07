# IAM 用户侧分组、订阅购买与权限页面修复记录

> 2026-10-07 · 正式版本：[v0.34.9](../releases/release-v0.34.9.md)（v0.34.8 候选发布中止，业务热修复保持）

## 故障与修复边界

| 入口 | 原故障 | 修复 | 运行时更新 |
| --- | --- | --- | --- |
| `GET /api/v1/routing-groups/available?page_size=200&page_token=` | 普通用户收到「无权使用此分组」 | 验证用户路由事实；内部目录、报价和模型读取使用专用服务能力，再按 `AccessSources` 过滤用户可用组 | admin、channel、billing |
| 钱包购买／续订调用的 `PurchaseSubscription` RPC | IAM 覆盖拦截返回 `owner execution point incomplete or caller denied` | 纳入 billing 已完成执行点；扣款前由 owner 验证本人会话与账户归属 | billing |
| 令牌创建使用的 `GetRoutingCapabilities` RPC | 已分类的能力探测未纳入覆盖列表 | 纳入已完成执行点；仍要求已验证、允许调用该方法的专用服务身份 | billing |
| `POST /api/v1/subscriptions/purchase/payment` | 创建支付订单前收到相同的覆盖拦截错误 | 将该入口实际调用的 `CreatePaymentOrder` 纳入覆盖列表；保留订单用例原有的本人校验、价格快照和幂等行为 | billing |
| `/admin/iam/*` | 顶部重复展示 IAM 子导航；授权轮询期间按钮／局部内容消失再出现 | 删除重复导航；展示使用有效摘要，动作在刷新期间禁用，真正授权变化仍清理旧页面与受保护缓存 | 独立挂载的前端 |

用户路由事实与写入请求继续携带已验证的 operator。内部参考读取仅在路由资格编排中标记，RPC 适配器复制 metadata 后移除 operator/reason；普通后台目录、分组详情和价格读取仍检查管理权限。新增服务能力限于 `admin` 的分组目录与报价读取，不扩展写权限，不修改用户角色、IAM grant 或数据。

钱包购买和支付创建是不同链路。`PurchaseSubscription` 的覆盖测试不能证明 `CreatePaymentOrder` 可达，HTTP 返回 200 也不能证明业务成功：必须同时断言响应 `success=true`、支付信息和订单存储结果。

## 回归验证

```bash
# 用户可用组与管理权限边界
go test ./app/admin/internal/server -run 'TestRoutingAvailableIAMMember|TestRoutingManagementKeepsOperatorPermissionChecks' -count=1

# RPC 覆盖及扣款前的本人校验
go test ./app/billing/internal/server ./app/billing/internal/service -run 'TestPurchaseSubscriptionReachesIAMOwner|TestIAMSubscriptionPurchaseChecksSelfBeforeCharge' -count=1

# 真实 HTTP → gRPC → identity 会话 → 套餐快照 → 订单存储
go test ./internal/integration -run TestIAMSubscriptionPaymentThroughRealOwners -count=1

# IAM 页面与授权生命周期
cd web
npm test -- src/pages/admin/IAMPage.test.tsx src/lib/authorization.test.tsx src/components/AdminRoute.test.tsx src/pages/TokensPage.test.tsx
```

完整支付入口集成测试在修复前复现原样错误，修复后通过，并验证：

- 普通用户可以为本人创建支付订单；请求中伪造的 `user_id` 不决定买方。
- 订单金额由 billing 的套餐快照决定，客户端 `money_cents=1` 不造成少付。
- 同一 Idempotency-Key 只创建一笔订单；套餐下架后仍可重放旧请求，新请求拒绝。
- 跨用户、无用户会话、错误服务 caller 的直接 RPC 请求仍被拒绝。

该集成测试使用 SQLite 实际仓储和真实服务边界，外部支付 provider 使用 mock，不代表真实支付宝交易、支付回调或权益发放的生产验收。

全仓 `make verify` 已通过，前端 63 个测试文件／322 个测试通过，IAM 入口契约审阅 829 项。真实 owner 浏览器矩阵完整新进程重跑 10 项通过、0 跳过；首次全项通过后的 Chrome worker 退出超时单独记录，不计为门禁成功。结果见 [发布校验证据](evidence/patch-v0.34.8-validation-2026-10-07.json)。

## 生产更新与证据

所有镜像在 Apple Silicon 开发机交叉构建为 `linux/amd64`，上传归档后核对摘要；远端未构建镜像。现网 Compose 使用固定版本标签，每次切换均明确更新对应镜像引用，并使用 `--no-deps --no-build`。回滚备份来自实际运行容器的 image ID，而非假定 `latest` 等于当前镜像。

| 时间（CST） | 更新 | 结果 | 证据 |
| --- | --- | --- | --- |
| 20:29–20:30 | channel、billing、admin 与 `/opt/web/dist` | 三服务 `/healthz` 通过、restart 0；实际返回首页和 IAM JS 摘要匹配本机构建 | [首次部署证据](evidence/routing-subscription-iam-hotfix-deploy-2026-10-07.json) |
| 20:40–20:41 | 单独补更新 billing | `CreatePaymentOrder` 覆盖拒绝解除；healthz 正常、restart 0 | [支付入口补修证据](evidence/subscription-payment-iam-hotfix-deploy-2026-10-07.json) |

首次更新覆盖了钱包 RPC 与用户可用组读取，随后用户明确报告支付入口仍失败；完整支付测试确认 `CreatePaymentOrder` 是独立遗漏，第二次更新才包含该修复。最终 billing image ID 为 `sha256:395ffdf6b79254dbaa94952f226268d31cca07408029e4d4b2e17c5881f628ac`。

支付入口现网复测使用专用 admin 服务凭证、无效用户会话及空请求：部署前返回覆盖拦截的 PermissionDenied，部署后进入 owner 会话校验并返回预期的 Unauthenticated。两次请求均不能创建订单；没有创建真实生产支付订单或发起扣款。页面 HTTP 200 仅证明 SPA 可访问，匿名可用组请求的 401 证明匿名保护保留，不能据此声称已完成带登录会话的生产购买验收。

## 升级与回滚

受影响的运行时为 `admin-api`、`channel-service`、`billing-service` 和宿主机挂载的 `web/dist`。无 API/proto 变更、数据库迁移或新增配置项；保留现有服务专用凭证、IAM 模式与订阅开关。

未部署修复的环境按 [v0.34.9 升级步骤](../releases/release-v0.34.9.md#升级步骤) 操作。当前生产已经运行修复等价镜像，正式 tag 发布不要求再重启容器。

- 首次回滚标签：`rollback-20261007-202844`；Compose 备份见首次证据；前端备份 `/opt/web/dist.bak.hotfix-20261007-202844`。
- 支付补修回滚标签：`rollback-payment-hotfix-20261007-203939`；Compose 备份见支付入口证据。该备份中的 billing 尚缺 `CreatePaymentOrder` 覆盖，回退会重新出现支付创建故障。
- 回滚只恢复 IAM 兼容镜像／前端；不切回 legacy、不改 IAM 分配、不删除数据库列。恢复绑定目录内容时保留 `/opt/web/dist` 目录本身，确保容器 bind mount 仍指向该目录。


## Release 冒烟同步

v0.34.8 的通用 Playwright 冒烟仍要求刷新期间按钮数量为 0，和「保留控件、暂时禁用」的新行为矛盾，因此桌面／手机两项失败并阻止镜像与 GitHub Release 发布。v0.34.9 同步该断言，保留草稿、缓存、刷新后恢复和真实撤销清理验证；不改变业务代码或已部署镜像，不跳过门禁。原 tag 保留，见 [v0.34.9 发布说明](../releases/release-v0.34.9.md)。
