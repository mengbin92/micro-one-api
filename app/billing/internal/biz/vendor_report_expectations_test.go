package biz

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func vendorExpectationsFixture() VendorReportExpectations {
	m, _, _ := vendorFixture()
	due := m.Scope.End.Add(72 * time.Hour)
	return VendorReportExpectations{ValidUntil: due.Add(24 * time.Hour), Reports: []VendorExpectedReport{{Scope: m.Scope, DueAt: due}}}
}

func TestDecodeVendorReportExpectationsStrictAndBounded(t *testing.T) {
	e := vendorExpectationsFixture()
	raw, err := jsonx.Marshal(e)
	require.NoError(t, err)
	decoded, err := DecodeVendorReportExpectations(raw)
	require.NoError(t, err)
	require.Equal(t, e, decoded)
	for name, input := range map[string][]byte{
		"empty":         nil,
		"null":          []byte("null"),
		"malformed":     []byte("{"),
		"empty_object":  []byte("{}"),
		"unknown_field": bytes.Replace(raw, []byte(`"reports":`), []byte(`"unexpected":1,"reports":`), 1),
		"duplicate_key": bytes.Replace(raw, []byte(`"reports":`), []byte(`"reports":[],"reports":`), 1),
		"trailing_json": append(append([]byte{}, raw...), []byte(" {}")...),
		"oversized":     bytes.Repeat([]byte(" "), (2<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeVendorReportExpectations(input)
			require.Error(t, err)
		})
	}
}

func TestDecodeVendorReportReviewException(t *testing.T) {
	e := vendorExpectationsFixture()
	raw, err := jsonx.Marshal(map[string]any{
		"valid_until": e.ValidUntil,
		"reports":     e.Reports,
		"review_exceptions": []map[string]any{{
			"report_id":   strings.Repeat("a", 64),
			"run_id":      strings.Repeat("b", 64),
			"reviewed_by": "maintainer",
			"reason":      "accepted historical execution coverage gap",
			"evidence_id": "sha256:" + strings.Repeat("c", 64),
			"reviewed_at": e.Reports[0].DueAt,
			"expires_at":  e.ValidUntil,
		}},
	})
	require.NoError(t, err)
	decoded, err := DecodeVendorReportExpectations(raw)
	require.NoError(t, err)
	require.Equal(t, []VendorReportReviewException{vendorReportReviewFixture(e)}, decoded.ReviewExceptions)
	for name, input := range map[string][]byte{
		"unknown_review_field": bytes.Replace(raw, []byte(`"reason":`), []byte(`"unexpected":true,"reason":`), 1),
		"duplicate_review_key": bytes.Replace(raw, []byte(`"reason":`), []byte(`"reason":"duplicate","reason":`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeVendorReportExpectations(input)
			require.Error(t, err)
		})
	}
}

func vendorReportReviewFixture(e VendorReportExpectations) VendorReportReviewException {
	return VendorReportReviewException{
		ReportID: strings.Repeat("a", 64), RunID: strings.Repeat("b", 64),
		ReviewedBy: "maintainer", Reason: "accepted historical execution coverage gap",
		EvidenceID: "sha256:" + strings.Repeat("c", 64),
		ReviewedAt: e.Reports[0].DueAt, ExpiresAt: e.ValidUntil,
	}
}

func TestValidateVendorReportReviewExceptions(t *testing.T) {
	for name, mutate := range map[string]func(*VendorReportExpectations){
		"too_many": func(e *VendorReportExpectations) {
			e.ReviewExceptions = make([]VendorReportReviewException, 101)
		},
		"empty_report":     func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReportID = "" },
		"short_report":     func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReportID = strings.Repeat("a", 63) },
		"uppercase_report": func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReportID = strings.Repeat("A", 64) },
		"invalid_report":   func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReportID = strings.Repeat("g", 64) },
		"empty_run":        func(e *VendorReportExpectations) { e.ReviewExceptions[0].RunID = "" },
		"long_run":         func(e *VendorReportExpectations) { e.ReviewExceptions[0].RunID = strings.Repeat("b", 65) },
		"uppercase_run":    func(e *VendorReportExpectations) { e.ReviewExceptions[0].RunID = strings.Repeat("B", 64) },
		"invalid_run":      func(e *VendorReportExpectations) { e.ReviewExceptions[0].RunID = strings.Repeat("g", 64) },
		"missing_reviewer": func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReviewedBy = " \t" },
		"long_reviewer":    func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReviewedBy = strings.Repeat("a", 129) },
		"missing_reason":   func(e *VendorReportExpectations) { e.ReviewExceptions[0].Reason = "\n " },
		"long_reason":      func(e *VendorReportExpectations) { e.ReviewExceptions[0].Reason = strings.Repeat("a", 1025) },
		"missing_evidence": func(e *VendorReportExpectations) { e.ReviewExceptions[0].EvidenceID = "" },
		"evidence_prefix":  func(e *VendorReportExpectations) { e.ReviewExceptions[0].EvidenceID = strings.Repeat("a", 64) },
		"evidence_short": func(e *VendorReportExpectations) {
			e.ReviewExceptions[0].EvidenceID = "sha256:" + strings.Repeat("a", 63)
		},
		"evidence_uppercase": func(e *VendorReportExpectations) {
			e.ReviewExceptions[0].EvidenceID = "sha256:" + strings.Repeat("A", 64)
		},
		"review_zero":  func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReviewedAt = time.Time{} },
		"review_epoch": func(e *VendorReportExpectations) { e.ReviewExceptions[0].ReviewedAt = time.Unix(0, 0) },
		"expiry_zero":  func(e *VendorReportExpectations) { e.ReviewExceptions[0].ExpiresAt = time.Time{} },
		"expiry_equal_review": func(e *VendorReportExpectations) {
			e.ReviewExceptions[0].ExpiresAt = e.ReviewExceptions[0].ReviewedAt
		},
		"expiry_before_review": func(e *VendorReportExpectations) {
			e.ReviewExceptions[0].ExpiresAt = e.ReviewExceptions[0].ReviewedAt.Add(-time.Nanosecond)
		},
		"expiry_after_inventory": func(e *VendorReportExpectations) {
			e.ReviewExceptions[0].ExpiresAt = e.ValidUntil.Add(time.Nanosecond)
		},
		"duplicate_report": func(e *VendorReportExpectations) {
			duplicate := e.ReviewExceptions[0]
			duplicate.RunID = strings.Repeat("c", 64)
			e.ReviewExceptions = append(e.ReviewExceptions, duplicate)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := vendorExpectationsFixture()
			e.ReviewExceptions = []VendorReportReviewException{vendorReportReviewFixture(e)}
			mutate(&e)
			require.Error(t, ValidateVendorReportExpectations(e))
		})
	}
	e := vendorExpectationsFixture()
	for i := range 100 {
		review := vendorReportReviewFixture(e)
		review.ReportID = fmt.Sprintf("%064x", i+1)
		review.ReviewedBy = strings.Repeat("a", 128)
		review.Reason = strings.Repeat("a", 1024)
		e.ReviewExceptions = append(e.ReviewExceptions, review)
	}
	require.NoError(t, ValidateVendorReportExpectations(e), "exact limits and inventory expiry equality are allowed")
	raw, err := jsonx.Marshal(e)
	require.NoError(t, err)
	decoded, err := DecodeVendorReportExpectations(raw)
	require.NoError(t, err)
	require.Equal(t, e, decoded)
}

func TestValidateVendorReportExpectations(t *testing.T) {
	for name, mutate := range map[string]func(*VendorReportExpectations){
		"expiry":       func(e *VendorReportExpectations) { e.ValidUntil = time.Time{} },
		"empty":        func(e *VendorReportExpectations) { e.Reports = nil },
		"too_many":     func(e *VendorReportExpectations) { e.Reports = make([]VendorExpectedReport, 101) },
		"provider":     func(e *VendorReportExpectations) { e.Reports[0].Scope.Provider = " " },
		"account":      func(e *VendorReportExpectations) { e.Reports[0].Scope.Account = "" },
		"long_account": func(e *VendorReportExpectations) { e.Reports[0].Scope.Account = strings.Repeat("a", 256) },
		"long_model":   func(e *VendorReportExpectations) { e.Reports[0].Scope.Model = strings.Repeat("m", 256) },
		"currency":     func(e *VendorReportExpectations) { e.Reports[0].Scope.Currency = " " },
		"unit":         func(e *VendorReportExpectations) { e.Reports[0].Scope.Unit = "" },
		"timezone":     func(e *VendorReportExpectations) { e.Reports[0].Scope.Timezone = "bad/timezone" },
		"empty_zone":   func(e *VendorReportExpectations) { e.Reports[0].Scope.Timezone = "" },
		"granularity":  func(e *VendorReportExpectations) { e.Reports[0].Scope.Granularity = "day" },
		"start":        func(e *VendorReportExpectations) { e.Reports[0].Scope.Start = time.Time{} },
		"end":          func(e *VendorReportExpectations) { e.Reports[0].Scope.End = e.Reports[0].Scope.Start },
		"period_limit": func(e *VendorReportExpectations) {
			e.Reports[0].Scope.End = e.Reports[0].Scope.Start.Add(367 * 24 * time.Hour)
		},
		"due_zero": func(e *VendorReportExpectations) { e.Reports[0].DueAt = time.Time{} },
		"due_before_end": func(e *VendorReportExpectations) {
			e.Reports[0].DueAt = e.Reports[0].Scope.End.Add(-time.Nanosecond)
		},
		"expiry_before_due": func(e *VendorReportExpectations) { e.ValidUntil = e.Reports[0].DueAt.Add(-time.Nanosecond) },
		"duplicate_scope": func(e *VendorReportExpectations) {
			copy := e.Reports[0]
			copy.Scope.Start = copy.Scope.Start.In(time.FixedZone("offset", 8*3600))
			copy.Scope.End = copy.Scope.End.In(time.FixedZone("offset", 8*3600))
			e.Reports = append(e.Reports, copy)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := vendorExpectationsFixture()
			mutate(&e)
			require.Error(t, ValidateVendorReportExpectations(e))
		})
	}
	e := vendorExpectationsFixture()
	e.Reports[0].DueAt = e.Reports[0].Scope.End
	e.ValidUntil = e.Reports[0].DueAt
	require.NoError(t, ValidateVendorReportExpectations(e), "deadline equality is allowed")
	e.Reports[0].Scope.Granularity = "request"
	require.NoError(t, ValidateVendorReportExpectations(e))
}

func TestMissingVendorReportsTracksEachDeclaredScopeAndLateImport(t *testing.T) {
	e := vendorExpectationsFixture()
	other := e.Reports[0]
	other.Scope.Account = "other-account"
	e.Reports = append(e.Reports, other)
	rows := []VendorReconciliation{{Manifest: VendorManifest{Scope: e.Reports[0].Scope}, Status: "pending"}}
	require.Equal(t, []VendorExpectedReport{other}, MissingVendorReports(e, rows))
	require.Equal(t, "pending", rows[0].Status, "presence must not promote reconciliation status")
	rows = append(rows, VendorReconciliation{Manifest: VendorManifest{Scope: other.Scope}, Status: "attention"})
	require.Empty(t, MissingVendorReports(e, rows), "late import clears only the absence check")
	require.Equal(t, e.Reports, MissingVendorReports(e, nil))
}

func TestMissingVendorReportsRequiresExactCanonicalScope(t *testing.T) {
	for name, mutate := range map[string]func(*VendorScope){
		"provider":    func(s *VendorScope) { s.Provider = "other" },
		"account":     func(s *VendorScope) { s.Account = "other" },
		"project":     func(s *VendorScope) { s.Project = "other" },
		"model":       func(s *VendorScope) { s.Model = "other" },
		"currency":    func(s *VendorScope) { s.Currency = "other" },
		"unit":        func(s *VendorScope) { s.Unit = "other" },
		"timezone":    func(s *VendorScope) { s.Timezone = "UTC" },
		"granularity": func(s *VendorScope) { s.Granularity = "request" },
		"start":       func(s *VendorScope) { s.Start = s.Start.Add(-time.Second) },
		"end":         func(s *VendorScope) { s.End = s.End.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			e := vendorExpectationsFixture()
			scope := e.Reports[0].Scope
			mutate(&scope)
			rows := []VendorReconciliation{{Manifest: VendorManifest{Scope: scope}}}
			require.Equal(t, e.Reports, MissingVendorReports(e, rows))
		})
	}
	e := vendorExpectationsFixture()
	scope := e.Reports[0].Scope
	scope.Start = scope.Start.In(time.FixedZone("offset", -7*3600))
	scope.End = scope.End.In(time.FixedZone("offset", -7*3600))
	require.Empty(t, MissingVendorReports(e, []VendorReconciliation{{Manifest: VendorManifest{Scope: scope}}}), "UTC offsets do not change the instant")
}
