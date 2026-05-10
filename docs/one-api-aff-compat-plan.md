# One-API 用户邀请 Aff Code 兼容 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:test-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 One API 风格用户邀请能力，包括邀请码持久化、注册绑定邀请关系、`/api/user/aff` 和可配置邀请奖励。

**Architecture:** 在 identity-service 内实现闭环：biz 层负责邀请规则和奖励策略，data 层负责 `users.aff_code`、`users.inviter_id` 和 quota 更新，HTTP 层暴露 One API 兼容路由。默认奖励额度为 0，避免未配置时改变现有账务行为。

**Tech Stack:** Go, go-kratos HTTP server, existing identity biz/data layers, MySQL migrations, table-driven `go test`.

---

## File Map

- Create `migrations/015_add_user_aff_fields.sql`: 添加 `users.aff_code`、`users.inviter_id` 和索引。
- Modify `internal/identity/biz/auth.go`: 扩展 `User`、`IdentityRepo`、注册和邀请码业务方法。
- Modify `internal/identity/biz/auth_test.go`: 增加邀请注册、懒生成邀请码、奖励额度测试。
- Modify `internal/identity/data/data.go`: 持久化邀请码字段，实现按邀请码查询和 quota 累加。
- Modify `internal/identity/data/data_test.go`: 增加 data 层字段持久化和查询测试。
- Modify `internal/identity/server/http.go`: 注册 `/api/user/aff`，注册接口读取 `aff_code`。
- Modify `internal/identity/server/http_test.go`: 增加 HTTP 兼容测试。
- Modify `docs/one-api-full-gap-analysis-20260509.md`: 实现后更新剩余缺口状态。
- Modify `docs/one-api-aff-compat-design.md`: 若实现细节有变化，同步设计。

## Task 1: Migration and Data Model

- [ ] Step 1: Write data-layer failing tests.

Add tests in `internal/identity/data/data_test.go`:

```go
func TestIdentityRepoFindUserByAffCode(t *testing.T) {
    repo := newTestIdentityRepo(t)
    user := &biz.User{Username: "inviter", Status: biz.UserStatusEnabled, AffCode: "ABCD"}
    require.NoError(t, repo.CreateUser(context.Background(), user))

    got, err := repo.FindUserByAffCode(context.Background(), "ABCD")

    require.NoError(t, err)
    require.Equal(t, user.ID, got.ID)
}
```

Also cover:

- `UpdateUser` persists `AffCode` and `InviterID`.
- `IncreaseUserQuota` increments quota.

Run:

```bash
go test ./internal/identity/data -run 'TestIdentityRepo.*Aff|TestIdentityRepoIncreaseUserQuota' -count=1
```

Expected: FAIL because repo methods/fields are missing.

- [ ] Step 2: Create migration.

Create `migrations/015_add_user_aff_fields.sql`:

```sql
ALTER TABLE `users` ADD COLUMN `aff_code` varchar(32) DEFAULT '';
ALTER TABLE `users` ADD COLUMN `inviter_id` bigint DEFAULT 0;
CREATE INDEX `idx_users_aff_code` ON `users` (`aff_code`);
CREATE INDEX `idx_users_inviter_id` ON `users` (`inviter_id`);
```

Use idempotent dialect if existing migrations in this project prefer `IF NOT EXISTS`.

- [ ] Step 3: Extend data model and repo.

Modify `internal/identity/data/data.go`:

- Add `AffCode` and `InviterID` to user model.
- Map fields to/from `biz.User`.
- Implement `FindUserByAffCode`.
- Implement `IncreaseUserQuota`.

- [ ] Step 4: Run data tests.

```bash
go test ./internal/identity/data -count=1
```

Expected: PASS.

## Task 2: Biz Invitation Flow

- [ ] Step 1: Write failing biz tests.

Add tests in `internal/identity/biz/auth_test.go`:

- `RegisterWithAffCode` creates a user with a unique `AffCode`.
- Valid inviter code sets `InviterID`.
- Invalid inviter code returns an error.
- `GetOrCreateAffCode` returns existing code.
- `GetOrCreateAffCode` generates and persists a code if missing.
- `INVITEE_BONUS_QUOTA` and `INVITER_BONUS_QUOTA` apply only when positive.

Run:

```bash
go test ./internal/identity/biz -run 'Test.*Aff|Test.*Invite' -count=1
```

Expected: FAIL because biz methods are missing.

- [ ] Step 2: Extend `biz.User` and `IdentityRepo`.

Modify `internal/identity/biz/auth.go`:

```go
type User struct {
    ...
    AffCode   string
    InviterID int64
}
```

Add repo methods:

```go
FindUserByAffCode(ctx context.Context, affCode string) (*User, error)
IncreaseUserQuota(ctx context.Context, userID int64, amount int64) error
```

- [ ] Step 3: Implement invitation methods.

Add:

```go
func (uc *IdentityUsecase) RegisterWithAffCode(ctx context.Context, username, password, email, group, affCode string) (*User, error)
func (uc *IdentityUsecase) GetOrCreateAffCode(ctx context.Context, userID int64) (string, error)
```

Implementation rules:

- Generate 8-character alphanumeric codes.
- Retry code generation up to 5 times on collision.
- Use `RegisterWithAffCode` from existing `Register` with empty aff code to keep behavior unified.
- Read bonus values from env with invalid values treated as 0.

- [ ] Step 4: Run biz tests.

```bash
go test ./internal/identity/biz -count=1
```

Expected: PASS.

## Task 3: HTTP Compatibility

- [ ] Step 1: Write failing HTTP tests.

Add tests in `internal/identity/server/http_test.go`:

- `GET /api/user/aff` without token returns 401.
- `GET /api/user/aff` with token returns `success=true` and code string.
- `POST /api/user/register` accepts `aff_code` and returns success for valid code.
- `POST /api/user/register` returns `success=false` for invalid code.

Run:

```bash
go test ./internal/identity/server -run 'Test.*Aff|Test.*Register.*Aff' -count=1
```

Expected: FAIL because HTTP route/field support is missing.

- [ ] Step 2: Add route.

Modify `internal/identity/server/http.go`:

```go
srv.HandleFunc("/api/user/aff", func(w http.ResponseWriter, r *http.Request) {
    handleAffCode(w, r, uc)
})
```

- [ ] Step 3: Implement handler.

Handler requirements:

- Only `GET`.
- Use existing `authSnapshotFromRequest`.
- Call `uc.GetOrCreateAffCode`.
- Return One API response:

```json
{"success":true,"message":"","data":"ABCD1234"}
```

- [ ] Step 4: Extend register handler.

Read `aff_code` from JSON body and call `RegisterWithAffCode`.

- [ ] Step 5: Run HTTP tests.

```bash
go test ./internal/identity/server -count=1
```

Expected: PASS.

## Task 4: Documentation and Verification

- [ ] Step 1: Update gap analysis.

Modify `docs/one-api-full-gap-analysis-20260509.md`:

- Move 用户邀请 from “仍未完全实现” to a new “已补齐/计划中已实现” entry once code lands.
- Keep remaining gaps intact: full web UI, Turnstile, provider matrix, full OAuth/SSO.

- [ ] Step 2: Run identity tests.

```bash
go test ./internal/identity/... -count=1
```

Expected: PASS.

- [ ] Step 3: Run full tests.

```bash
go test ./...
```

Expected: PASS.

- [ ] Step 4: Run build.

```bash
go build ./...
```

Expected: PASS.

- [ ] Step 5: Check git status.

```bash
git status --short --branch
```

Expected: only intended migration, identity, and docs files changed.
