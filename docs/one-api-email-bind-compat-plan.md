# One-API Email Bind Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add One API compatible `/api/oauth/email/bind` for authenticated users.

**Architecture:** Keep email verification state in the existing identity HTTP `verificationStore`. Add a narrow `IdentityUsecase.UpdateSelfEmail` method so self email binding does not reuse admin user update semantics.

**Tech Stack:** Go, Kratos HTTP transport, standard `net/http/httptest` tests.

---

## Files

- Modify: `internal/identity/biz/auth.go`
  - Add `UpdateSelfEmail`.
- Modify: `internal/identity/biz/auth_test.go`
  - Add usecase tests.
- Modify: `internal/identity/server/http.go`
  - Register `/api/oauth/email/bind`.
  - Add handler that authenticates current user and validates `verificationStore`.
- Modify: `internal/identity/server/http_test.go`
  - Add HTTP tests.
- Modify: `docs/one-api-full-gap-analysis-20260509.md`
  - Mark email bind as completed and narrow remaining OAuth gaps.

## Task 1: Usecase Email Update

**Files:**
- Modify: `internal/identity/biz/auth_test.go`
- Modify: `internal/identity/biz/auth.go`

- [ ] **Step 1: Write failing tests**

Add:

```go
func TestIdentityUsecase_UpdateSelfEmail_UpdatesEmail(t *testing.T)
func TestIdentityUsecase_UpdateSelfEmail_PreservesOtherFields(t *testing.T)
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelfEmail' -count=1
```

Expected: FAIL because `UpdateSelfEmail` is not implemented.

- [ ] **Step 3: Implement minimal usecase**

In `internal/identity/biz/auth.go`:

- Find user by ID.
- Require non-empty email.
- Set `user.Email`.
- Persist through `repo.UpdateUser`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelfEmail' -count=1
```

Expected: PASS.

## Task 2: HTTP Email Bind

**Files:**
- Modify: `internal/identity/server/http_test.go`
- Modify: `internal/identity/server/http.go`

- [ ] **Step 1: Write failing HTTP tests**

Add:

```go
func TestIdentityHTTPEmailBindRequiresAuth(t *testing.T)
func TestIdentityHTTPEmailBindRejectsInvalidCode(t *testing.T)
func TestIdentityHTTPEmailBindUpdatesEmail(t *testing.T)
```

The success test should call `/api/verification?email=new@example.com`, extract `verification_code`, then call `/api/oauth/email/bind?email=new@example.com&code=<code>` with Bearer token and assert `/api/user/self` returns the new email.

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPEmailBind' -count=1
```

Expected: FAIL because route is missing.

- [ ] **Step 3: Implement HTTP route**

In `internal/identity/server/http.go`:

- Register `/api/oauth/email/bind`.
- Require GET.
- Authenticate with `authSnapshotFromRequest`.
- Validate `email` and `code`.
- Check `verificationStore["v:"+email]`.
- Call `uc.UpdateSelfEmail`.
- Return One API style JSON.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPEmailBind' -count=1
```

Expected: PASS.

## Task 3: Docs And Verification

**Files:**
- Modify: `docs/one-api-full-gap-analysis-20260509.md`

- [ ] **Step 1: Update gap analysis**

Move `/api/oauth/email/bind` into completed branch work. Keep GitHub/OIDC/Lark/WeChat full OAuth flows as remaining gaps.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/identity/biz -run 'TestIdentityUsecase_UpdateSelfEmail' -count=1
go test ./internal/identity/server -run 'TestIdentityHTTPEmailBind' -count=1
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
git add internal/identity/biz/auth.go internal/identity/biz/auth_test.go internal/identity/server/http.go internal/identity/server/http_test.go docs/one-api-full-gap-analysis-20260509.md docs/one-api-email-bind-compat-design.md docs/one-api-email-bind-compat-plan.md
git commit -m "feat: add one-api email bind endpoint"
```
