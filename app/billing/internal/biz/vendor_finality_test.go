package biz

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func vendorFinalityFixture() (VendorManifest, []VendorBillRow) {
	m, rows, _ := vendorFixture()
	m.CostBasis = "provider_reported"
	m.FinalityAttestation = &VendorFinalityAttestation{
		Kind: "maintainer_confirmation", EvidenceID: "sha256:" + strings.Repeat("a", 64),
		ConfirmedAt: m.Scope.End.Add(time.Hour), Statement: "no_further_corrections",
	}
	return m, rows
}

func TestVendorFinalityAttestationExplicitPeriodConfirmation(t *testing.T) {
	m, _ := vendorFinalityFixture()
	require.NoError(t, ValidateVendorFinalityAttestation(m))
	require.NoError(t, ValidateVendorManifest(m))
	// Confirmation at the exact half-open period end is sufficient; changing
	// the timezone representation does not move the confirmed instant.
	m.FinalityAttestation.ConfirmedAt = m.Scope.End.In(time.FixedZone("CST", 8*60*60))
	require.NoError(t, ValidateVendorFinalityAttestation(m))
	m.FinalityAttestation = nil
	m.Complete = false
	m.VendorWatermark = time.Time{}
	require.NoError(t, ValidateVendorFinalityAttestation(m), "legacy and pending manifests do not require an attestation")
}

func TestVendorFinalityAttestationRejectsUnboundedOrStrongerClaims(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*VendorManifest)
	}{
		{"provider_claim", func(m *VendorManifest) { m.FinalityAttestation.Kind = "provider_published" }},
		{"signed_claim", func(m *VendorManifest) { m.FinalityAttestation.Kind = "signed_invoice" }},
		{"missing_statement", func(m *VendorManifest) { m.FinalityAttestation.Statement = "" }},
		{"different_statement", func(m *VendorManifest) { m.FinalityAttestation.Statement = "no_changes_expected" }},
		{"missing_digest", func(m *VendorManifest) { m.FinalityAttestation.EvidenceID = "" }},
		{"unqualified_digest", func(m *VendorManifest) { m.FinalityAttestation.EvidenceID = strings.Repeat("a", 64) }},
		{"uppercase_digest", func(m *VendorManifest) { m.FinalityAttestation.EvidenceID = "sha256:" + strings.Repeat("A", 64) }},
		{"zero_confirmation", func(m *VendorManifest) { m.FinalityAttestation.ConfirmedAt = time.Time{} }},
		{"early_confirmation", func(m *VendorManifest) { m.FinalityAttestation.ConfirmedAt = m.Scope.End.Add(-time.Nanosecond) }},
		{"incomplete_report", func(m *VendorManifest) { m.Complete = false }},
		{"incomplete_pages", func(m *VendorManifest) { m.PagesComplete = false }},
		{"missing_watermark", func(m *VendorManifest) { m.VendorWatermark = time.Time{} }},
		{"early_watermark", func(m *VendorManifest) { m.VendorWatermark = m.Scope.End.Add(-time.Nanosecond) }},
		{"later_watermark", func(m *VendorManifest) { m.VendorWatermark = m.Scope.End.Add(time.Nanosecond) }},
		{"zero_period_end", func(m *VendorManifest) { m.Scope.End = time.Time{}; m.VendorWatermark = m.Scope.End }},
		{"reversed_period", func(m *VendorManifest) { m.Scope.Start = m.Scope.End }},
		{"request_scope", func(m *VendorManifest) {
			m.Scope.Granularity = "request"
			m.RequestIDKind = "header"
			m.RequestIDContractEvidenceID = "request-contract"
		}},
		{"invoice_basis", func(m *VendorManifest) { m.CostBasis = "invoice" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _ := vendorFinalityFixture()
			test.mutate(&m)
			require.Error(t, ValidateVendorFinalityAttestation(m))
			require.Error(t, ValidateVendorManifest(m), "manifest validation must enforce the attestation boundary")
		})
	}
}

func TestVendorFinalityCanonicalIdentityPreservesUTCInstantAndInput(t *testing.T) {
	require.Nil(t, CanonicalVendorFinalityAttestation(nil))
	m, rows := vendorFinalityFixture()
	legacy := m
	legacy.FinalityAttestation = nil
	require.NotEqual(t, VendorReportID(legacy, rows), VendorReportID(m, rows), "supplier finality is part of immutable report identity")
	before := *m.FinalityAttestation
	m.FinalityAttestation.ConfirmedAt = before.ConfirmedAt.In(time.FixedZone("CST", 8*60*60))
	local := m.FinalityAttestation.ConfirmedAt
	canonical := CanonicalVendorFinalityAttestation(m.FinalityAttestation)
	require.NotSame(t, m.FinalityAttestation, canonical)
	require.Equal(t, time.UTC, canonical.ConfirmedAt.Location())
	require.Equal(t, local, m.FinalityAttestation.ConfirmedAt, "canonicalization must not mutate the archived timestamp")
	utcManifest := m
	utcManifest.FinalityAttestation = canonical
	require.Equal(t, VendorReportID(utcManifest, rows), VendorReportID(m, rows))
	canonical.Statement = "tampered"
	require.Equal(t, "no_further_corrections", m.FinalityAttestation.Statement, "canonical copy must not alias the source")

	for _, mutate := range []func(*VendorFinalityAttestation){
		func(a *VendorFinalityAttestation) { a.EvidenceID = "sha256:" + strings.Repeat("b", 64) },
		func(a *VendorFinalityAttestation) { a.ConfirmedAt = a.ConfirmedAt.Add(time.Nanosecond) },
		func(a *VendorFinalityAttestation) { a.Statement = "changed" },
		func(a *VendorFinalityAttestation) { a.Kind = "changed" },
	} {
		changed := m
		changed.FinalityAttestation = CanonicalVendorFinalityAttestation(m.FinalityAttestation)
		mutate(changed.FinalityAttestation)
		require.NotEqual(t, VendorReportID(m, rows), VendorReportID(changed, rows), "any finality evidence change must produce a new supplier identity")
	}
}

func TestVendorFinalityDoesNotGrantLocalCoverageOrInvoiceVerification(t *testing.T) {
	m, rows := vendorFinalityFixture()
	m.LocalWatermark = time.Time{}
	m.CoverageEvidenceID = ""
	without := m
	without.FinalityAttestation = nil
	baseline, err := ReconcileVendor(without, rows, nil)
	require.NoError(t, err)
	report, err := ReconcileVendor(m, rows, nil)
	require.NoError(t, err)
	require.Equal(t, "provider_reported", report.CostBasis)
	require.Equal(t, "pending", report.Status)
	require.Equal(t, "none", report.VerificationScope)
	require.Equal(t, baseline.Comparisons, report.Comparisons)
	require.Equal(t, "unknown", report.Comparisons[0].AmountStatus)
	require.Empty(t, report.Attempts)
	require.Zero(t, report.UserReservations)
	require.Zero(t, report.InternalAttempts)
	require.True(t, report.Manifest.LocalWatermark.IsZero())
	require.Empty(t, report.Manifest.CoverageEvidenceID)
}
