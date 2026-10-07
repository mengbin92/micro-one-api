# Micro-One-API v0.34.8 发布：用户可用分组、订阅支付与权限页面修复

> 2026-10-07 · 上一版：[v0.34.7](./release-v0.34.7.md)（2026-10-07）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.8)

v0.34.8 是 v0.34.7 之后的 **PATCH 用户侧 IAM 与权限页面可靠性修复版本**：恢复普通用户的可用分组读取，补齐钱包购买／续订和支付订单创建的 owner 执行点覆盖，移除权限管理页面重复导航，并保留授权轮询期间的界面与有效读缓存。

**受影响的运行时为 `admin-api`、`channel-service`、`billing-service` 与独立挂载的前端 `web/dist`**。无公共 API/proto 变更、数据库迁移或新增配置项。当前生产已部署本版修复等价镜像和前端，正式 tag 发布不重复重启容器。

## 修复内容

### 1. 普通用户可用分组读取恢复

**根因**：`/tokens` 调用 `GET /api/v1/routing-groups/available?page_size=200&page_token=` 时，内部组目录、报价和模型读取复用了后台管理 RPC 的 operator 权限路径。普通用户没有管理目录或价格的 grant，下游 403 被统一显示为「无权使用此分组」。

**修复**：identity 的用户路由事实读取继续携带已验证会话；内部参考读取在路由资格编排中显式标记，由 RPC 适配器复制 metadata 后移除 operator/reason，使用专用服务能力读取组目录、报价和模型。`AccessSources` 仍过滤用户实际可用组，普通后台管理读取仍保留 operator 判权。admin 的新增系统能力仅限分组目录与报价读取，不扩大写权限。

**影响服务**：`admin-api`、`channel-service`、`billing-service`。

### 2. 钱包续订与能力探测通过 IAM 覆盖检查

**根因**：`PurchaseSubscription` 与 `GetRoutingCapabilities` 已有固定 caller 分类，但未进入 billing 已完成执行点列表。IAM 覆盖拦截在业务用例之前返回 `owner execution point incomplete or caller denied`。

**修复**：将两个方法纳入覆盖列表；钱包购买／续订由 billing 在读取、扣减余额之前独立验证本人会话与账户归属。专用服务凭证本身不能替代用户会话，跨用户扣款仍被拒绝。

**影响服务**：`billing-service`。

### 3. 支付购买入口独立覆盖，保留服务端价格与幂等性

**根因**：`POST /api/v1/subscriptions/purchase/payment` 实际调用 `CreatePaymentOrder`，并不经过钱包购买 RPC；该方法同样遗漏在 billing 覆盖列表中。首次修复的钱包测试没有覆盖这条独立链路，支付入口仍返回相同拦截错误。

**修复**：补齐 `CreatePaymentOrder` 覆盖，保留订单用例原有的本人校验、套餐价格快照和 Idempotency-Key 语义。新增真实 admin HTTP → billing gRPC → identity 会话 → 套餐快照 → SQLite 订单仓储集成测试，同时断言 HTTP 状态、`success=true`、支付信息和订单结果；外部支付 provider 使用 mock。

**影响服务**：`billing-service`。回归覆盖服务器定价、同键单笔订单、套餐下架后的旧请求重放／新请求拒绝，以及跨用户、无会话和错误 caller 拒绝。

### 4. 权限管理页面移除重复导航，轮询保留展示

**根因**：IAM 子页面额外渲染全部子导航，与侧边栏重复；页面用刷新期间暂时不可执行的授权快照控制按钮和局部内容的存在，导致每轮授权轮询中内容消失再出现。

**修复**：删除子页面重复导航；展示使用仍有效的授权摘要，动作使用共享的刷新状态门禁，刷新期间保留控件并禁用执行。真实权限、会话或凭证边界变化继续清理旧数据与页面。IAM 入口矩阵同步记录执行点摘要与新增回归。授权手动恢复测试改用真实用户事件，避免查询完成早于 React 通知的测试时序竞态。

**影响服务**：独立挂载的前端 `web/dist`。

## 兼容性说明

- 无公共 API/proto 变更、数据库迁移或新增运行时配置。
- 保留现有专用服务凭证、IAM 模式与订阅开关；不修改用户角色、IAM grant 或数据库数据。
- 已验证的用户事实、可用组过滤和 owner 本人校验继续生效；界面展示摘要不授予接口权限。
- `admin-api` 挂载宿主机 `/opt/web/dist`；仅更新 admin 镜像不会更新前端，必须单独同步静态产物。

## 升级步骤

1. 备份当前三个服务实际运行镜像、Compose 文件与 `/opt/web/dist`，记录 image ID 和前端摘要。
2. 获取 `v0.34.8` 源码；在本机按各服务 Dockerfile 构建 `linux/amd64` 镜像，或使用发布流水线产出的对应平台镜像。服务器资源受限，禁止远端构建。
3. 上传并核对镜像归档摘要，加载后明确更新 Compose 中 channel、billing、admin 的固定镜像标签；按该顺序执行 `docker compose up -d --no-deps --no-build --pull never <service>`。保留其他服务配置与现有 IAM 凭证。
4. 本机执行 `cd web && npm run build`，单独上传 `dist` 到现有 `/opt/web/dist` 绑定目录并保留回滚副本；无需为静态目录更新额外重启 admin。
5. 验证三个 `/healthz`、实际 image ID、restart、前端摘要及用户侧入口。带登录会话的购买验收应在受控测试套餐下进行；健康检查不代表支付／回调／权益发放已完成。

当前生产已于 2026-10-07 20:29 更新三个服务与前端，20:40 补更新支付创建修复；正式 tag 无需重复部署。详细升级、回滚标签和验证范围见 [IAM 用户侧修复记录](../runbooks/iam-self-service-hotfix-2026-10-07.md)。

## 验证

- 用户可用分组 HTTP 回归在修复前复现「无权使用此分组」，修复后通过；不可用组不返回，后台目录、详情与价格读取仍拒绝缺少管理权限的操作者。
- 钱包扣款前本人校验、三个 billing RPC 覆盖测试通过；伪造目标用户不会扣减余额或写账本。
- `TestIAMSubscriptionPaymentThroughRealOwners` 在修复前通过完整 HTTP 链路复现原样拦截错误，修复后验证创建、幂等重放、服务端定价、套餐下架与拒绝边界；全部 `TestIAM` 跨服务集成测试通过。
- IAM 页面、授权生命周期、路由和令牌页面的 37 个前端测试及 ESLint 通过；前端生产构建和六项体积预算通过。
- 全仓 `make verify` 通过：格式、单元／race、分层、迁移、829 项 IAM 入口契约及浏览器验收结果检查、生成 API 类型、前端 lint／322 个测试／生产构建和六项体积预算；`git diff --check` 与 Markdown 本地链接检查通过。
- 真实 owner IAM 浏览器矩阵完整重跑通过：10 项实际通过、0 跳过、无全局错误。首次执行 10 项测试通过但本机 Chrome worker 退出超时，未跳过或改弱门禁；新进程完整重跑成功，见 [发布校验证据](../runbooks/evidence/patch-v0.34.8-validation-2026-10-07.json)。
- [首次部署证据](../runbooks/evidence/routing-subscription-iam-hotfix-deploy-2026-10-07.json)：三服务 healthz 通过、restart 0、前端实际返回摘要匹配；页面 HTTP 200 和匿名分组请求 401 仅为路由／保护检查。
- [支付补修证据](../runbooks/evidence/subscription-payment-iam-hotfix-deploy-2026-10-07.json)：无效用户会话／空请求的线上 `CreatePaymentOrder` 探针由覆盖拒绝的 PermissionDenied 变为预期的 Unauthenticated，证明进入 owner 会话校验。没有创建真实生产支付订单、发起扣款或执行真实支付回调验收。

## 完整变更日志

- `fix(iam): restore routing availability and subscription purchases`
- `docs(ops): record IAM self-service hotfix rollout`
- `docs(release): v0.34.8`
