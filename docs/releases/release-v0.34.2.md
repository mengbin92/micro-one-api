# Micro-One-API v0.34.2 发布：本人订阅进度会话转发修复

> 2026-10-06 · 上一版：[v0.34.1](./release-v0.34.1.md)（2026-10-06）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.2)

v0.34.2 是 v0.34.1 之后的 **PATCH 修复版本**：修复已登录用户调用 `/api/v1/subscriptions/progress?user_id=1` 仍返回 `user session required` 的问题，并补齐 v0.34.1 新增联系字段可见性的前端生成类型。

**线上只需更新 `admin-api`**；前端生成类型不改变运行时页面，沿用 v0.34.1 已发布的前端资源。无新增 API/proto、数据库迁移或运行时配置。

## 修复内容

### 1. 本人订阅进度漏转发已验证会话

**根因**：本人 HTTP 入口完成会话验证后，只设置 domain 的 credential，未设置 admin 下游 RPC 转发器读取的 verified operator 上下文。billing 的本人范围鉴权因此收不到用户会话，返回 HTTP 401 和 `user session required`；API Key 的 `/v1/subscription/usage` 是另一条调用链，v0.34.1 的修复没有覆盖此入口。

**修复**：只有本人认证成功后才写入 verified operator 上下文，并标记 self request，使 billing 收到同一个已验证会话后继续核对本人范围。显式和省略 `user_id` 的本人读取均转发；异用户、缺少或失效会话仍拒绝；已有伪造/重复下游 credential 被正确替换。

**影响服务**：`admin-api`。不增加系统 caller、console 权限或账户范围豁免。

### 2. 同步前端生成 API 类型

**根因**：v0.34.1 增加 `UserInfo.contact_fields_visible` 后，未同步仓库跟踪的 `web/src/types/api.ts`，CI 的生成结果一致性检查失败。

**修复**：通过 `npm run generate:api` 从生成 OpenAPI 重新生成类型，补齐 `contactFieldsVisible`。再次生成无差异，TypeScript 检查通过；没有前端运行时行为修改。

**影响范围**：前端类型与 CI 生成检查。

## 兼容性说明

- 继续要求有效用户登录会话。`user_id` 是本人核对参数，不是访问其他用户订阅的授权。
- 保留 v0.34.1 的订阅用量 RPC 能力、用户字段可见性和公开目录过滤；billing、channel、identity 及前端沿用已验证的 v0.34.1 更新。
- 无新增数据库迁移、API/proto 或配置，无 IAM 模式/权限分配变更。

## 升级步骤

1. 备份当前 admin 镜像及 Compose，保留 v0.34.1 的 IAM 兼容回滚镜像。
2. 本机按 `app/admin/Dockerfile` 交叉构建 `linux/amd64` admin 镜像，上传后在服务器加载；服务器不构建。
3. 使用 `docker compose up -d --no-deps --no-build admin-api` 更新，检查健康、镜像源码摘要和环境一致性。
4. 使用真实登录会话验证本人订阅进度，核对伪造 `user_id` 和匿名请求仍拒绝；复验 v0.34.1 的用量、用户字段和公开价格。

## 验证

- 生产有效会话先复现 HTTP 401、`success:false`、`user session required`。
- HTTP → AdminService → billing 转发 seam 的回归先复现相同错误，修复后显式/隐式本人读取成功；异用户、缺少/失效会话拒绝，伪造和重复 metadata 被替换。定向 race、admin server/service 全包测试通过。
- RBAC 829 行入口契约通过；API 类型重新生成无差异，TypeScript 检查通过。
- 2026-10-06 17:16（Asia/Shanghai）本机交叉构建并完成 admin 更新，实际镜像、回滚标签与脱敏证据见 [生产记录](../runbooks/evidence/patch-v0.34.2-production-2026-10-06.json)。健康 200、restart 0，容器运行时环境逐项未变；挂载前端沿用 v0.34.1。
- 同一真实会话的显式/隐式本人订阅进度从 401 变为 200，返回 `usage_source=billing`；异用户为 403，匿名/无效会话为 401。既有 API Key 用量、8 用户获准字段、7 个启用公开模型及模态全部复验通过，临时验收 API Key 已删除。IAM 保持 `iam/complete`，policy revision 7、catalog revision 2。

## 完整变更日志

- `fix: forward verified sessions for self subscription progress`
- `docs(release): v0.34.2`

