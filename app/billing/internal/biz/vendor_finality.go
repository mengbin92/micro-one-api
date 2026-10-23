package biz

import (
	"fmt"
	"regexp"
	"time"
)

// VendorFinalityAttestation records a maintainer's explicit confirmation for
// one supplier account period. It is not supplier-published or signed proof,
// execution coverage, a local delivery watermark, or an invoice.
type VendorFinalityAttestation struct {
	Kind        string    `json:"kind"`
	EvidenceID  string    `json:"evidence_id"`
	ConfirmedAt time.Time `json:"confirmed_at"`
	Statement   string    `json:"statement"`
}

var vendorFinalityDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func ValidateVendorFinalityAttestation(m VendorManifest) error {
	a := m.FinalityAttestation
	if a == nil {
		return nil
	}
	if a.Kind != "maintainer_confirmation" || a.Statement != "no_further_corrections" || !vendorFinalityDigest.MatchString(a.EvidenceID) {
		return fmt.Errorf("vendor finality requires an explicit maintainer confirmation and canonical SHA256 evidence identity")
	}
	if m.Scope.Granularity != "account_period" || m.CostBasis != "provider_reported" {
		return fmt.Errorf("maintainer finality is limited to provider-reported account-period evidence")
	}
	if m.Scope.End.IsZero() || !m.Scope.Start.Before(m.Scope.End) || a.ConfirmedAt.IsZero() || a.ConfirmedAt.Before(m.Scope.End) || !m.Complete || !m.PagesComplete || !m.VendorWatermark.Equal(m.Scope.End) {
		return fmt.Errorf("maintainer finality requires a completed period, complete pages, an exact period-end vendor watermark and confirmation at or after period end")
	}
	return nil
}

// CanonicalVendorFinalityAttestation returns a detached UTC copy for supplier
// report identity. Omitting an attestation preserves existing report hashes.
func CanonicalVendorFinalityAttestation(a *VendorFinalityAttestation) *VendorFinalityAttestation {
	if a == nil {
		return nil
	}
	canonical := *a
	canonical.ConfirmedAt = canonical.ConfirmedAt.UTC()
	return &canonical
}
