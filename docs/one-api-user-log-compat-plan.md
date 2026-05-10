# One-API User Log Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add One API compatible user self log endpoints to `log-service`.

**Architecture:** Keep existing service-token `/v1/logs` behavior unchanged. Add optional identity client support to the log HTTP server for Bearer-token user authentication, and add user-filtered log query methods below the existing log usecase/repo APIs.

**Tech Stack:** Go, Kratos HTTP transport, generated identity gRPC client interface, standard `net/http/httptest` tests.

---

## Files

- Modify: `internal/log/biz/log.go`
  - Add `ListUserLogs`.
  - Extend repo interface with user-filtered query.
- Modify: `internal/log/data/data.go`
  - Implement user-filtered DB and memory queries.
- Modify: `internal/log/data/data_test.go`
  - Add user filtering and keyword tests.
- Modify: `internal/log/service/log.go`
  - Add One API response helpers and handlers for `/api/log/self*`.
- Modify: `internal/log/server/http.go`
  - Add optional identity client dependency.
  - Register `/api/log/self`, `/api/log/self/search`, `/api/log/self/stat`.
- Add: `internal/log/server/http_test.go`
  - Add HTTP tests with a fake identity client.
- Modify: `docs/one-api-full-gap-analysis-20260509.md`
  - Mark user log self endpoints as completed and correct stale gap entries.

## Task 1: User-Filtered Log Query

**Files:**
- Modify: `internal/log/data/data_test.go`
- Modify: `internal/log/biz/log.go`
- Modify: `internal/log/data/data.go`

- [ ] **Step 1: Write failing data/usecase tests**

Add tests:

```go
func TestMemoryRepository_ListByUser(t *testing.T)
func TestMemoryRepository_ListByUserFiltersKeyword(t *testing.T)
```

The first test should create logs for two users and assert only requested user logs are returned. The second should search by keyword and assert logs from other users are excluded.

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/log/data -run 'TestMemoryRepository_ListByUser' -count=1
```

Expected: FAIL because `ListByUser` does not exist.

- [ ] **Step 3: Implement minimal query support**

In `internal/log/biz/log.go`:

- Add `ListByUser(ctx, userID, page, pageSize, level, keyword)` to `LogRepo`.
- Add `ListUserLogs` to `LogUsecase` with page/page_size normalization.

In `internal/log/data/data.go`:

- Implement `ListByUser`.
- DB query filters `user_id = ?`.
- Memory query filters `entry.UserID == userID`.
- Keep existing `List` unchanged.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/log/data -run 'TestMemoryRepository_ListByUser' -count=1
```

Expected: PASS.

## Task 2: One API User Log HTTP Endpoints

**Files:**
- Add: `internal/log/server/http_test.go`
- Modify: `internal/log/service/log.go`
- Modify: `internal/log/server/http.go`

- [ ] **Step 1: Write failing HTTP tests**

Add tests:

```go
func TestLogHTTPUserLogsRequireAuth(t *testing.T)
func TestLogHTTPUserLogsRequireIdentityClient(t *testing.T)
func TestLogHTTPUserLogsReturnOnlyCurrentUser(t *testing.T)
func TestLogHTTPUserLogSearchReturnsOnlyCurrentUser(t *testing.T)
func TestLogHTTPUserLogStatsReturnsCurrentUserStats(t *testing.T)
```

Use a fake `identityv1.IdentityServiceClient` that returns `GetAuthSnapshotReply{UserId: 2}` for token `user-token`.

- [ ] **Step 2: Run HTTP tests to verify RED**

Run:

```bash
go test ./internal/log/server -run 'TestLogHTTPUserLog' -count=1
```

Expected: FAIL because routes are not registered and constructor does not accept identity client.

- [ ] **Step 3: Implement HTTP support**

In `internal/log/server/http.go`:

- Change constructor to `NewHTTPServer(addr string, svc *service.LogService, identityClients ...identityv1.IdentityServiceClient)`.
- Register `/api/log/self`, `/api/log/self/search`, `/api/log/self/stat`.
- Pass optional identity client into service handler methods.

In `internal/log/service/log.go`:

- Add `HandleOneAPIUserLogs`.
- Add `HandleOneAPIUserLogSearch`.
- Add `HandleOneAPIUserLogStats`.
- Add Bearer token helper that calls `GetAuthSnapshot`.
- Return One API style `success/message/data` responses.

- [ ] **Step 4: Run HTTP tests to verify GREEN**

Run:

```bash
go test ./internal/log/server -run 'TestLogHTTPUserLog' -count=1
```

Expected: PASS.

## Task 3: Documentation And Verification

**Files:**
- Modify: `docs/one-api-full-gap-analysis-20260509.md`

- [ ] **Step 1: Update gap analysis**

Mark these as completed:

- `/api/log/self`
- `/api/log/self/search`
- `/api/log/self/stat`

Also correct stale entries for `/api/notice`, `/api/about`, `/api/home_page_content`, `/api/group`, `/api/channel/test`, and `/api/channel/update_balance` if they are already implemented as basic compatibility routes.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/log/... -count=1
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
git add internal/log/biz/log.go internal/log/data/data.go internal/log/data/data_test.go internal/log/service/log.go internal/log/server/http.go internal/log/server/http_test.go docs/one-api-full-gap-analysis-20260509.md docs/one-api-user-log-compat-design.md docs/one-api-user-log-compat-plan.md
git commit -m "feat: add one-api user log endpoints"
```
