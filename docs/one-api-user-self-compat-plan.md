# One-API User Self Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add One API compatible `PUT /api/user/self` and `DELETE /api/user/self` behavior.

**Architecture:** Add a dedicated `IdentityUsecase.UpdateSelf` method for user-owned profile updates, keeping admin update semantics unchanged. Extend the existing identity HTTP `/api/user/self` handler to dispatch GET/PUT/DELETE and keep Bearer-token authentication consistent with other user endpoints.

**Tech Stack:** Go, bcrypt, Kratos HTTP transport, standard `net/http/httptest` tests.

---

## Files

- Modify: `internal/identity/biz/auth.go`
  - Add `UpdateSelf`.
- Modify: `internal/identity/biz/auth_test.go`
  - Add usecase tests for self username/display/password updates and duplicate username rejection.
- Modify: `internal/identity/data/data.go`
  - Persist username and password hash in `updateUserDB`, because self updates need those fields.
- Modify: `internal/identity/server/http.go`
  - Change `/api/user/self` handler to support GET/PUT/DELETE.
- Modify: `internal/identity/server/http_test.go`
  - Add HTTP tests for self update/delete auth and success paths.
- Modify: `docs/one-api-full-gap-analysis-20260509.md`
  - Mark self update/delete as completed and narrow remaining user self gaps.

## Task 1: Usecase Self Update

**Files:**
- Modify: `internal/identity/biz/auth_test.go`
- Modify: `internal/identity/biz/auth.go`

- [ ] **Step 1: Write failing tests**

Add:

```go
func TestIdentityUsecase_UpdateSelf_UpdatesProfile(t *testing.T)
func TestIdentityUsecase_UpdateSelf_RejectsDuplicateUsername(t *testing.T)
func TestIdentityUsecase_UpdateSelf_UpdatesPassword(t *testing.T)
```

The password test should update a user's password and verify `uc.Login` accepts the new password.

- [ ] **Step 2: Run usecase tests to verify RED**

Run:

```bash
go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelf' -count=1
```

Expected: FAIL because `UpdateSelf` is not implemented.

- [ ] **Step 3: Implement minimal usecase**

In `internal/identity/biz/auth.go`:

- Load current user by ID.
- If username is non-empty and changed, check `FindUserByUsername`.
- If another user already owns the username, return `ErrUserExists`.
- Update display name if non-empty.
- Hash password if non-empty and at least 8 characters.
- Persist through `repo.UpdateUser`.

- [ ] **Step 4: Run usecase tests to verify GREEN**

Run:

```bash
go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelf' -count=1
```

Expected: PASS.

## Task 2: HTTP Self Update And Delete

**Files:**
- Modify: `internal/identity/server/http_test.go`
- Modify: `internal/identity/server/http.go`
- Modify: `internal/identity/data/data.go`

- [ ] **Step 1: Write failing tests**

Add:

```go
func TestIdentityHTTPSelfUpdateRequiresAuth(t *testing.T)
func TestIdentityHTTPSelfUpdateChangesCurrentUser(t *testing.T)
func TestIdentityHTTPSelfDeleteRequiresAuth(t *testing.T)
func TestIdentityHTTPSelfDeleteRemovesCurrentUser(t *testing.T)
```

The update success test should call `PUT /api/user/self`, then `GET /api/user/self`, and assert the new username/display name.

The delete success test should call `DELETE /api/user/self`, then `GET /api/user/self` with the same token and expect 401.

- [ ] **Step 2: Run HTTP tests to verify RED**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPSelf' -count=1
```

Expected: FAIL because `PUT` and `DELETE` are not implemented.

- [ ] **Step 3: Implement HTTP support**

In `internal/identity/server/http.go`:

- Allow `handleSelf` to dispatch by method.
- Keep GET behavior unchanged.
- Add PUT body with `username`, `display_name`, and `password`.
- Add DELETE using `uc.DeleteUser`.

In `internal/identity/data/data.go`:

- Include `username` and `password_hash` in DB user updates so self updates persist in real storage.

- [ ] **Step 4: Run HTTP tests to verify GREEN**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPSelf' -count=1
```

Expected: PASS.

## Task 3: Documentation And Verification

**Files:**
- Modify: `docs/one-api-full-gap-analysis-20260509.md`

- [ ] **Step 1: Update gap analysis**

Move `PUT /api/user/self` and `DELETE /api/user/self` into completed branch work. Keep email binding and richer profile flows as remaining gaps.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelf' -count=1
go test ./internal/identity/server -run 'TestIdentityHTTPSelf' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run repository verification**

Run:

```bash
go test ./...
go build ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/identity/biz/auth.go internal/identity/biz/auth_test.go internal/identity/data/data.go internal/identity/server/http.go internal/identity/server/http_test.go docs/one-api-full-gap-analysis-20260509.md docs/one-api-user-self-compat-design.md docs/one-api-user-self-compat-plan.md
git commit -m "feat: add one-api user self management"
```
