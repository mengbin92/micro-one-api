package biz

import (
	"bytes"
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
