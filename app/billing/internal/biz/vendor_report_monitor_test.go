package biz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

func TestVendorMonitorReviewExceptionIsBoundToOnePendingRunAndExpiry(t *testing.T) {
	for _, scenario := range []string{"approved", "changed_run", "other_report", "expired", "future_review", "attention", "invalid_inventory", "expired_inventory", "missing_report"} {
		t.Run(scenario, func(t *testing.T) {
			core, logs := observer.New(zap.InfoLevel)
			applogger.SetLoggerForTest(t, zap.New(core))
			m, _, _ := vendorFixture()
			now := m.Scope.End.Add(96 * time.Hour)
			row := VendorReconciliation{ID: strings.Repeat("a", 64), Manifest: m, Status: "pending", VerificationScope: "none", Comparisons: []VendorComparison{{AmountStatus: "unknown"}}}
			raw, err := jsonx.Marshal(row)
			require.NoError(t, err)
			review := VendorReportReviewException{
				ReportID: row.ID, RunID: fmt.Sprintf("%x", sha256.Sum256(raw)),
				ReviewedBy: "maintainer", Reason: "accepted historical execution evidence gap",
				EvidenceID: "sha256:" + strings.Repeat("e", 64),
				ReviewedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
			}
			expectations := VendorReportExpectations{ValidUntil: now.Add(24 * time.Hour), Reports: []VendorExpectedReport{{Scope: m.Scope, DueAt: m.Scope.End.Add(72 * time.Hour)}}, ReviewExceptions: []VendorReportReviewException{review}}
			switch scenario {
			case "changed_run":
				row.Manifest.Reason += " new evidence"
			case "other_report":
				row.ID = strings.Repeat("b", 64)
			case "expired":
				expectations.ReviewExceptions[0].ExpiresAt = now
			case "future_review":
				expectations.ReviewExceptions[0].ReviewedAt = now.Add(time.Second)
			case "attention":
				row.Status = "attention"
				raw, err = jsonx.Marshal(row)
				require.NoError(t, err)
				expectations.ReviewExceptions[0].RunID = fmt.Sprintf("%x", sha256.Sum256(raw))
			case "invalid_inventory":
				expectations.Reports[0].Scope.Timezone = "invalid"
			case "expired_inventory":
				expectations.ValidUntil = now
				expectations.ReviewExceptions[0].ExpiresAt = now
			}
			repo := &vendorMonitorRepo{rows: []VendorReconciliation{row}}
			if scenario == "missing_report" {
				repo.rows = nil
			}
			notifier := &recordingNotifier{}
			j := newJobHarness(t, &mockReconRepo{}, notifier, nil, "")
			WithVendorReportMonitoring(repo)(j)
			WithVendorReportExpectations(expectations)(j)
			expectations.ReviewExceptions[0].RunID = strings.Repeat("c", 64)
			j.monitorVendorReports(context.Background(), now)
			if scenario == "approved" {
				require.Empty(t, notifier.calls)
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("reviewed_overdue")))
				audit := logs.FilterMessage("vendor evidence review exception applied").All()
				require.Len(t, audit, 1)
				require.Equal(t, review.RunID, audit[0].ContextMap()["run_id"])
				require.Equal(t, review.ReviewedBy, audit[0].ContextMap()["reviewed_by"])
			} else {
				require.Len(t, notifier.calls, 1)
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("reviewed_overdue")))
			}
			if scenario != "attention" && scenario != "missing_report" {
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("pending")))
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("overdue")))
			}
			require.Equal(t, "unknown", row.Comparisons[0].AmountStatus)
			require.Equal(t, "none", row.VerificationScope)
		})
	}
}

func TestVendorMonitorMissingAccountDeadlineAndLateImport(t *testing.T) {
	m, _, _ := vendorFixture()
	due := m.Scope.End.Add(72 * time.Hour)
	missing := m.Scope
	missing.Account = "second-account"
	expectations := VendorReportExpectations{
		ValidUntil: due.Add(24 * time.Hour),
		Reports: []VendorExpectedReport{
			{Scope: m.Scope, DueAt: due},
			{Scope: missing, DueAt: due},
		},
	}
	repo := &vendorMonitorRepo{rows: []VendorReconciliation{{ID: "received", Manifest: m, Status: "matched"}}}
	notifier := &recordingNotifier{}
	j := newJobHarness(t, &mockReconRepo{}, notifier, nil, "")
	WithVendorReportMonitoring(repo)(j)
	WithVendorReportExpectations(expectations)(j)
	expectations.Reports[1].Scope.Account = "caller-mutation"

	j.monitorVendorReports(context.Background(), due.Add(-time.Nanosecond))
	require.Empty(t, notifier.calls)
	require.Equal(t, float64(2), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("expected")))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing")))
	require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing_overdue")))

	j.monitorVendorReports(context.Background(), due)
	require.Len(t, notifier.calls, 1)
	require.Contains(t, notifier.calls[0].content, `account="second-account"`)
	require.Contains(t, notifier.calls[0].content, "action=import_independent_supplier_report")
	require.NotContains(t, notifier.calls[0].content, "caller-mutation")
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing_overdue")))

	late := m
	late.Scope = missing
	repo.rows = append(repo.rows, VendorReconciliation{ID: "late", Manifest: late, Status: "pending"})
	j.monitorVendorReports(context.Background(), due.Add(time.Second))
	require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing")))
	require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing_overdue")))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("pending")))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("overdue")))
	// Receipt clears missing coverage; pending finality remains independently overdue.
	require.Len(t, notifier.calls, 2)
	require.NotContains(t, notifier.calls[1].content, "missing_vendor_report")
}

func TestVendorMonitorExpectationConfigurationFailuresStayVisibleWithoutNotifier(t *testing.T) {
	m, _, _ := vendorFixture()
	due := m.Scope.End.Add(72 * time.Hour)
	for _, kind := range []string{"unconfigured", "invalid", "expired", "empty", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			applogger.SetLoggerForTest(t, zap.New(core))
			expectations := VendorReportExpectations{
				ValidUntil: due.Add(time.Hour),
				Reports:    []VendorExpectedReport{{Scope: m.Scope, DueAt: due}},
			}
			repo := &vendorMonitorRepo{rows: []VendorReconciliation{{ID: "received", Manifest: m, Status: "matched"}}}
			switch kind {
			case "unconfigured":
				expectations = VendorReportExpectations{}
			case "invalid":
				expectations.Reports[0].Scope.Timezone = "invalid/timezone"
			case "expired":
				expectations.ValidUntil = due
			case "empty":
				repo.rows = nil
			case "unavailable":
				repo.err = errors.New("private storage details")
			}
			j := newJobHarness(t, &mockReconRepo{}, nil, nil, "")
			WithVendorReportMonitoring(repo)(j)
			WithVendorReportExpectations(expectations)(j)
			j.monitorVendorReports(context.Background(), due)
			require.Len(t, logs.All(), 1)
			require.Equal(t, "vendor evidence needs review", logs.All()[0].Message)
			require.NotContains(t, logs.All()[0].ContextMap()["details"], "private storage details")
			if kind == "unconfigured" || kind == "invalid" {
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("expectations_unconfigured")))
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("unavailable")))
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing")))
			} else {
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("expectations_unconfigured")))
			}
			if kind == "expired" {
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("expectations_expired")))
			} else {
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("expectations_expired")))
			}
			if kind == "empty" {
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("empty")))
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing_overdue")))
			}
			if kind == "unavailable" {
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("unavailable")))
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing")))
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing_overdue")))
			}
			require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("notification_failed")))
		})
	}
}

func TestVendorMonitorScanStaysUnavailableUntilCompleteAndHonorsCancellation(t *testing.T) {
	m, _, _ := vendorFixture()
	due := m.Scope.End.Add(72 * time.Hour)
	for _, cancelled := range []bool{false, true} {
		name := "complete"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			repo := &vendorMonitorRepo{scan: func(ctx context.Context, _ int) ([]VendorReconciliation, error) {
				entered <- ctx
				select {
				case <-release:
					return []VendorReconciliation{{ID: "received", Manifest: m, Status: "matched"}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
			j := newJobHarness(t, &mockReconRepo{}, nil, nil, "")
			WithVendorReportMonitoring(repo)(j)
			WithVendorReportExpectations(VendorReportExpectations{
				ValidUntil: due.Add(time.Hour),
				Reports:    []VendorExpectedReport{{Scope: m.Scope, DueAt: due}},
			})(j)
			metrics.VendorReportMonitor.WithLabelValues("unavailable").Set(0)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan struct{})
			go func() {
				j.monitorVendorReports(ctx, due)
				close(done)
			}()
			var scanContext context.Context
			select {
			case scanContext = <-entered:
			case <-time.After(time.Second):
				t.Fatal("vendor scan did not start")
			}
			deadline, bounded := scanContext.Deadline()
			require.True(t, bounded)
			require.Positive(t, time.Until(deadline))
			require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
			require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("unavailable")))
			if cancelled {
				cancel()
			} else {
				close(release)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("vendor scan did not return after completion or cancellation")
			}
			if cancelled {
				require.Equal(t, float64(1), testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("unavailable")))
			} else {
				require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("unavailable")))
			}
			require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing")))
			require.Zero(t, testutil.ToFloat64(metrics.VendorReportMonitor.WithLabelValues("missing_overdue")))
		})
	}
}

func TestVendorMonitorNextScanDeadlineUsesIntervalAndAdvances(t *testing.T) {
	m, _, _ := vendorFixture()
	now := m.Scope.End.Add(72 * time.Hour)
	for _, interval := range []time.Duration{0, 15 * time.Minute} {
		t.Run(interval.String(), func(t *testing.T) {
			j := newJobHarness(t, &mockReconRepo{}, nil, nil, "")
			j.checkInterval = interval
			WithVendorReportMonitoring(&vendorMonitorRepo{rows: []VendorReconciliation{{ID: "received", Manifest: m, Status: "matched"}}})(j)
			WithVendorReportExpectations(VendorReportExpectations{
				ValidUntil: now.Add(24 * time.Hour),
				Reports:    []VendorExpectedReport{{Scope: m.Scope, DueAt: now}},
			})(j)
			effectiveInterval := interval
			if effectiveInterval <= 0 {
				effectiveInterval = time.Hour
			}
			j.monitorVendorReports(context.Background(), now)
			require.Equal(t, float64(now.Add(effectiveInterval).Add(5*time.Minute).Unix()), testutil.ToFloat64(metrics.VendorReportNextScanDeadline))
			j.monitorVendorReports(context.Background(), now.Add(5*time.Minute))
			require.Equal(t, float64(now.Add(effectiveInterval).Add(10*time.Minute).Unix()), testutil.ToFloat64(metrics.VendorReportNextScanDeadline))
		})
	}
}
