# Micro-One-API v0.34.1 发布：订阅用量、用户字段与公开模型价格修复

> 2026-10-06 · 上一版：[v0.34.0](./release-v0.34.0.md)（2026-10-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.1)

v0.34.1 是 v0.34.0 之后的 **PATCH 修复版本**：修复专属 relay 身份查询订阅用量时的 502、IAM 用户管理列表的余额/已用字段缺失和空邮箱误标“受限”，以及用户价格页展示禁用模型、缺少输入/输出模态的问题。

**更新 `billing-service`、`channel-service`、`identity-service`、`admin-api` 和前端 `web/dist`**。API/proto 只有兼容性新增；无新增数据库迁移或运行时业务配置，既有 IAM 状态和权限分配保持原样。

## 修复内容

### 1. API Key 订阅用量查询返回 502

**根因**：固定服务 caller 表遗漏 relay 对 billing `GetSubscriptionUsage` 的调用能力。启用专属服务身份后，billing 在执行查询前返回 `PermissionDenied`，网关将其转换为 `502 subscription service error`。

**修复**：将 relay 列入该只读 RPC 的明确系统 caller。网关继续从已验证的 API Key 快照取得用户 ID，客户端 URL 参数不能改写查询主体；无关服务和账户写入仍拒绝。

**影响服务**：`billing-service`；`relay-gateway` 使用既有接口，不需要重建。

### 2. 用户管理页邮箱、余额和已用误显示“受限”

**根因**：IAM 分支在读取 billing 前直接删除余额和已用字段；前端又把空邮箱等同于无查看权限，无法区分管理员获准查看但用户未填写的情况。

**修复**：转发当前管理员会话，由 billing 独立验证账户范围并返回快照；批量范围不覆盖全部用户时，逐项执行同样的 owner 授权，获准账户的零值也正常保留。identity 返回明确的联系字段可见性，前端区分未填写、受限和账务依赖暂不可用。

**影响服务**：`identity-service`、`admin-api` 和前端；billing 保留现有账户授权规则。

### 3. 公开价格页展示禁用模型且缺少模态

**根因**：公开价格接口调用需要管理员会话的模型管理列表，鉴权失败后静默返回未经目录过滤的价格配置，因此禁用模型仍显示，输入/输出模态没有补全。

**修复**：新增只允许专属 admin caller 读取的 `ListPublicModels` RPC，owner 在计数与分页前限定启用、公开模型，仅投影模型 ID、状态和输入/输出模态。价格接口遍历目录分页，并与有效计费价格配置取交集；禁用、非公开和未注册价格项不再展示，目录失败不再回退到未过滤配置。注册表的编辑默认价格不替代计费价格。

**影响服务**：`channel-service`、`admin-api`；前端沿用既有模态展示组件。

## 兼容性说明

- 新增 channel gRPC `ListPublicModels`，复用列表请求/响应；新增 `UserInfo.contact_fields_visible`。现有客户端可忽略新增字段，既有 RPC 行为保持兼容。
- 升级顺序为 billing → channel → identity → admin → 前端，确保 admin 调用公开目录及消费可见性字段时 owner 已更新。
- 用户价格页只展示启用、公开、已注册且具有计费价格配置的模型；仅残留在旧价格配置中的名称不再表示可用模型。
- 无数据库迁移，无 IAM 重新切换，无权限授予或旧共享凭证恢复。回滚应使用本次备份的 IAM 兼容镜像和前端；admin 及其 channel/identity 依赖应配套回滚。

## 升级步骤

1. 备份四个服务当前镜像、Compose 和前端静态资源，核对既有专属服务身份及 IAM 状态。
2. 在本机按标准 Dockerfile 交叉构建四个 `linux/amd64` 镜像，上传并在服务器加载；服务器不构建镜像。
3. 按 billing → channel → identity → admin 顺序使用 `docker compose up -d --no-deps` 更新，核对镜像、源码摘要、健康与运行时环境。
4. 单独执行 `web` 的生产构建并发布到 `/opt/web/dist`。保留旧 hash 资源并最后替换入口文件，使已打开页面仍能加载原资源。
5. 验证真实订阅用量、用户字段、公开价格和权限拒绝边界，并检查全部九服务健康。

## 验证

- 订阅用量通过真实 gRPC 凭证/鉴权拦截器先复现相同 502，再通过修复验证 200、API Key 用户绑定、无关 caller 和账户写入拒绝；定向 race 通过。
- 真实 SQLite owner 快照验证 relay 的订阅用量系统能力；共享凭证不能取得该能力。既有权威订阅窗口测试通过。
- 用户列表回归覆盖 owner 会话转发、部分账户范围、获准零值、联系字段可见性和依赖不可用；前端显示回归通过。
- 公开目录真实 SQLite 回归覆盖分页前状态/公开性过滤、模态和准确总数；服务投影测试排除管理字段和编辑价格。公开价格 HTTP 回归覆盖空目录和目录失败。
- admin server/service、channel service、identity service、服务身份和 gRPC 传输相关全包测试通过；RBAC 829 行契约、架构和 Compose 服务身份模板检查通过。
- `make test` 单元测试目标、TypeScript、定向 ESLint、前端全部 214 项测试和生产 build/体积预算通过。前端初轮并发受本地构建负载影响有三项超时，降低 worker 数复验全部通过；需要独立运行服务的 Compose E2E 由 release 流水线执行。
- 2026-10-06 17:04（Asia/Shanghai）完成四服务和前端更新。部署前的真实 API Key 用量 502 已变为 200，返回 billing 权威快照且 URL 用户参数不能改写主体；临时验收 API Key 已删除。8 个管理用户的获准账户字段与数据库一致，联系字段可见性通过；公开价格仅返回 7 个启用公开模型且模态与目录一致。
- 九服务健康且 restart 0；四个更新服务的运行时环境逐项相同。专属 relay 用量读取及 admin 公开目录读取成功，无关 caller、共享凭证和匿名入口仍拒绝。IAM 保持 `iam/complete`，policy revision 7、catalog revision 2。
- 前端 173 个文件、实际 HTTP 入口与新用户列表 bundle 校验通过。旧镜像、Compose 和前端保留备份，脱敏镜像/回滚/验收证据见 [生产更新记录](../runbooks/evidence/patch-v0.34.1-production-2026-10-06.json)。

## 完整变更日志

- `fix: restore IAM subscription usage and console field views`
- `docs(release): v0.34.1`

