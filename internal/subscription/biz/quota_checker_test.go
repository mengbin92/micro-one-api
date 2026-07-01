package biz

import (
	"context"
	"testing"
	"time"
)

func TestQuotaChecker_DelegatesToUsecase(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled, DailyLimitUSD: ptrFloat64(5)}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(1000, 0) }
	if _, err := uc.Assign(context.Background(), &AssignSubscriptionRequest{UserID: 1, GroupID: group.ID, ExpiresAt: 2000}); err != nil {
		t.Fatalf("Assign() error = %v", err)
	}

	checker := NewQuotaChecker(uc)
	result, err := checker.CheckQuota(context.Background(), 1, 1.5)
	if err != nil {
		t.Fatalf("CheckQuota() error = %v", err)
	}
	if !result.Allowed {
		t.Fatalf("expected allowed quota, got %+v", result)
	}
}
