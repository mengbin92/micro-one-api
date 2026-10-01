# RBAC A6 IAM RPC、后台 API 与本人会话交付

> 2026-10-01 · 基线 `87025145` · 工作分支 `codex/rbac-a5-a6-governance-api`。
> A6 后端 API 完成；治理语义见 [A5](./a5-governance-delivery.md)。当前仅隔离 IAM/complete 环境可验收管理 API；identity/admin 已按用户授权更新线上，生产继续 legacy；详见 [生产更新记录](./a5-a6-legacy-production-deployment.md)。

## 契约与真实启动链

`api/identity/v1/iam.proto` 提供 52 个 IAMService RPC；`api/admin/v1/iam.proto` 提供 51 个 IAMAdminService RPC/HTTP 映射。覆盖目录、角色生命周期/引用/成员、授权与继承、分配/批量/预检、三类委派、约束、菜单、有效授权/解释/模拟、审计查询/独立导出、他人会话查询/撤销和本人会话。RescueRootCredential 仅在 identity 专用 RPC 上提供。

admin HTTP 使用 `/api/v1/admin/iam`，本人入口为 `/api/user/authorization`、`/api/user/session/roles` 与 `DELETE /api/user/session`；identity 直接 HTTP 仅注册四个本人方法。DELETE 管理写的 reason/request_id/expected_revision/expected_policy_revision 通过查询参数传入；分配撤销由服务端按 user path 与 assignment ID 读取实际 role/boundary，不需要请求体。详细路径以 proto 和生成 OpenAPI 为准。

| 层/启动点 | 实现 |
|---|---|
| identity service | ValidateRequest 验证 timestamp/context/ID/规模/mask；转换 DTO ↔ DO 后调用 biz，无 storage 或治理规则 |
| identity biz/data | A5 主库权威治理、范围过滤、CAS 与成功审计原子；业务模型与 PO 分离，构造返回接口 |
| admin biz | 共享纯 Request/Response 与 IAMRepo，保留操作者凭证并委托数据所有者 |
| admin data/iam | 共用 identityConn 的 IAMServiceClient；覆盖转发 operator metadata，不访问 IAM 表、不导入 identity internal |
| admin service | 真实 HTTP bearer 或 gRPC interceptor 提供操作者，校验/转换后经 biz → RPC；新增 IAM 路由按具体 IAM 操作授权 |
| identity cmd/Wire | 新治理用例/IAMService 进入实际 gRPC 构造；现存 runtime/runner 在 bootstrap 前接入 |
| admin cmd/Wire | 新 client/repo/usecase/service；AdminService setter 后注册 HTTP；`admin_helpers.go` 实际 gRPC 和 alternate server 均注册 IAMAdminService/interceptor |
| 生成物 | `make all` 生成 API/config/Wire；OpenAPI → `npm run generate:api` 同步前端类型。pb/OpenAPI 延续仓库忽略规则，无手改生成文件 |

RPC 普通用户操作同时要求服务凭证与单个 operator bearer，identity 从 JWT/JTI 验证实际 actor，不接受客户端 actor 字段或 JWT 数值 role。ADMIN_TOKEN 不能执行普通管理或本人方法。本人 HTTP 方法仅操作验证后自己的 JTI，拒绝选择其他 user_id/id/source_id；待选择会话仍可查询/激活，角色集合复用 A4/DSD/CAS。

IAM HTTP 使用专用 protojson codec：int64/uint64 按字符串输出，timestamp/field mask 使用 protobuf JSON，拒绝未知字段、无效时间、超限请求和组织域；默认平台入口可省略 context，RPC 显式 context 仍核实一致性。401 表示身份/会话无效，403 表示操作/委派/保护规则拒绝，409 表示版本或约束冲突。

## 范围、查询与决策边界

列表先做数据所有者逐对象授权和委派过滤，再计算 filter/order/page/total；详情、成员、来源、引用与审计独立检查。grant/graph 不随 role.list 隐式暴露；模拟/成功写的影响来源也按独立分配/grant 读取权限过滤，不暴露目标用户其他不可管理分配；管理某个角色不能查看另一角色或隐藏成员。分页 token 绑定域、用户/会话版本、policy、入口和查询。审计数据层只查询可见 role targets；非 root 原始 before/after/diff、操作者 JTI、request ID/reason 脱敏，批量/账号等跨对象审计只供 root。

CheckAuthorization 当前仅支持明确的 identity 所有者 IAM 方法 selector，如 GetRole；对应固定 operation、目标事实、范围、委派与业务保护使用相同真实 handler 计算。浏览器传 `channel.channel.read` 或任意 permission 字符串均拒绝。ExplainAuthorization 的对象输入只用于解释模拟，输出不能用于业务执行。跨资源所有者 full-method/caller 注册和权威 business facts 接入由 B0/B1–B4 交付，当前不提供可用于这些执行链的通用 capability。

代码只为实际交付的 IAM 执行点报告 bound；普通元数据不能改 binding/scope/context/protected。registered-but-unbound 的业务权限仍拒绝执行/发布。已有业务页面和旧 HTTP/RPC guard 的迁移仍属于 B/C 阶段。

## 受保护凭证恢复

identity RPC RescueRootCredential 的 interceptor 只接受 `IAM_RESCUE_SERVICE_TOKEN`，不能用共享 SERVICE_TOKEN 进入；另需 ADMIN_TOKEN、`IAM_RESCUE_ENABLED=true`、root 目标、iam/complete、reason 与 user/policy CAS。调用 A4 救援用例，密码 epoch、全域会话撤销、版本和审计同事务；不输出密码或哈希，不开放日常角色管理。

`cmd/admin-reset -iam-rescue` 走专用 RPC，无数据库 DSN；要求 endpoint/user-id/reason/两份 revision，拒绝 role/email 修改。生成密码只在提交成功后写一次性绝对路径文件（0600/O_EXCL）。原 SQL 模式继续受 legacy/idle policy 锁门槛保护。

```sh
# 仅在已经授权的恢复环境配置专用凭证/开关后使用；不包含实际秘密。
admin-reset -iam-rescue -identity-grpc-endpoint '<identity-endpoint>' \
  -user-id '<root-id>' -expected-revision '<user-revision>' \
  -expected-policy-revision '<policy-revision>' -reason '<recovery-reason>' \
  -generated-password-file '/absolute/private/root-password.txt'
```

## 验证证据

`internal/integration/TestIAMA6RealAdminIdentityEndpoints` 使用完整 scratch SQLite、真实 identity gRPC/biz/data 与 admin HTTP/biz/RPC adapter，验证双凭证、缺失 operator、ADMIN_TOKEN、JWT role=100 无授权、root 管理、HTTP 创建/409 CAS、未知 actor/组织 400、本人 HTTP/伪造目标、他人 session 查询、固定 CheckAuthorization selector、无 body DELETE 撤销及正确审计 role target。专用救援在关闭时 403，开启后成功且旧 JWT/session 401；共享 token 无法进入。全 integration 包及 IAM DTO/identity biz/service 均补做 race。

`platform/iamdto` 验证大于 2^53 的 ID、snake/camel 字段、时间/mask、未知字段拒绝、nil 与显式空范围及嵌套 context 省略往返。A5 三库与失败原子性见 [治理交付记录](./a5-governance-delivery.md)。全仓 make verify、Wire、迁移、入口契约通过。

P0 inventory 新增实际生成 HTTP 注册/annotation 检查与未注册服务反例，CLI rescue 文件纳入源码摘要。当前矩阵 757 行：254 原 HTTP 注册、55 实际 proto HTTP 路由、281 RPC、13 gRPC 注册、2 HTTP 生成注册、152 源码摘要。源码/注册漂移、未知 code、空/非法 caller 分类都阻断；矩阵中 B0–D 的测试义务不表示这些业务入口已接入 IAM。

本批没有新迁移或前端 IAM 页面；授权的生产更新保持 legacy，未执行事实源切换。下一工作包为 B0/B1 业务身份与执行链、C1 授权查询/我的角色页面；D0 初始化、影子比较和交接演练完成前不得切换生产。
