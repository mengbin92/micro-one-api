package biz

import (
	"context"
	"testing"
	"time"
)

func TestSubscriptionExpiryChecker_MarksExpiredAndWarnsSoon(t *testing.T) {
	repo := newMockSubscriptionRepo()
	now := time.Unix(10_000, 0)
	repo.subscriptions[1] = &UserSubscription{ID: 1, UserID: 11, GroupID: 1, Status: SubscriptionStatusActive, ExpiresAt: now.Add(-time.Minute).Unix()}
	repo.subscriptions[2] = &UserSubscription{ID: 2, UserID: 12, GroupID: 1, Status: SubscriptionStatusActive, ExpiresAt: now.Add(2 * time.Hour).Unix()}
	repo.subscriptions[3] = &UserSubscription{ID: 3, UserID: 13, GroupID: 1, Status: SubscriptionStatusActive, ExpiresAt: now.Add(2 * time.Hour).Unix()}

	checker := NewSubscriptionExpiryChecker(repo)
	checker.now = func() time.Time { return now }

	notes, err := checker.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("notifications = %d, want 2", len(notes))
	}

	updated, err := repo.GetSubscriptionByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetSubscriptionByID() error = %v", err)
	}
	if updated.Status != SubscriptionStatusExpired {
		t.Fatalf("status = %s, want expired", updated.Status)
	}
}
