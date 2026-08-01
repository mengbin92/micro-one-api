package biz

import (
	"context"
	"time"
)

const (
	ExpiryCheckInterval = time.Hour
	ExpiryWarnBefore    = 24 * time.Hour
)

type SubscriptionExpiryChecker struct {
	repo SubscriptionRepository
	now  func() time.Time
}

type ExpiryNotification struct {
	SubscriptionID int64
	UserID         int64
	ExpiresAt      int64
}

func NewSubscriptionExpiryChecker(repo SubscriptionRepository) *SubscriptionExpiryChecker {
	return &SubscriptionExpiryChecker{
		repo: repo,
		now:  time.Now,
	}
}

func (c *SubscriptionExpiryChecker) Run(ctx context.Context) {
	if c == nil {
		return
	}
	ticker := time.NewTicker(ExpiryCheckInterval)
	defer ticker.Stop()
	_, _ = c.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = c.Tick(ctx)
		}
	}
}

func (c *SubscriptionExpiryChecker) Tick(ctx context.Context) ([]ExpiryNotification, error) {
	if c == nil || c.repo == nil {
		return nil, nil
	}
	now := c.now().Unix()
	subs, err := c.repo.ListActiveSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	notifications := make([]ExpiryNotification, 0)
	for _, sub := range subs {
		if sub == nil {
			continue
		}
		if sub.ExpiresAt <= now {
			sub.Status = SubscriptionStatusExpired
			sub.UpdatedAt = now
			// domain-H1: write only status (+ updated_at). The expiry transition
			// must not clobber a concurrent AddUsage's usage/window increments.
			if err := c.repo.UpdateSubscriptionFields(ctx, sub, []SubscriptionField{SubscriptionFieldStatus}); err != nil {
				return nil, err
			}
			continue
		}
		if sub.ExpiresAt <= now+int64(ExpiryWarnBefore.Seconds()) {
			notifications = append(notifications, ExpiryNotification{
				SubscriptionID: sub.ID,
				UserID:         sub.UserID,
				ExpiresAt:      sub.ExpiresAt,
			})
		}
	}
	return notifications, nil
}
