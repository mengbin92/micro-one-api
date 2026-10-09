package biz

import (
	"context"
	"testing"
	"time"
)

type expiryNotifierFunc func(context.Context, ExpiryNotification) error

func (f expiryNotifierFunc) NotifyExpiry(ctx context.Context, n ExpiryNotification) error {
	return f(ctx, n)
}

func TestExpiryCheckerPrunesNotifiedSubscriptions(t *testing.T) {
	repo := newMockSubscriptionRepo()
	now := time.Unix(10000, 0)
	repo.subscriptions[1] = &UserSubscription{ID: 1, UserID: 1, Status: SubscriptionStatusActive, ExpiresAt: now.Add(time.Hour).Unix()}
	checker := NewSubscriptionExpiryChecker(repo)
	checker.now = func() time.Time { return now }
	calls := 0
	checker.SetNotifier(expiryNotifierFunc(func(context.Context, ExpiryNotification) error { calls++; return nil }))
	checker.notify(context.Background())
	checker.notify(context.Background())
	if calls != 1 || len(checker.notified) != 1 {
		t.Fatalf("calls=%d, notified=%v", calls, checker.notified)
	}
	now = now.Add(2 * time.Hour)
	checker.notify(context.Background())
	if len(checker.notified) != 0 {
		t.Fatalf("expired reminders retained: %v", checker.notified)
	}
}

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
