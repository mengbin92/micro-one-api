package biz

import (
	"fmt"
	"strings"
	"time"
)

// Expectations declare coverage independently of imported supplier reports.
// ValidUntil keeps a fixed inventory from silently expiring into healthy zeroes.
type VendorReportExpectations struct {
	ValidUntil time.Time              `json:"valid_until"`
	Reports    []VendorExpectedReport `json:"reports"`
}

type VendorExpectedReport struct {
	Scope VendorScope `json:"scope"`
	DueAt time.Time   `json:"due_at"`
}

func DecodeVendorReportExpectations(raw []byte) (VendorReportExpectations, error) {
	var out VendorReportExpectations
	if err := DecodeVendorJSON(raw, &out); err != nil {
		return VendorReportExpectations{}, err
	}
	if err := ValidateVendorReportExpectations(out); err != nil {
		return VendorReportExpectations{}, err
	}
	return out, nil
}

func ValidateVendorReportExpectations(e VendorReportExpectations) error {
	if e.ValidUntil.IsZero() || len(e.Reports) < 1 || len(e.Reports) > 100 {
		return fmt.Errorf("vendor report expectations require an expiry and 1 to 100 fixed scopes")
	}
	seen := make(map[VendorScope]bool, len(e.Reports))
	for i, report := range e.Reports {
		s := report.Scope
		if strings.TrimSpace(s.Provider) == "" || strings.TrimSpace(s.Account) == "" || len(s.Account) > 255 || len(s.Model) > 255 || strings.TrimSpace(s.Currency) == "" || strings.TrimSpace(s.Unit) == "" || s.Start.Unix() <= 0 || !s.Start.Before(s.End) || s.End.Sub(s.Start) > 366*24*time.Hour {
			return fmt.Errorf("invalid vendor report expectation scope at index %d", i)
		}
		if s.Timezone == "" {
			return fmt.Errorf("vendor report expectation timezone required at index %d", i)
		}
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("invalid vendor report expectation timezone at index %d", i)
		}
		if s.Granularity != "request" && s.Granularity != "account_period" {
			return fmt.Errorf("invalid vendor report expectation granularity at index %d", i)
		}
		if report.DueAt.IsZero() || report.DueAt.Before(s.End) || e.ValidUntil.Before(report.DueAt) {
			return fmt.Errorf("vendor report expectation deadlines must cover the period and precede inventory expiry at index %d", i)
		}
		key := CanonicalVendorScope(s)
		if seen[key] {
			return fmt.Errorf("duplicate vendor report expectation scope at index %d", i)
		}
		seen[key] = true
	}
	return nil
}

// Presence is separate from finality: a pending report still needs its existing
// completeness checks, and different periods never imply allocated money.
func MissingVendorReports(e VendorReportExpectations, rows []VendorReconciliation) []VendorExpectedReport {
	imported := make(map[VendorScope]bool, len(rows))
	for _, row := range rows {
		imported[CanonicalVendorScope(row.Manifest.Scope)] = true
	}
	var missing []VendorExpectedReport
	for _, report := range e.Reports {
		if !imported[CanonicalVendorScope(report.Scope)] {
			missing = append(missing, report)
		}
	}
	return missing
}
