# One-API 用户邀请 Aff Code 兼容设计

## 背景

同级目录 `../one-api` 支持用户邀请能力：

- 用户拥有 `aff_code`。
- 注册时可以传入邀请人的邀请码。
- 新用户和邀请人可获得配置化额度奖励。
- 登录用户可以通过 `/api/user/aff` 获取自己的邀请码。

当前 `micro-one-api` 已补齐注册、登录、用户自助 token、邮箱验证/重置占位和部分管理 API，但用户邀请能力仍未实现。该能力是 `docs/one-api-full-gap-analysis-20260509.md` 中明确记录的剩余缺口。

## 目标

本期补齐 One API 风格的用户邀请基础能力：

1. 用户记录持久化 `aff_code` 和 `inviter_id`。
2. 注册接口接受 `aff_code`，绑定邀请关系。
3. 登录用户可调用 `/api/user/aff` 获取自己的邀请码。
4. 邀请奖励通过环境变量配置，默认不改变现有额度。
5. 保持与现有 identity-service 分层一致，不引入前端或跨服务大改。

## 非目标

本期不做：

1. 邀请统计页面。
2. 邀请收益提现或结算。
3. 复杂防刷策略。
4. 与 billing-service 的完整审计流水联动。
5. 迁移完整 `one-api` 前端邀请 UI。

## 数据模型

### users 表

新增字段：

```sql
ALTER TABLE users ADD COLUMN aff_code varchar(32) DEFAULT '';
ALTER TABLE users ADD COLUMN inviter_id bigint DEFAULT 0;
CREATE INDEX idx_users_aff_code ON users (aff_code);
CREATE INDEX idx_users_inviter_id ON users (inviter_id);
```

注意事项：

- `aff_code` 由业务层生成并检查唯一性。数据库使用普通索引，避免历史用户默认空字符串导致唯一索引迁移失败。
- 旧用户没有邀请码时，在调用 `/api/user/aff` 时懒生成。
- 新注册用户创建时生成邀请码。
- `inviter_id` 为 0 表示无邀请人。

## 业务设计

### 注册流程

`POST /api/user/register` 继续兼容现有字段，并新增读取：

```json
{
  "username": "alice",
  "password": "secret",
  "email": "alice@example.com",
  "aff_code": "ABCD"
}
```

流程：

1. 校验 username/password/email。
2. 如果 `aff_code` 非空，按邀请码查找邀请人。
3. 创建新用户，写入 `inviter_id`。
4. 为新用户生成唯一 `aff_code`。
5. 按配置发放邀请奖励：
   - `INVITEE_BONUS_QUOTA`：给被邀请人增加额度，默认 0。
   - `INVITER_BONUS_QUOTA`：给邀请人增加额度，默认 0。

默认值为 0 的原因：当前项目已把账务拆到 billing-service，直接修改 quota 有行为风险。默认不变更额度，部署方显式开启后再生效。

### 获取邀请码

新增：

```http
GET /api/user/aff
Authorization: Bearer <token>
```

响应：

```json
{
  "success": true,
  "message": "",
  "data": "ABCD"
}
```

如果用户没有 `aff_code`，服务端生成、持久化并返回。

## 分层改动

### identity biz

新增能力：

- `GetOrCreateAffCode(ctx, userID int64) (string, error)`
- `RegisterWithAffCode(ctx, username, password, email, group, affCode string) (*User, error)`

`User` 增加：

- `AffCode string`
- `InviterID int64`

`IdentityRepo` 增加：

- `FindUserByAffCode(ctx, affCode string) (*User, error)`
- `IncreaseUserQuota(ctx, userID int64, amount int64) error`

奖励额度在 usecase 中读取环境变量并通过 repo 执行。

### identity data

MySQL/GORM 与内存测试 repo 都需要支持：

- `aff_code`
- `inviter_id`
- 按邀请码查用户
- 更新用户邀请码
- 增加用户 quota

### identity HTTP

新增路由：

- `/api/user/aff`

扩展注册 handler：

- 接受 `aff_code` 字段。
- 调用新的 `RegisterWithAffCode`。

## 错误处理

- 邀请码为空：按无邀请注册处理。
- 邀请码不存在：返回 `success=false`，message 为 `invalid aff code`。
- 邀请码属于自己：注册阶段不会出现同一用户；懒生成时不涉及。
- 邀请奖励配置非法：忽略并按 0 处理。
- 生成邀请码冲突：重试最多 5 次，仍失败则返回错误。

## 测试策略

1. Biz 测试：
   - 注册时生成邀请码。
   - 用有效邀请码注册写入 `inviter_id`。
   - 无效邀请码注册失败。
   - `/api/user/aff` 对无邀请码用户懒生成。
   - 奖励额度环境变量为 0 时不改变 quota。
   - 奖励额度大于 0 时增加邀请人和被邀请人 quota。

2. Data 测试：
   - `FindUserByAffCode` 命中和未命中。
   - `UpdateUser` 可持久化 `aff_code`、`inviter_id`。
   - `IncreaseUserQuota` 正确累加 quota。

3. HTTP 测试：
   - `GET /api/user/aff` 未授权返回 401。
   - 授权用户返回邀请码。
   - 注册接口接受 `aff_code`。

## 文档更新

完成实现后更新：

- `docs/one-api-full-gap-analysis-20260509.md`
- 必要时更新 `README.md` 的已知限制或功能说明。

## 验收标准

1. `go test ./internal/identity/...` 通过。
2. `go test ./...` 通过。
3. 新增迁移脚本包含 users 邀请字段。
4. `/api/user/aff` 返回 One API 风格响应。
5. 注册时传入有效邀请码会写入邀请关系。
