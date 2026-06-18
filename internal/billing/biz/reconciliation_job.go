package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	applogger "micro-one-api/internal/pkg/logger"
)

// ReconciliationJob runs periodic billing reconciliation tasks.
type ReconciliationJob struct {
	uc            *ReconciliationUsecase
	notifier      Notifier
	recipients    []string
	notifyType    string
	checkInterval time.Duration
	stopChan      chan struct{}
}

// JobOption customizes ReconciliationJob construction.
type JobOption func(*ReconciliationJob)

// WithNotifier enables alert delivery. When omitted, alerts are not sent and
// the job only logs discrepancies (legacy behaviour).
func WithNotifier(n Notifier) JobOption {
	return func(j *ReconciliationJob) { j.notifier = n }
}

// WithRecipients sets the alert recipients. Defaults to [""], which lets
// notify-worker use its configured channel fallback (for example webhook_url).
func WithRecipients(recipients []string) JobOption {
	return func(j *ReconciliationJob) {
		if len(recipients) > 0 {
			j.recipients = recipients
		}
	}
}

// WithNotifyType sets the notification channel type used for reconciliation alerts.
func WithNotifyType(notifyType string) JobOption {
	return func(j *ReconciliationJob) {
		if notifyType != "" {
			j.notifyType = notifyType
		}
	}
}

// NewReconciliationJob creates a new reconciliation job.
func NewReconciliationJob(uc *ReconciliationUsecase, checkInterval time.Duration, opts ...JobOption) *ReconciliationJob {
	if checkInterval <= 0 {
		checkInterval = 1 * time.Hour
	}
	j := &ReconciliationJob{
		uc:            uc,
		checkInterval: checkInterval,
		stopChan:      make(chan struct{}),
		recipients:    []string{""},
		notifyType:    "event",
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Start begins the reconciliation loop. Call Stop() to terminate.
func (j *ReconciliationJob) Start(ctx context.Context) {
	ticker := time.NewTicker(j.checkInterval)
	defer ticker.Stop()

	// Run once on startup
	j.runReconciliation(ctx)

	for {
		select {
		case <-ticker.C:
			j.runReconciliation(ctx)
		case <-ctx.Done():
			applogger.Log.Info("reconciliation job stopped", zap.String("reason", "context canceled"))
			return
		case <-j.stopChan:
			applogger.Log.Info("reconciliation job stopped", zap.String("reason", "stop requested"))
			return
		}
	}
}

// Stop terminates the reconciliation job.
func (j *ReconciliationJob) Stop() {
	close(j.stopChan)
}

func (j *ReconciliationJob) runReconciliation(ctx context.Context) {
	result, err := j.uc.RunReconciliation(ctx)
	if err != nil {
		applogger.Log.Error("reconciliation failed", zap.Error(err))
		return
	}

	applogger.Log.Info("reconciliation completed",
		zap.Time("run_at", result.RunAt),
		zap.Int("expired_cleaned", result.ExpiredCleaned),
		zap.Int("total_accounts", result.TotalAccounts),
		zap.Int("inconsistencies", result.DiscrepancyCount()),
	)

	for _, inc := range result.AccountInconsistencies {
		applogger.Log.Warn("account inconsistency",
			zap.String("user_id", inc.UserID),
			zap.Int64("expected", inc.ExpectedQuota),
			zap.Int64("actual", inc.ActualQuota),
			zap.Int64("frozen", inc.FrozenQuota),
		)
	}
	for _, inc := range result.ChannelInconsistencies {
		applogger.Log.Warn("channel inconsistency",
			zap.Int64("channel_id", inc.ChannelID),
			zap.Int64("expected", inc.ExpectedUsedQuota),
			zap.Int64("actual", inc.ActualUsedQuota),
			zap.Int64("diff", inc.Difference),
			zap.Int64("upstream_cost", inc.UpstreamCost),
		)
	}
	for _, inc := range result.LogInconsistencies {
		applogger.Log.Warn("ledger log inconsistency",
			zap.Int64("ledger_count", inc.LedgerCount),
			zap.Int64("log_count", inc.LogCount),
			zap.Int64("count_diff", inc.CountDiff),
			zap.Int64("quota_diff", inc.QuotaDiff),
		)
	}

	if j.notifier == nil || result.DiscrepancyCount() == 0 {
		return
	}
	if err := j.dispatchAlerts(ctx, result); err != nil {
		applogger.Log.Warn("reconciliation alert dispatch failed", zap.Error(err))
	}
}

// dispatchAlerts sends a single combined alert when discrepancies exist. We
// intentionally group all categories into one notification to avoid alert
// spam during partial outages; operators can drill down via the admin
// reconciliation page.
func (j *ReconciliationJob) dispatchAlerts(ctx context.Context, result *ReconciliationResult) error {
	subject := fmt.Sprintf("[recon] %d discrepancies @ %s",
		result.DiscrepancyCount(), result.RunAt.UTC().Format(time.RFC3339))
	content := buildAlertContent(result)

	var errs []string
	for _, recipient := range j.recipients {
		if err := j.notifier.CreateNotification(ctx, j.notifyType, recipient, subject, content); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", recipient, err))
			continue
		}
		applogger.Log.Info("reconciliation alert sent", zap.String("recipient", recipient), zap.Int("discrepancies", result.DiscrepancyCount()))
	}
	if len(errs) > 0 {
		return fmt.Errorf("notify: %s", strings.Join(errs, "; "))
	}
	return nil
}

func buildAlertContent(r *ReconciliationResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reconciliation run at %s found %d discrepancies.\n",
		r.RunAt.UTC().Format(time.RFC3339), r.DiscrepancyCount())
	fmt.Fprintf(&b, "Expired reservations cleaned: %d\n", r.ExpiredCleaned)
	fmt.Fprintf(&b, "Accounts checked: %d (mismatches: %d)\n", r.TotalAccounts, len(r.AccountInconsistencies))
	fmt.Fprintf(&b, "Channels checked: %d (mismatches: %d)\n", r.TotalChannels, len(r.ChannelInconsistencies))
	fmt.Fprintf(&b, "Ledger/log consume groups drifted: %d\n", len(r.LogInconsistencies))

	if len(r.AccountInconsistencies) > 0 {
		b.WriteString("\nAccount quota mismatches (showing up to 5):\n")
		for i, inc := range r.AccountInconsistencies {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - user=%s expected=%d actual=%d frozen=%d\n",
				inc.UserID, inc.ExpectedQuota, inc.ActualQuota, inc.FrozenQuota)
		}
	}
	if len(r.ChannelInconsistencies) > 0 {
		b.WriteString("\nChannel usage mismatches (showing up to 5):\n")
		for i, inc := range r.ChannelInconsistencies {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - channel=%d expected=%d actual=%d diff=%d upstream_cost=%d\n",
				inc.ChannelID, inc.ExpectedUsedQuota, inc.ActualUsedQuota, inc.Difference, inc.UpstreamCost)
		}
	}
	if len(r.LogInconsistencies) > 0 {
		b.WriteString("\nLedger/log consume drift (showing up to 5):\n")
		for i, inc := range r.LogInconsistencies {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - ledger_count=%d log_count=%d count_diff=%d quota_diff=%d\n",
				inc.LedgerCount, inc.LogCount, inc.CountDiff, inc.QuotaDiff)
		}
	}
	return b.String()
}
