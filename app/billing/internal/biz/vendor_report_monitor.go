package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

func WithVendorReportMonitoring(repo VendorReconciliationRepo) JobOption {
	return func(j *ReconciliationJob) { j.vendorReports = repo }
}

func WithVendorReportExpectations(expectations VendorReportExpectations) JobOption {
	return func(j *ReconciliationJob) {
		j.vendorExpectations = expectations
		j.vendorExpectations.Reports = append([]VendorExpectedReport(nil), expectations.Reports...)
		j.vendorExpectations.ReviewExceptions = append([]VendorReportReviewException(nil), expectations.ReviewExceptions...)
	}
}

func (j *ReconciliationJob) monitorVendorReports(ctx context.Context, now time.Time) {
	if j.vendorReports == nil {
		return
	}
	interval := j.checkInterval
	if interval <= 0 {
		interval = time.Hour
	}
	metrics.VendorReportNextScanDeadline.Set(float64(now.Add(interval).Add(5 * time.Minute).Unix()))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	metrics.VendorReportMonitor.WithLabelValues("unavailable").Set(1)
	for _, state := range []string{"empty", "pending", "attention", "unknown", "overdue", "reviewed_overdue", "notification_failed", "expected", "missing", "missing_overdue", "expectations_unconfigured", "expectations_expired"} {
		metrics.VendorReportMonitor.WithLabelValues(state).Set(0)
	}
	var details strings.Builder
	expectationsValid := ValidateVendorReportExpectations(j.vendorExpectations) == nil
	if !expectationsValid {
		metrics.VendorReportMonitor.WithLabelValues("expectations_unconfigured").Set(1)
		details.WriteString("Vendor report expectations are unconfigured or invalid; register the expected account periods and deadlines. Expected supplier cost coverage is unknown.\n")
	} else {
		metrics.VendorReportMonitor.WithLabelValues("expected").Set(float64(len(j.vendorExpectations.Reports)))
		if !now.Before(j.vendorExpectations.ValidUntil) {
			metrics.VendorReportMonitor.WithLabelValues("expectations_expired").Set(1)
			fmt.Fprintf(&details, "Vendor report expectations expired at %s; register the next account periods before claiming complete coverage.\n", j.vendorExpectations.ValidUntil.UTC().Format(time.RFC3339))
		}
	}
	var rows []VendorReconciliation
	var err error
	if repo, ok := j.vendorReports.(VendorReportMonitoringRepo); ok {
		rows, err = repo.ListVendorMonitoringReports(ctx, 100)
	} else {
		err = fmt.Errorf("complete monitoring scan unavailable")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		details.WriteString("Vendor report coverage unavailable; inspect persistence or narrow the fixed window. No cost confirmation or expansion is permitted from this result.\n")
	} else {
		review := SummarizeVendorWeekly(rows, now)
		reviewedReports := make(map[string]bool)
		if expectationsValid && now.Before(j.vendorExpectations.ValidUntil) {
			for _, row := range rows {
				if row.Status != "pending" || now.Sub(row.Manifest.Scope.End) < 72*time.Hour {
					continue
				}
				for _, exception := range j.vendorExpectations.ReviewExceptions {
					if exception.ReportID != row.ID || now.Before(exception.ReviewedAt) || !now.Before(exception.ExpiresAt) {
						continue
					}
					// Persistence identifies immutable runs by the canonical report payload SHA.
					raw, err := jsonx.Marshal(row)
					if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != exception.RunID {
						continue
					}
					reviewedReports[row.ID] = true
					applogger.Log.Info("vendor evidence review exception applied", zap.String("report_id", row.ID), zap.String("run_id", exception.RunID), zap.String("reviewed_by", exception.ReviewedBy), zap.String("reason", exception.Reason), zap.String("evidence_id", exception.EvidenceID), zap.Time("reviewed_at", exception.ReviewedAt), zap.Time("expires_at", exception.ExpiresAt))
				}
			}
		}
		overdue, reviewedOverdue := 0, 0
		if len(rows) == 0 {
			metrics.VendorReportMonitor.WithLabelValues("empty").Set(1)
			details.WriteString("No imported vendor reports in the bounded monitor window; supplier cost coverage is unknown.\n")
		}
		metrics.VendorReportMonitor.WithLabelValues("pending").Set(float64(review.Pending))
		metrics.VendorReportMonitor.WithLabelValues("attention").Set(float64(review.Attention))
		metrics.VendorReportMonitor.WithLabelValues("unknown").Set(float64(review.UnknownAmounts))
		for _, item := range review.Items {
			if item.Status == "pending" && item.AgeSeconds >= int64(72*time.Hour/time.Second) {
				overdue++
				if reviewedReports[item.ReportID] {
					reviewedOverdue++
					continue
				}
			}
			if item.Status == "attention" || item.Status == "pending" && item.AgeSeconds >= int64(72*time.Hour/time.Second) {
				fmt.Fprintf(&details, "report=%s status=%s age_seconds=%d action=%s\n", item.ReportID, item.Status, item.AgeSeconds, item.NextAction)
			}
		}
		metrics.VendorReportMonitor.WithLabelValues("overdue").Set(float64(overdue))
		metrics.VendorReportMonitor.WithLabelValues("reviewed_overdue").Set(float64(reviewedOverdue))
		if expectationsValid {
			metrics.VendorReportMonitor.WithLabelValues("unavailable").Set(0)
			missing := MissingVendorReports(j.vendorExpectations, rows)
			metrics.VendorReportMonitor.WithLabelValues("missing").Set(float64(len(missing)))
			missingOverdue := 0
			for _, expected := range missing {
				if now.Before(expected.DueAt) {
					continue
				}
				missingOverdue++
				scope := expected.Scope
				fmt.Fprintf(&details, "missing_vendor_report provider=%q account=%q project=%q model=%q currency=%q unit=%q timezone=%q granularity=%q start=%s end=%s due_at=%s action=import_independent_supplier_report\n", scope.Provider, scope.Account, scope.Project, scope.Model, scope.Currency, scope.Unit, scope.Timezone, scope.Granularity, scope.Start.UTC().Format(time.RFC3339), scope.End.UTC().Format(time.RFC3339), expected.DueAt.UTC().Format(time.RFC3339))
			}
			metrics.VendorReportMonitor.WithLabelValues("missing_overdue").Set(float64(missingOverdue))
		}
	}
	if details.Len() == 0 {
		return
	}
	applogger.Log.Warn("vendor evidence needs review", zap.String("details", details.String()))
	if j.notifier == nil {
		return
	}
	for _, recipient := range j.recipients {
		if err := j.notifier.CreateNotification(ctx, j.notifyType, recipient, "[recon] vendor evidence needs review", details.String()); err != nil {
			metrics.VendorReportMonitor.WithLabelValues("notification_failed").Set(1)
			return
		}
	}
}
