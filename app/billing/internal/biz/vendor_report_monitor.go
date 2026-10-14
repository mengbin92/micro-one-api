package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"micro-one-api/platform/metrics"
)

func WithVendorReportMonitoring(repo VendorReconciliationRepo) JobOption {
	return func(j *ReconciliationJob) { j.vendorReports = repo }
}

func (j *ReconciliationJob) monitorVendorReports(ctx context.Context, now time.Time) {
	if j.vendorReports == nil {
		return
	}
	var rows []VendorReconciliation
	var err error
	if repo, ok := j.vendorReports.(VendorReportMonitoringRepo); ok {
		rows, err = repo.ListVendorMonitoringReports(ctx, 100)
	} else {
		err = fmt.Errorf("complete monitoring scan unavailable")
	}
	metrics.VendorReportMonitor.WithLabelValues("unavailable").Set(0)
	var details strings.Builder
	if err != nil {
		metrics.VendorReportMonitor.WithLabelValues("unavailable").Set(1)
		details.WriteString("Vendor report coverage unavailable; inspect persistence or narrow the fixed window. No cost confirmation or expansion is permitted from this result.\n")
	} else {
		review := SummarizeVendorWeekly(rows, now)
		overdue := 0
		metrics.VendorReportMonitor.WithLabelValues("empty").Set(0)
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
			}
			if item.Status == "attention" || item.Status == "pending" && item.AgeSeconds >= int64(72*time.Hour/time.Second) {
				fmt.Fprintf(&details, "report=%s status=%s age_seconds=%d action=%s\n", item.ReportID, item.Status, item.AgeSeconds, item.NextAction)
			}
		}
		metrics.VendorReportMonitor.WithLabelValues("overdue").Set(float64(overdue))
	}
	if details.Len() == 0 || j.notifier == nil {
		return
	}
	for _, recipient := range j.recipients {
		if err := j.notifier.CreateNotification(ctx, j.notifyType, recipient, "[recon] vendor evidence needs review", details.String()); err != nil {
			metrics.VendorReportMonitor.WithLabelValues("notification_failed").Set(1)
			return
		}
	}
	metrics.VendorReportMonitor.WithLabelValues("notification_failed").Set(0)
}
