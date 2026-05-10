# One-API Token Route Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete One API compatible token HTTP routes for search, path id, delete, and body-id update.

**Architecture:** Reuse the existing identity HTTP token handler and extend route registration plus token id parsing. Keep identity token business logic unchanged.

**Tech Stack:** Go, Kratos HTTP transport, standard `net/http/httptest` tests.

---

## Files

- Modify: `internal/identity/server/http.go`
  - Use `HandlePrefix("/api/token/", ...)`.
  - Let `PUT /api/token/` read `id` from body if no path id exists.
  - Treat `/api/token/search` as list/search, not path id.
- Modify: `internal/identity/server/http_test.go`
  - Add route compatibility tests.
- Modify: `docs/one-api-full-gap-analysis-20260509.md`
  - Mark token route compatibility as completed.

## Task 1: Token Route Tests

**Files:**
- Modify: `internal/identity/server/http_test.go`

- [ ] **Step 1: Write failing tests**

Add tests:

```go
func TestIdentityHTTPTokenPathGetAndDelete(t *testing.T)
func TestIdentityHTTPTokenSearchRoute(t *testing.T)
func TestIdentityHTTPTokenUpdateAcceptsBodyID(t *testing.T)
```

Use existing register/login helpers and create tokens through `POST /api/token/`.

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPToken(Path|Search|Update)' -count=1
```

Expected: FAIL because path routes are not registered and PUT body id is not accepted.

## Task 2: Implementation

**Files:**
- Modify: `internal/identity/server/http.go`

- [ ] **Step 1: Register prefix route**

Replace exact `/api/token/` handling with prefix handling so `/api/token/123` and `/api/token/search` reach `handleTokens`.

- [ ] **Step 2: Support body id for PUT**

Add `ID int64 json:"id"` to PUT request body and use it when path id is absent.

- [ ] **Step 3: Keep search route as list**

Ensure `parseTokenID("/api/token/search")` returns `(0, false)` so query `keyword` is used.

- [ ] **Step 4: Run route tests to verify GREEN**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPToken(Path|Search|Update)' -count=1
```

Expected: PASS.

## Task 3: Docs And Verification

**Files:**
- Modify: `docs/one-api-full-gap-analysis-20260509.md`

- [ ] **Step 1: Update gap analysis**

Move token route compatibility into completed branch work. Keep advanced token fields such as subnet/accessed_time as remaining gaps.

- [ ] **Step 2: Run verification**

Run:

```bash
go test ./internal/identity/server -run 'TestIdentityHTTPToken' -count=1
go test ./...
go build ./...
```

Expected: PASS.

- [ ] **Step 3: Commit**

Run:

```bash
git add internal/identity/server/http.go internal/identity/server/http_test.go docs/one-api-full-gap-analysis-20260509.md docs/one-api-token-route-compat-design.md docs/one-api-token-route-compat-plan.md
git commit -m "feat: add one-api token route compatibility"
```
