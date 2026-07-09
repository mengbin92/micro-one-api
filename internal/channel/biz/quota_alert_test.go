package biz

import (
	"context"
	"strconv"
	"testing"
	"time"
)

type recordingAlertNotifier struct {
	notifications []recordedNotification
}

func (n *recordingAlertNotifier) CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) error {
	n.notifications = append(n.notifications, recordedNotification{
		notifyType: notifyType,
		recipient:  recipient,
		subject:    subject,
		content:    content,
	})
	return nil
}

func TestQuotaAlertEvaluator_ExhaustedAlert(t *testing.T) {
	now := time.Unix(1710000000, 0)
	acc := &SubscriptionAccount{
		ID:                 1,
		Name:               "spent",
		Status:             ChannelStatusEnabled,
		Platform:           "codex",
		QuotaDailyLimitUSD: 1,
		QuotaDailyUsedUSD:  1,
		QuotaDailyWindowStart: now.Add(-time.Hour).Unix(),
	}
	repo := newSweeperRepo(acc)
	notifier := &recordingAlertNotifier{}
	alertCfg := HealthAlertConfig{Enabled: true, NotifyType: "event", Recipients: []string{""}}
	e := NewQuotaAlertEvaluator(repo, notifier, alertCfg, QuotaAlertEvaluatorConfig{Enabled: true, PageSize: 10, DedupeWindow: time.Hour})
	e.SetNow(func() time.Time { return now })
	if err := e.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
	if got := notifier.notifications[0].subject; got != "Subscription account spent: exhausted" {
		t.Fatalf("subject = %q, want exhausted", got)
	}
}

func TestQuotaAlertEvaluator_NearExhaustedAlert(t *testing.T) {
	now := time.Unix(1710000000, 0)
	pct := 85.0
	acc := &SubscriptionAccount{
		ID:                      1,
		Name:                    "near",
		Status:                  ChannelStatusEnabled,
		Platform:                "codex",
		PrimaryQuotaUsedPercent: &pct,
	}
	repo := newSweeperRepo(acc)
	notifier := &recordingAlertNotifier{}
	e := NewQuotaAlertEvaluator(repo, notifier, HealthAlertConfig{Enabled: true}, QuotaAlertEvaluatorConfig{Enabled: true, PageSize: 10, NearExhaustedPct: 80, DedupeWindow: time.Hour})
	e.SetNow(func() time.Time { return now })
	if err := e.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
}

func TestQuotaAlertEvaluator_IdleAlert(t *testing.T) {
	now := time.Unix(1710000000, 0)
	acc := &SubscriptionAccount{
		ID:         1,
		Name:       "idle",
		Status:     ChannelStatusEnabled,
		Platform:   "codex",
		LastUsedAt: now.Add(-48 * time.Hour).Unix(),
	}
	repo := newSweeperRepo(acc)
	notifier := &recordingAlertNotifier{}
	e := NewQuotaAlertEvaluator(repo, notifier, HealthAlertConfig{Enabled: true}, QuotaAlertEvaluatorConfig{Enabled: true, PageSize: 10, IdleDuration: 24 * time.Hour, DedupeWindow: time.Hour})
	e.SetNow(func() time.Time { return now })
	if err := e.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
}

func TestQuotaAlertEvaluator_DedupeWithinWindow(t *testing.T) {
	now := time.Unix(1710000000, 0)
	acc := &SubscriptionAccount{
		ID:                 1,
		Name:               "spent",
		Status:             ChannelStatusEnabled,
		Platform:           "codex",
		QuotaDailyLimitUSD: 1,
		QuotaDailyUsedUSD:  1,
		QuotaDailyWindowStart: now.Add(-time.Hour).Unix(),
		Metadata:           `{"last_quota_alert_kind":"exhausted","last_quota_alert_at":` + strconv.FormatInt(now.Unix()-60, 10) + `}`,
	}
	repo := newSweeperRepo(acc)
	notifier := &recordingAlertNotifier{}
	e := NewQuotaAlertEvaluator(repo, notifier, HealthAlertConfig{Enabled: true}, QuotaAlertEvaluatorConfig{Enabled: true, PageSize: 10, DedupeWindow: time.Hour})
	e.SetNow(func() time.Time { return now })
	if err := e.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0 (deduped within window)", len(notifier.notifications))
	}
}

func TestQuotaAlertEvaluator_DisabledDoesNothing(t *testing.T) {
	now := time.Unix(1710000000, 0)
	acc := &SubscriptionAccount{
		ID:                 1,
		Name:               "spent",
		Status:             ChannelStatusEnabled,
		QuotaDailyLimitUSD: 1,
		QuotaDailyUsedUSD:  1,
		QuotaDailyWindowStart: now.Add(-time.Hour).Unix(),
	}
	repo := newSweeperRepo(acc)
	notifier := &recordingAlertNotifier{}
	// alert disabled
	e := NewQuotaAlertEvaluator(repo, notifier, HealthAlertConfig{Enabled: false}, QuotaAlertEvaluatorConfig{Enabled: true, PageSize: 10})
	e.SetNow(func() time.Time { return now })
	if err := e.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0 (alert disabled)", len(notifier.notifications))
	}
}

func TestQuotaAlertEvaluator_WritebackDownAlert(t *testing.T) {
	now := time.Unix(1710000000, 0)
	acc := &SubscriptionAccount{
		ID:                   1,
		Name:                 "paused",
		Status:               ChannelStatusEnabled,
		Platform:             "codex",
		QuotaSnapshotPaused:  true,
	}
	repo := newSweeperRepo(acc)
	notifier := &recordingAlertNotifier{}
	e := NewQuotaAlertEvaluator(repo, notifier, HealthAlertConfig{Enabled: true}, QuotaAlertEvaluatorConfig{Enabled: true, PageSize: 10, DedupeWindow: time.Hour})
	e.SetNow(func() time.Time { return now })
	if err := e.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("EvaluateOnce() error = %v", err)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notifier.notifications))
	}
}
