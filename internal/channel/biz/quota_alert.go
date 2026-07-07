package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"micro-one-api/internal/pkg/metrics"
)

// QuotaAlertKind enumerates the subscription-account quota alert classes.
const (
	QuotaAlertExhausted     = "exhausted"      // account quota fully consumed
	QuotaAlertNearExhausted = "near_exhausted" // >= threshold (default 80%)
	QuotaAlertIdle          = "idle"           // no usage for a long period
	QuotaAlertWritebackDown = "writeback_down" // quota event writeback failing/degraded
)

// QuotaAlertEvaluatorConfig configures the quota alert evaluator.
type QuotaAlertEvaluatorConfig struct {
	Enabled            bool
	Interval           time.Duration
	PageSize           int32
	Timeout            time.Duration
	NearExhaustedPct   float64 // default 80
	IdleDuration       time.Duration // default 24h
	DedupeWindow       time.Duration // default 1h
}

// QuotaAlertNotifier is the subset of the notify-worker client needed to emit
// an alert. It mirrors biz.Notifier so the evaluator can reuse the existing
// notify channel.
type QuotaAlertNotifier interface {
	CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) error
}

// QuotaAlertEvaluator scans subscription-account quota state and emits alerts
// via the existing notify-worker channel (no new delivery path). Deduplication
// is enforced by a per-account cooldown stamped in metadata so repeated
// evaluations within the dedupe window do not re-alert.
type QuotaAlertEvaluator struct {
	repo     ChannelRepo
	notifier QuotaAlertNotifier
	alertCfg HealthAlertConfig
	now      func() time.Time
	cfg      QuotaAlertEvaluatorConfig
}

// NewQuotaAlertEvaluator builds a quota alert evaluator.
func NewQuotaAlertEvaluator(repo ChannelRepo, notifier QuotaAlertNotifier, alertCfg HealthAlertConfig, cfg QuotaAlertEvaluatorConfig) *QuotaAlertEvaluator {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Minute
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 200
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.NearExhaustedPct <= 0 {
		cfg.NearExhaustedPct = 80
	}
	if cfg.IdleDuration <= 0 {
		cfg.IdleDuration = 24 * time.Hour
	}
	if cfg.DedupeWindow <= 0 {
		cfg.DedupeWindow = time.Hour
	}
	return &QuotaAlertEvaluator{repo: repo, notifier: notifier, alertCfg: alertCfg, now: time.Now, cfg: cfg}
}

// SetNow overrides the clock (tests).
func (e *QuotaAlertEvaluator) SetNow(f func() time.Time) { e.now = f }

// Run loops until ctx is cancelled, evaluating alerts every Interval.
func (e *QuotaAlertEvaluator) Run(ctx context.Context) {
	if e == nil || !e.cfg.Enabled {
		return
	}
	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = e.EvaluateOnce(ctx)
		}
	}
}

// EvaluateOnce performs a single scan and emits any pending alerts.
func (e *QuotaAlertEvaluator) EvaluateOnce(ctx context.Context) error {
	if !e.alertCfg.Enabled || e.notifier == nil {
		return nil
	}
	now := e.now()
	page := int32(1)
	for {
		accounts, total, err := e.repo.ListSubscriptionAccounts(ctx, page, e.cfg.PageSize, "", "", 0, "")
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if account == nil {
				continue
			}
			e.evaluateAccount(ctx, account, now)
		}
		if int64(page)*int64(e.cfg.PageSize) >= total {
			return nil
		}
		page++
	}
}

// evaluateAccount checks a single account for alert conditions and emits at
// most one alert per dedupe window. The alert kind is recorded in metadata
// (last_quota_alert_kind / last_quota_alert_at) for deduplication.
func (e *QuotaAlertEvaluator) evaluateAccount(ctx context.Context, account *SubscriptionAccount, now time.Time) {
	kind := e.classify(account, now)
	if kind == "" {
		return
	}
	// Dedupe: skip if we already alerted for this kind within the window.
	lastAlertAt := parseMetadataInt(account.Metadata, "last_quota_alert_at")
	lastAlertKind := subscriptionAccountMetadataValue(account.Metadata, "last_quota_alert_kind")
	if lastAlertKind == kind && lastAlertAt > 0 && now.Unix()-lastAlertAt < int64(e.cfg.DedupeWindow.Seconds()) {
		metrics.SubscriptionQuotaAlertsTotal.WithLabelValues(kind, "deduped").Inc()
		return
	}
	if err := e.emit(ctx, account, kind, now); err != nil {
		metrics.SubscriptionQuotaAlertsTotal.WithLabelValues(kind, "error").Inc()
		return
	}
	metrics.SubscriptionQuotaAlertsTotal.WithLabelValues(kind, "sent").Inc()
}

// classify returns the highest-priority alert kind for an account, or "" if
// none. Priority: exhausted > near_exhausted > writeback_down > idle.
func (e *QuotaAlertEvaluator) classify(account *SubscriptionAccount, now time.Time) string {
	// Exhausted: any local quota window at/over its limit.
	if account.LocalQuotaExceededAt(now) {
		return QuotaAlertExhausted
	}
	// Near-exhausted: upstream snapshot primary usage >= threshold.
	if account.PrimaryQuotaUsedPercent != nil && *account.PrimaryQuotaUsedPercent >= e.cfg.NearExhaustedPct {
		return QuotaAlertNearExhausted
	}
	if account.QuotaUsedPercent >= float32(e.cfg.NearExhaustedPct) && account.QuotaUsedPercent > 0 {
		return QuotaAlertNearExhausted
	}
	// Writeback down: snapshot paused indicates the recorder could not persist.
	if account.QuotaSnapshotPaused {
		return QuotaAlertWritebackDown
	}
	// Idle: no usage for the configured duration.
	if account.LastUsedAt > 0 && now.Unix()-account.LastUsedAt >= int64(e.cfg.IdleDuration.Seconds()) {
		return QuotaAlertIdle
	}
	return ""
}

// emit sends the alert notification and stamps the dedupe metadata.
func (e *QuotaAlertEvaluator) emit(ctx context.Context, account *SubscriptionAccount, kind string, now time.Time) error {
	notifyType := e.alertCfg.NotifyType
	if notifyType == "" {
		notifyType = defaultHealthAlertNotifyType
	}
	recipients := e.alertCfg.Recipients
	if len(recipients) == 0 {
		recipients = []string{""}
	}
	subject := fmt.Sprintf("Subscription account %s: %s", account.Name, kind)
	content := quotaAlertContent(account, kind, now)
	alertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.cfg.Timeout)
	defer cancel()
	for _, recipient := range recipients {
		if err := e.notifier.CreateNotification(alertCtx, notifyType, recipient, subject, content); err != nil {
			return err
		}
	}
	// Stamp dedupe metadata so the next evaluation within the window is skipped.
	// We update only the alert-marker keys; last_error is preserved as-is.
	return e.repo.StampQuotaAlertMetadata(ctx, account.ID, kind, now.Unix())
}

// quotaAlertContent builds the human-readable alert body.
func quotaAlertContent(account *SubscriptionAccount, kind string, now time.Time) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Subscription account alert: %s\n", kind))
	b.WriteString(fmt.Sprintf("Account: %s (ID: %d)\n", account.Name, account.ID))
	b.WriteString(fmt.Sprintf("Platform: %s\n", account.Platform))
	b.WriteString(fmt.Sprintf("Group: %s\n", account.Group))
	if account.PrimaryQuotaUsedPercent != nil {
		b.WriteString(fmt.Sprintf("Primary quota used: %.1f%%\n", *account.PrimaryQuotaUsedPercent))
	}
	if account.SecondaryQuotaUsedPercent != nil {
		b.WriteString(fmt.Sprintf("Secondary quota used: %.1f%%\n", *account.SecondaryQuotaUsedPercent))
	}
	b.WriteString(fmt.Sprintf("Local quota used USD: %.4f / limit %.4f\n", account.QuotaUsedUSD, account.QuotaLimitUSD))
	b.WriteString(fmt.Sprintf("Daily used USD: %.4f / limit %.4f\n", account.QuotaDailyUsedUSD, account.QuotaDailyLimitUSD))
	if account.LastUsedAt > 0 {
		b.WriteString(fmt.Sprintf("Last used: %s\n", time.Unix(account.LastUsedAt, 0).Format(time.RFC3339)))
	}
	if account.QuotaSnapshotPaused {
		b.WriteString("Quota snapshot recording is paused.\n")
	}
	b.WriteString(fmt.Sprintf("Evaluated at: %s\n", now.Format(time.RFC3339)))
	return b.String()
}
