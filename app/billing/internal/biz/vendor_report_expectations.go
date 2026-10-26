package biz

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Expectations declare coverage independently of imported supplier reports.
// ValidUntil keeps a fixed inventory from silently expiring into healthy zeroes.
type VendorReportExpectations struct {
	ValidUntil       time.Time                     `json:"valid_until"`
	Reports          []VendorExpectedReport        `json:"reports"`
	ReviewExceptions []VendorReportReviewException `json:"review_exceptions,omitempty"`
}

type VendorExpectedReport struct {
	Scope VendorScope `json:"scope"`
	DueAt time.Time   `json:"due_at"`
}

// Review exceptions acknowledge one immutable run without confirming its costs.
type VendorReportReviewException struct {
	ReportID   string    `json:"report_id"`
	RunID      string    `json:"run_id"`
	ReviewedBy string    `json:"reviewed_by"`
	Reason     string    `json:"reason"`
	EvidenceID string    `json:"evidence_id"`
	ReviewedAt time.Time `json:"reviewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

var vendorReviewDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

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
	if len(e.ReviewExceptions) > 100 {
		return fmt.Errorf("vendor report expectations allow at most 100 review exceptions")
	}
	reviewed := make(map[string]bool, len(e.ReviewExceptions))
	for i, review := range e.ReviewExceptions {
		if !vendorReviewDigest.MatchString(review.ReportID) || !vendorReviewDigest.MatchString(review.RunID) || !strings.HasPrefix(review.EvidenceID, "sha256:") || !vendorReviewDigest.MatchString(strings.TrimPrefix(review.EvidenceID, "sha256:")) {
			return fmt.Errorf("invalid vendor report review identity at index %d", i)
		}
		if strings.TrimSpace(review.ReviewedBy) == "" || len(review.ReviewedBy) > 128 || strings.TrimSpace(review.Reason) == "" || len(review.Reason) > 1024 {
			return fmt.Errorf("vendor report review requires a bounded reviewer and reason at index %d", i)
		}
		if review.ReviewedAt.Unix() <= 0 || !review.ExpiresAt.After(review.ReviewedAt) || review.ExpiresAt.After(e.ValidUntil) {
			return fmt.Errorf("vendor report review must expire after review and no later than inventory at index %d", i)
		}
		if reviewed[review.ReportID] {
			return fmt.Errorf("duplicate vendor report review at index %d", i)
		}
		reviewed[review.ReportID] = true
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
