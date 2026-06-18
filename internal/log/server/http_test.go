package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	identityv1 "micro-one-api/api/identity/v1"
	logbiz "micro-one-api/internal/log/biz"
	logdata "micro-one-api/internal/log/data"
	logservice "micro-one-api/internal/log/service"

	"google.golang.org/grpc"
)

func TestLogHTTPUserLogsRequireAuth(t *testing.T) {
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/log/self", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogHTTPUserLogsRequireIdentityClient(t *testing.T) {
	srv := newLogHTTPServerForTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/log/self", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogHTTPUserLogsReturnOnlyCurrentUser(t *testing.T) {
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{token: "user-token", userID: 2})

	req := httptest.NewRequest(http.MethodGet, "/api/log/self?page_size=20", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"user_id":2`) {
		t.Fatalf("self log response mismatch: %s", body)
	}
	if strings.Contains(body, `"user_id":1`) {
		t.Fatalf("self log response leaked another user: %s", body)
	}
}

func TestLogHTTPUserLogSearchReturnsOnlyCurrentUser(t *testing.T) {
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{token: "user-token", userID: 2})

	req := httptest.NewRequest(http.MethodGet, "/api/log/self/search?keyword=target", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "target from user two") {
		t.Fatalf("search response missing current user result: %s", body)
	}
	if strings.Contains(body, "target from user one") {
		t.Fatalf("search response leaked another user: %s", body)
	}
}

func TestLogHTTPUserLogStatsReturnsCurrentUserStats(t *testing.T) {
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{token: "user-token", userID: 2})

	req := httptest.NewRequest(http.MethodGet, "/api/log/self/stat", nil)
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"total":2`) {
		t.Fatalf("stats response mismatch: %s", body)
	}
	if !strings.Contains(body, `"info":1`) || !strings.Contains(body, `"error":1`) {
		t.Fatalf("stats response missing type counts: %s", body)
	}
	for _, want := range []string{
		`"usage"`,
		`"day":"2026-05-12"`,
		`"model_name":"gpt-4o-mini"`,
		`"request_count":2`,
		`"quota":30`,
		`"prompt_tokens":13`,
		`"completion_tokens":17`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stats response missing usage field %s: %s", want, body)
		}
	}
}

func TestLogHTTPDeleteLogsRequiresServiceAuthAndEndTime(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "service-token")
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{})

	req := httptest.NewRequest(http.MethodDelete, "/v1/logs?type=info", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/logs?type=info", nil)
	req.Header.Set("Authorization", "Bearer service-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogHTTPDeleteLogsDeletesMatchingServiceLogs(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "service-token")
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{})

	end := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC).Unix()
	req := httptest.NewRequest(http.MethodDelete, "/v1/logs?type=info&user_id=2&end_time="+strconv.FormatInt(end, 10), nil)
	req.Header.Set("Authorization", "Bearer service-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deleted":1`) {
		t.Fatalf("delete response mismatch: %s", rec.Body.String())
	}
}

func TestLogHTTPIngestSanitizesMessage(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "service-token")
	srv := newLogHTTPServerForTest(t, &logHTTPIdentityClient{})

	req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(`{"level":"info","message":"Authorization: Bearer secret-token","source":"test"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "secret-token") {
		t.Fatalf("response leaked token: %s", string(body))
	}
	if !strings.Contains(string(body), "***REDACTED***") {
		t.Fatalf("response missing redaction: %s", string(body))
	}
}

type logHTTPIdentityClient struct {
	identityv1.IdentityServiceClient
	token  string
	userID int64
}

func (c *logHTTPIdentityClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	if req.Token != c.token {
		return nil, context.Canceled
	}
	return &identityv1.GetAuthSnapshotReply{
		UserId:       c.userID,
		UserEnabled:  true,
		TokenEnabled: true,
	}, nil
}

func newLogHTTPServerForTest(t *testing.T, identityClient identityv1.IdentityServiceClient) http.Handler {
	t.Helper()
	repo := logdata.NewMemoryRepositoryForTest()
	uc := logbiz.NewLogUsecase(repo)
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	for _, entry := range []*logbiz.LogEntry{
		{Level: "info", Message: "target from user one", Source: "relay", UserID: 1, CreatedAt: now},
		{Level: "info", Message: "target from user two", Source: "relay", UserID: 2, CreatedAt: now, ModelName: "gpt-4o-mini", Quota: 10, PromptTokens: 4, CompletionTokens: 6},
		{Level: "error", Message: "failure from user two", Source: "relay", UserID: 2, CreatedAt: now, ModelName: "gpt-4o-mini", Quota: 20, PromptTokens: 9, CompletionTokens: 11},
	} {
		if err := uc.IngestLog(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}
	svc := logservice.NewLogService(uc)
	if identityClient == nil {
		return NewHTTPServer(":0", svc)
	}
	return NewHTTPServer(":0", svc, identityClient)
}
