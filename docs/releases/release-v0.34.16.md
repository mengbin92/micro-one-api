# Micro-One-API v0.34.16 发布：订阅价格契约、写前置条件与模型可用性修复

> 2026-10-11 · 上一版：[v0.34.15](./release-v0.34.15.md)（2026-10-09）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.16)

v0.34.16 是 v0.34.15 之后的 **PATCH 修复版本**，包含 10 个提交（6 fix + 1 test + 3 merge）。统一订阅配额窗口与 CNY 价格契约（修复钱包实收金额 10,000 倍少扣的资金问题），保留管理台写前置条件并回填零版本号，按来源状态同步模型可用性，修复中文操作原因被 gRPC 元数据拒绝，并交付一批跨服务 review 修复。

**无 API/proto 破坏性变更**。涉及数据库迁移 096（订阅契约）与 124/125（零版本号回填），三者在生产均已随业务热修复应用；Compose 为 billing/admin 增加 `RECHARGE_AMOUNT_MULTIPLIER` 环境变量。受影响运行时服务为 `billing-service`、`admin-api`、`channel-service`、`relay-gateway`、`identity-service` 及宿主机挂载的前端。模型可用性、写前置条件两批修复及对应前端已分别于 2026-10-09／2026-10-10 热部署到生产，本版主要新增订阅价格契约与跨服务 review 修复的运行时更新。

## 修复内容

### 1. 订阅配额窗口与 CNY 价格契约统一（资金相关）

**根因**：仪表盘“今日”消费使用 UTC 自然日，而订阅配额按订阅生效时间锚定的 24 小时窗口计量，两个日数据不一致。套餐价格单位含糊：支付宝对 `PriceQuota=100` 的套餐收取 100 元，钱包却只扣 100 单位（$0.01）——每次钱包购买少扣 10,000 倍。支付请求还可覆盖套餐价格与时长，目录编辑会改变在途订单的应付金额。

**修复**：

- 钱包扣费将 CNY 套餐价（或升级差价）经服务端充值倍率换算（默认 10，`domain/subscription/biz/price.go`，`RECHARGE_AMOUNT_MULTIPLIER`），`PricePaid` 保留 CNY 基准；新订单在套餐快照中冻结价格、倍率与时长，支付请求不能再覆盖价格／时长；连续升级只收当前差价；拒绝负价格溢出的免费升级。充值按实付 CNY 分精确入账，部分退款按退款比例缩短时长。
- 仪表盘：有有效订阅时展示当前订阅窗口的已结算用量、窗口边界与冻结配额；无订阅时保留自然日统计并标注“UTC calendar day”。金额保留最多 8 位小数，非有限值显示为破折号而非 $0.00。购买确认弹窗重读目录，价格变化更新报价，过期钱包报价或已下架套餐阻止提交。
- 管理端：`GET /api/v1/admin/subscriptions/progress` 提供按用户进度查询；RBAC 事实矩阵复审（832 → 833 行）。
- 部署：billing/admin 的 Compose 环境新增 `RECHARGE_AMOUNT_MULTIPLIER`（identity 已有），lite/postgres 变体对齐。

**影响服务**：`billing-service`、`admin-api`、使用共享订阅库的服务及独立前端。需要迁移 `096_add_subscription_contracts`（生产已应用）。

### 2. 管理台写前置条件保留与零版本号回填

**根因**：历史订阅账号与配置可能保留 revision 0，而 IAM 写入要求正数期望版本；控制台适配器还会丢失版本号或选中过期缓存，导致合法操作被拒绝、后台刷新掩盖编辑冲突。

**修复**：通过属主限定迁移 124、125 回填零版本号；批量配额写入转发逐账号 CAS 与原因，暴露策略与模型路由版本，保留编辑起始版本并读取当前属主缓存；缺失与过期版本依旧拒绝。

**影响服务**：`admin-api` 及前端；迁移 124、125 已在属主 MySQL 库应用，amd64 admin-api 与前端已热部署。

### 3. 模型可用性随来源状态同步

**根因**：管理端注册表把已禁用渠道／订阅账号的启用映射计入来源数，并直接展示模型开关作为可用性；路由层早已检查来源状态，导致运营禁用上游后模型页仍显示启用、来源计数虚高。

**修复**：有效可用性按“启用且存活”的来源派生，保留模型手动开关与按权限过滤的供应商计数；同一账号跨分组只计一次；新增 `configured_status` 支持单行操作；显式 `status=0` 的列表／导出筛选正确匹配有效禁用模型；来源变更后刷新模型视图缓存；模型用量记录改为仅按模型 ID 查询，避免每请求聚合。

**影响服务**：`channel-service`、`admin-api` 及前端；已于 2026-10-09 热部署并完成只读验收（详见[生产更新记录](../runbooks/model-source-availability-production-2026-10-09.md)）。

### 4. 跨服务 review 修复

**根因**：选择阶段的计数器与无序槽位报告在真实上游执行前泄露负载或绕过容量；并发 Token 扣费、邮箱变更后会话、余额算术、退款校验、事务 panic 处理与订阅写前置条件存在边界问题；重定向可能泄露凭据、OAuth 错误信息外泄、JSON 数字精度、流取消、日志写入关闭竞争与 SSE 终止均有缺陷。

**修复**：以幂等可续期租约跟踪尝试，原子执行渠道容量上限，WebSocket 账号逐轮准入，本地拒绝不计入上游健康统计；保留并发扣费并在邮箱变更后失效会话；加固余额、退款与事务边界；阻止重定向凭据泄露与 OAuth 错误披露，保留 JSON 数字，修复流取消与关闭竞争；全部附回归测试。

**影响服务**：`relay-gateway`、`identity-service`、`channel-service`、`billing-service`、`log-service` 及共享库使用者。

### 5. Unicode 渠道操作原因与 CI 修复

**根因**：中文操作原因以文本 gRPC 元数据发送，而文本元数据只接受可打印 ASCII，渠道操作在到达属主前被拒；IAM 路由白名单同时拒绝控制台启用／禁用渠道使用的 POST 方法。此前修复改动三个已复审文件但未刷新 RBAC 摘要，导致契约测试失败；前端新增 CAS 回归未纳入关键覆盖率清单，分支覆盖率跌破阈值。

**修复**：Unicode 与控制字符经二进制元数据传输，滚动升级期间可打印 ASCII 保留文本键；接收端两种格式均接受并拒绝歧义原因；内部路由引用读取剥离两种格式；渠道状态别名与 GET/POST 处理对齐。仅刷新三个已复审文件的 RBAC 摘要；对定时安全扫描中已确认的历史 SHA256 校验和误报按精确指纹豁免，未来发现仍照常扫描；将渠道前置条件套件纳入关键覆盖率（admin-write.ts 分支 94.11%）。

**影响服务**：`admin-api`、`channel-service`；CI／测试配置，不改变生产行为。

## 兼容性说明

- HTTP/RPC 与 proto 契约兼容；订阅购买的价格、时长改由服务端快照决定，依赖客户端传价的旧调用不再生效。更严格的写前置条件、支付授权与输入校验会拒绝此前错误放行的请求。
- 需要迁移 `096_add_subscription_contracts` 及 124、125；三者在生产均已应用。未应用 096 的环境必须先迁移再上线本版 billing/admin。
- billing/admin 需配置 `RECHARGE_AMOUNT_MULTIPLIER`（默认 10）；取值变更会直接改变钱包扣费金额，升级时不要顺带调整。
- 既有 JWT／Token 哈希密钥、渠道 AES 密钥与 Redis 要求同 v0.34.15，保持不变。
- 前端继续由 `/opt/web/dist` 独立挂载；仅替换管理服务镜像不会更新前端。

## 升级步骤

1. 获取 v0.34.16，备份数据库、当前镜像、Compose、密钥配置与前端。
2. 确认迁移 096、124、125 已应用；确认 billing/admin 的 `RECHARGE_AMOUNT_MULTIPLIER` 取值（生产保持默认 10）。
3. 本地交叉构建 linux/amd64，按 billing、channel、identity、admin、relay 顺序更新受影响服务；禁止在生产服务器构建。模型可用性与写前置条件修复已在生产运行，等价环境可跳过对应服务。
4. 独立执行 `cd web && npm run build`，备份并发布宿主机 dist，核对文件摘要及公网入口。
5. 核对镜像摘要、health、重启次数与启动日志；重点观察订阅购买报价、钱包扣费金额与仪表盘订阅窗口数据。
6. 回滚使用保存的镜像、Compose 和前端目录；回退旧代码会恢复钱包价格契约缺陷，涉及资金，回滚前需评估在途订单。

## 验证

- 全量 Go 测试（不含 live-stack e2e）、race 检测、集成套件、MySQL/Postgres 迁移冒烟、RBAC 契约、架构／提交／迁移门禁通过。
- 前端 424 项 vitest 用例通过，关键资金分支覆盖率 100%，ESLint 与 tsc 干净；158 项关键测试覆盖渠道 CAS 回归。
- 模型可用性修复生产只读验收通过：两个 step 模型禁用且来源为 0，glm-5.3 保持启用（1 渠道、0 订阅）。
- 未运行真实供应商、真实支付或邮件发送；Release 流水线另行执行路由／Compose E2E、浏览器门禁、多架构镜像及 GitHub Release 发布，正式制品状态以其实际结果为准。

## 完整变更日志

- `fix: align subscription quota windows and unify CNY price contract`
- `fix: resolve cross-service review findings`
- `test(web): include channel CAS regressions in critical coverage`
- `fix(admin): preserve owner write preconditions across console operations`
- `fix(channel): synchronize model availability with source status`
- `fix(ci): refresh RBAC digests and exclude historical checksum false positives`
- `fix(admin): support Unicode channel action reasons`
- `docs(release): v0.34.16`
