package biz

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func vendorFixture() (VendorManifest, []VendorBillRow, []VendorLocalAttempt) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	m := VendorManifest{Scope: VendorScope{Provider: "supplier", Account: "acct", Currency: "USD", Unit: "currency", Start: start, End: end, Timezone: "Asia/Shanghai", Granularity: "account_period"}, Version: "v1", EvidenceID: "bill-1", CostBasis: "invoice", Mode: "tokens", Complete: true, PagesComplete: true, VendorWatermark: end, LocalWatermark: end, CoverageEvidenceID: "all-pages-and-attempts", AbsoluteTolerance: "0", RelativeTolerance: "0", Actor: "operator", Reason: "trial", Mappings: []VendorSourceMapping{{SourceKind: "channel", SourceID: 1, Provider: "supplier", Account: "acct", Start: start, End: end, Revision: "map-v1", EvidenceID: "dedicated-account"}}}
	value := "0.000000000000000001"
	b := [5]int64{400, 600, 0, 0, 100}
	rows := []VendorBillRow{{Amount: &value, Buckets: &b}}
	a := []VendorLocalAttempt{{ID: "attempt", ReservationID: "res", SourceKind: "channel", SourceID: 1, Model: "vendor/Exact", RequestID: "req", Purpose: "user", ExecutedAt: start.Add(time.Hour), Complete: true, Buckets: &b, EstimatedAmount: &value, Currency: "USD", Unit: "currency", EvidenceIDs: []string{"terminal-digest"}}}
	return m, rows, a
}

func TestVendorFixedWindowAndExactAttemptPurchase(t *testing.T) {
	m, rows, a := vendorFixture()
	outside := a[0]
	outside.ID = "outside"
	outside.ExecutedAt = m.Scope.End
	a = append(a, outside)
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "matched", r.Status)
	require.Equal(t, "account_period", r.VerificationScope)
	require.Equal(t, "0", *r.Comparisons[0].Difference)
	require.Len(t, r.Comparisons[0].Attempts, 1)
	require.Equal(t, 1, r.UserReservations)
	require.Equal(t, *rows[0].Buckets, *r.Comparisons[0].LocalBuckets)
	// A duplicated attempt is an integrity failure, never counted twice.
	_, err = ReconcileVendor(m, rows, append(a, a[0]))
	require.Error(t, err)
}

func TestVendorZeroDenominatorBothKeySetsAndNoFalseSuccess(t *testing.T) {
	m, rows, a := vendorFixture()
	zero := "0"
	rows[0].Amount = &zero
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "attention", r.Status)
	require.Equal(t, "variance", r.Comparisons[0].AmountStatus)
	require.Nil(t, r.Comparisons[0].RelativeDifference)
	m.Scope.Granularity = "request"
	m.RequestIDKind = "header"
	m.RequestIDContractEvidenceID = "request-id-contract"
	rows[0].RequestID = "vendor-only"
	a[0].RequestID = "local-only"
	r, err = ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "attention", r.Status)
	require.Len(t, r.Comparisons, 2)
	require.Equal(t, "missing_vendor", r.Comparisons[0].Status)
	require.Equal(t, "missing_local", r.Comparisons[1].Status)
}

func TestVendorExplicitIdlePeriodZeroAndCostOnlyReport(t *testing.T) {
	m, rows, a := vendorFixture()
	rows[0].Buckets = nil
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "matched", r.Status)
	require.Equal(t, "not_applicable", r.Comparisons[0].UsageStatus)
	zero := "0"
	rows[0].Amount = &zero
	r, err = ReconcileVendor(m, rows, nil)
	require.NoError(t, err)
	require.Equal(t, "matched", r.Status)
	require.Equal(t, "0", *r.Comparisons[0].Difference)
}

func TestVendorCompletenessAndUnknownAmountsAreOrthogonal(t *testing.T) {
	for _, kind := range []string{"report", "pages", "vendor_watermark", "local_watermark", "coverage", "terminal", "execution_time", "mapping"} {
		t.Run(kind, func(t *testing.T) {
			m, rows, a := vendorFixture()
			switch kind {
			case "report":
				m.Complete = false
			case "pages":
				m.PagesComplete = false
			case "vendor_watermark":
				m.VendorWatermark = m.Scope.Start
			case "local_watermark":
				m.LocalWatermark = m.Scope.Start
			case "coverage":
				m.CoverageEvidenceID = ""
			case "terminal":
				a[0].Complete = false
			case "execution_time":
				a[0].ExecutedAt = time.Time{}
			case "mapping":
				a[0].SourceID = 2
			}
			r, err := ReconcileVendor(m, rows, a)
			require.NoError(t, err)
			require.Equal(t, "pending", r.Status)
			require.Equal(t, "none", r.VerificationScope)
		})
	}
	m, rows, a := vendorFixture()
	a[0].EstimatedAmount = nil
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, 1, r.Comparisons[0].UnknownAmounts)
	require.Nil(t, r.Comparisons[0].EstimatedAmount)
	require.Nil(t, r.Comparisons[0].Difference)
	require.Equal(t, "unknown", r.Comparisons[0].Status)
	// Invoice evidence for an account is independent of local estimation.
	require.Equal(t, "account_period", r.VerificationScope)
	rows[0].Amount = nil
	r, err = ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "none", r.VerificationScope)
}

func TestVendorInternalCostsCurrencyAndRepresentativeModes(t *testing.T) {
	m, rows, a := vendorFixture()
	a[0].Purpose = "canary"
	a[0].ReservationID = ""
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, 1, r.InternalAttempts)
	require.Zero(t, r.UserReservations)
	require.Equal(t, 1, r.Comparisons[0].PurposeCounts["canary"])
	a[0].Currency = "CNY"
	r, err = ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Nil(t, r.Comparisons[0].Difference)
	require.Equal(t, "unknown", r.Comparisons[0].Status)
	for _, mode := range []string{"monthly", "balance_only", "unknown"} {
		m.Mode = mode
		m.ContractEvidenceID = "contract"
		m.OverageEvidenceID = "no-overage-proof"
		r, err = ReconcileVendor(m, rows, a)
		require.NoError(t, err)
		require.Nil(t, r.Comparisons[0].Difference)
		if mode == "monthly" {
			require.Equal(t, "not_applicable", r.Status)
			require.Equal(t, "account_period", r.VerificationScope)
		} else {
			require.Equal(t, "attention", r.Status)
			require.Equal(t, "none", r.VerificationScope)
		}
	}
}

func TestVendorGranularityRequestCollisionsExactModelAndDecimalTolerance(t *testing.T) {
	m, rows, a := vendorFixture()
	m.Scope.Granularity = "request"
	m.RequestIDKind = "header"
	m.RequestIDContractEvidenceID = "request-id-contract"
	rows[0].RequestID = "req"
	m.Scope.Model = "vendor/exact"
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "missing_local", r.Comparisons[0].Status)
	m.Scope.Model = "vendor/Exact"
	copy := a[0]
	copy.ID = "another-attempt"
	a = append(a, copy)
	r, err = ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "pending", r.Status)
	require.Equal(t, "ambiguous", r.Comparisons[0].Status)
	a = a[:1]
	m.AbsoluteTolerance = "0.000000000000000001"
	_, err = ReconcileVendor(m, rows, a)
	require.Error(t, err)
	m.RoundingEvidenceID = "rounding-spec"
	two := "0.000000000000000002"
	rows[0].Amount = &two
	r, err = ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "matched", r.Status)
}

func TestVendorAttributionReconstructsDeltaWithoutHypothesesOrDoubleEvidence(t *testing.T) {
	delta := "10.000000000000000001"
	c := VendorComparison{Difference: &delta}
	items := []VendorAttribution{{Category: "internal", Amount: "3", Status: "confirmed", EvidenceID: "canary-invoice", Scope: "request"}, {Category: "retry_or_failover", Amount: "2", Status: "hypothesis", EvidenceID: "retry-hypothesis", Scope: "account_period"}}
	require.NoError(t, AttributeVendorDifference(&c, items))
	require.Equal(t, "7.000000000000000001", *c.Unexplained)
	items = append(items, items[0])
	require.Error(t, AttributeVendorDifference(&c, items))
	require.Error(t, AttributeVendorDifference(&VendorComparison{}, items[:1]))
}

func TestVendorCSVStrictPresenceDecimalAndScope(t *testing.T) {
	m, _, _ := vendorFixture()
	header := "provider,account,project,upstream_model_id,currency,unit,request_id,amount,uncached_input_tokens,cache_read_tokens,cache_creation_5m_tokens,cache_creation_1h_tokens,output_tokens\n"
	for _, line := range []string{"supplier,acct,,,USD,currency,,0,0,0,0,0,0\n", "supplier,acct,,,USD,currency,,,,,,,\n"} {
		rows, err := ReadVendorCSV(strings.NewReader(header+line), m.Scope)
		require.NoError(t, err)
		require.Len(t, rows, 1)
	}
	for _, line := range []string{"supplier,wrong,,,USD,currency,,0,0,0,0,0,0\n", "supplier,acct,,,USD,currency,,0,0,,,,\n", "supplier,acct,,,USD,currency,,NaN,0,0,0,0,0\n", "supplier,acct,,,USD,currency,,1e-4,0,0,0,0,0\n", "supplier,acct,,,USD,currency,,0,12junk,0,0,0,0\n"} {
		_, err := ReadVendorCSV(strings.NewReader(header+line), m.Scope)
		require.Error(t, err, line)
	}
}

func TestVendorMappingOverlapAndAuditedHistoricalExclusion(t *testing.T) {
	m, rows, a := vendorFixture()
	m.Mappings = append(m.Mappings, m.Mappings[0])
	require.Error(t, ValidateVendorManifest(m))
	m.Mappings = m.Mappings[:1]
	old := a[0]
	old.ID = "old"
	old.ExecutedAt = time.Time{}
	a = append(a, old)
	m.ExcludedAttempts = []VendorExcludedAttempt{{ID: "old", Reason: "independently proven outside window", EvidenceID: "old-reservation-window"}}
	r, err := ReconcileVendor(m, rows, a)
	require.NoError(t, err)
	require.Equal(t, "matched", r.Status)
}

func TestVendorScopeDigestUsesInstantsAcrossTimezoneRepresentations(t *testing.T) {
	m, rows, _ := vendorFixture()
	shifted := m
	zone, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	shifted.Scope.Start = shifted.Scope.Start.In(zone)
	shifted.Scope.End = shifted.Scope.End.In(zone)
	shifted.VendorWatermark = shifted.VendorWatermark.In(zone)
	require.Equal(t, VendorReportID(m, rows), VendorReportID(shifted, rows))
}

func TestVendorDailyNeverDuplicatesInternalAttemptsAcrossScopes(t *testing.T) {
	m, rows, attempts := vendorFixture()
	attempts[0].Purpose = "canary"
	attempts[0].ReservationID = ""
	r, err := ReconcileVendor(m, rows, attempts)
	require.NoError(t, err)
	other := r
	other.Manifest.Scope.Model = attempts[0].Model
	summary := SummarizeVendorDaily([]VendorReconciliation{r, other})
	require.Len(t, summary, 1)
	require.Equal(t, 2, summary[0].Reports)
	require.Equal(t, 1, summary[0].InternalAttempts)
}
