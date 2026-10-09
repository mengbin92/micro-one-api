package biz

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
	"time"
)

const (
	ExpiryCheckInterval = time.Hour
	ExpiryWarnBefore    = 24 * time.Hour
)

type SubscriptionExpiryChecker struct {
	repo     SubscriptionRepository
	now      func() time.Time
	notifier ExpiryNotifier
	notified map[string]struct{}
}

type ExpiryNotification struct {
	SubscriptionID int64
	UserID         int64
	ExpiresAt      int64
}

type ExpiryNotificationClaimer interface {
	ClaimExpiryNotification(context.Context, ExpiryNotification) (func(context.Context, bool) error, bool, error)
}

type ExpiryNotifier interface {
	NotifyExpiry(ctx context.Context, notification ExpiryNotification) error
}

func NewSubscriptionExpiryChecker(repo SubscriptionRepository) *SubscriptionExpiryChecker {
	return &SubscriptionExpiryChecker{
		repo:     repo,
		now:      time.Now,
		notified: make(map[string]struct{}),
	}
}

func (c *SubscriptionExpiryChecker) SetNotifier(n ExpiryNotifier) { c.notifier = n }

func (c *SubscriptionExpiryChecker) Run(ctx context.Context) {
	if c == nil {
		return
	}
	ticker := time.NewTicker(ExpiryCheckInterval)
	defer ticker.Stop()
	c.notify(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.notify(ctx)
		}
	}
}

func (c *SubscriptionExpiryChecker) notify(ctx context.Context) {
	notifications, err := c.Tick(ctx)
	if err != nil {
		applogger.Log.Warn("subscription expiry scan failed", zap.Error(err))
		return
	}
	if c.notifier == nil {
		return
	}
	active := make(map[string]struct{}, len(notifications))
	for _, n := range notifications {
		active[fmt.Sprintf("%d:%d", n.SubscriptionID, n.ExpiresAt)] = struct{}{}
	}
	for key := range c.notified {
		if _, ok := active[key]; !ok {
			delete(c.notified, key)
		}
	}
	for _, notification := range notifications {
		key := fmt.Sprintf("%d:%d", notification.SubscriptionID, notification.ExpiresAt)
		if _, seen := c.notified[key]; seen {
			continue
		}
		complete := func(context.Context, bool) error { return nil }
		if repo, ok := c.repo.(ExpiryNotificationClaimer); ok {
			finish, claimed, err := repo.ClaimExpiryNotification(ctx, notification)
			if err != nil {
				applogger.Log.Warn("claim expiry reminder failed", zap.Error(err))
				continue
			}
			if !claimed {
				continue
			}
			complete = finish
		}
		sendCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err := c.notifier.NotifyExpiry(sendCtx, notification)
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		finishErr := complete(finishCtx, err == nil)
		finishCancel()
		if err != nil || finishErr != nil {
			applogger.Log.Warn("subscription expiry reminder failed", zap.Int64("subscription_id", notification.SubscriptionID), zap.Error(err), zap.NamedError("claim_error", finishErr))
			continue
		}
		c.notified[key] = struct{}{}
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
