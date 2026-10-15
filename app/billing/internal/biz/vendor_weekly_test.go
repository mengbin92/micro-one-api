package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"micro-one-api/platform/metrics"
)

type vendorMonitorRepo struct {
	rows []VendorReconciliation
	err  error
}

func (r *vendorMonitorRepo) ListVendorMonitoringReports(context.Context, int) ([]VendorReconciliation, error) {
	return r.rows, r.err
}

func (r *vendorMonitorRepo) LoadVendorAttempts(context.Context, VendorManifest) ([]VendorLocalAttempt, error) {
	return nil, r.err
}
func (r *vendorMonitorRepo) StoreVendorReconciliation(context.Context, *VendorReconciliation) error {
	return r.err
}
func (r *vendorMonitorRepo) ListVendorReconciliations(context.Context, time.Time, time.Time, int) ([]VendorReconciliation, error) {
	return r.rows, r.err
}

func TestWeeklyReviewRanksKnownCurrencyDifferencesAndOldestPending(t *testing.T) {
	m, _, _ := vendorFixture()
	asOf := m.Scope.End.Add(8 * 24 * time.Hour)
	small, large := "-0.01", "2"
	rows := []VendorReconciliation{
		{ID: "small", Manifest: m, Status: "attention", Comparisons: []VendorComparison{{Unexplained: &small}}},
		{ID: "large", Manifest: m, Status: "attention", Comparisons: []VendorComparison{{Unexplained: &large, UnknownAmounts: 3}}},
		{ID: "pending", Manifest: m, Status: "pending"},
	}
	r := SummarizeVendorWeekly(rows, asOf)
	require.Equal(t, "large", r.Items[0].ReportID)
	require.Equal(t, 3, r.UnknownAmounts)
	require.Equal(t, 2, r.Attention)
	require.Equal(t, 1, r.Pending)
	require.Equal(t, int64(8*24*3600), r.Items[2].AgeSeconds)
	require.Equal(t, "complete_watermarks_or_late_evidence", r.Items[2].NextAction)
}
func TestVendorMonitorFaultInjectionAndNotificationFailure(t *testing.T) {
	m, _, _ := vendorFixture()
	now := m.Scope.End.Add(4 * 24 * time.Hour)
	r := &vendorMonitorRepo{rows: []VendorReconciliation{{ID: "overdue", Manifest: m, Status: "pending"}}}
	n := &recordingNotifier{}
	j := newJobHarness(t, &mockReconRepo{}, n, nil, "")
	j.vendorReports = r
	j.monitorVendorReports(context.Background(), now)
	require.Len(t, n.calls, 1)
	require.Contains(t, n.calls[0].content, "overdue")
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("pending")))
	r.err = errors.New("database failure must not leak credentials")
	j.monitorVendorReports(context.Background(), now)
	require.Len(t, n.calls, 2)
	require.NotContains(t, n.calls[1].content, "credentials")
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("unavailable")))
	r.err = nil
	n.err = errors.New("notifier down")
	j.monitorVendorReports(context.Background(), now)
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("notification_failed")))
}
