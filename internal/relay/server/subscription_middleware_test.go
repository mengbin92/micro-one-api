package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"micro-one-api/internal/pkg/metrics"
	subscriptionbiz "micro-one-api/internal/subscription/biz"
	subscriptiondata "micro-one-api/internal/subscription/data"
)

func TestSubscriptionQuotaMiddlewareRejectsExceededQuota(t *testing.T) {
	repo := subscriptiondata.NewMemoryRepositoryForTest()
	group := &subscriptionbiz.SubscriptionGroup{
		Name:          "pro",
		Platform:      "openai",
		Status:        subscriptionbiz.SubscriptionGroupStatusEnabled,
		DailyLimitUSD: ptrFloat64Server(1),
	}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := subscriptionbiz.NewSubscriptionUsecase(repo, repo)
	if _, err := uc.Assign(context.Background(), &subscriptionbiz.AssignSubscriptionRequest{UserID: 42, GroupID: group.ID, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("Assign() error = %v", err)
	}
	if err := uc.RecordUsage(context.Background(), 42, 0.75); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	srv := &HTTPServer{identityClient: rawIdentityClient{userIDByToken: map[string]int64{"user-token": 42}}}
	srv.SetSubscriptionUsecase(uc)

	nextCalled := false
	handler := srv.withSubscriptionQuotaCheck(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))
	rejectedBefore := testutil.ToFloat64(metrics.SubscriptionQuotaChecksTotal.WithLabelValues("rejected"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("X-Estimated-Cost-USD", "0.5")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}
	rejectedAfter := testutil.ToFloat64(metrics.SubscriptionQuotaChecksTotal.WithLabelValues("rejected"))
	if rejectedAfter-rejectedBefore != 1 {
		t.Fatalf("subscription quota rejected metric delta = %v, want 1", rejectedAfter-rejectedBefore)
	}
}

func TestSubscriptionEstimatedCostFromHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Estimated-Cost-USD", "0.25")
	if got := subscriptionEstimatedCostFromHeader(req); got != 0.25 {
		t.Fatalf("estimated cost = %v, want 0.25", got)
	}
	req.Header.Set("X-Estimated-Cost-USD", "bad")
	if got := subscriptionEstimatedCostFromHeader(req); got != defaultSubscriptionEstimatedCostUSD {
		t.Fatalf("fallback estimated cost = %v, want %v", got, defaultSubscriptionEstimatedCostUSD)
	}
}

func ptrFloat64Server(v float64) *float64 {
	return &v
}
