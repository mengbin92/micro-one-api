package biz

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
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
			log.Println("reconciliation job stopped")
			return
		case <-j.stopChan:
			log.Println("reconciliation job stopped")
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
		log.Printf("reconciliation failed: %v", err)
		return
	}

	log.Printf("reconciliation completed: run_at=%s, expired_cleaned=%d, total_accounts=%d, inconsistencies=%d",
		result.RunAt.Format(time.RFC3339),
		result.ExpiredCleaned,
		result.TotalAccounts,
		result.DiscrepancyCount(),
	)

	for _, inc := range result.AccountInconsistencies {
		log.Printf("account inconsistency: user_id=%s, expected=%d, actual=%d, frozen=%d",
			inc.UserID, inc.ExpectedQuota, inc.ActualQuota, inc.FrozenQuota)
	}
	for _, inc := range result.ChannelInconsistencies {
		log.Printf("channel inconsistency: channel_id=%d, expected=%d, actual=%d, diff=%d, upstream_cost=%d",
			inc.ChannelID, inc.ExpectedUsedQuota, inc.ActualUsedQuota, inc.Difference, inc.UpstreamCost)
	}
	for _, inc := range result.LogInconsistencies {
		log.Printf("ledger/log inconsistency: ledger_count=%d, log_count=%d, count_diff=%d, quota_diff=%d",
			inc.LedgerCount, inc.LogCount, inc.CountDiff, inc.QuotaDiff)
	}

	if j.notifier == nil || result.DiscrepancyCount() == 0 {
		return
	}
	if err := j.dispatchAlerts(ctx, result); err != nil {
		log.Printf("reconciliation alert dispatch failed: %v", err)
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
		log.Printf("reconciliation alert sent: recipient=%s, discrepancies=%d", recipient, result.DiscrepancyCount())
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
