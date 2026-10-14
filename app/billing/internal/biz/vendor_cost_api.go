package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"

	"micro-one-api/pkg/jsonx"
)

// VendorCostQuery fixes the authenticated account binding and comparable CSV
// evidence before network access. A model-specific scope cannot be inferred
// from an organization cost API.
type VendorCostQuery struct {
	Scope                     VendorScope `json:"scope"`
	CredentialBindingEvidence string      `json:"credential_binding_evidence"`
	CSVReportID               string      `json:"csv_report_id"`
	CSVTotal                  string      `json:"csv_total"`
}

type VendorCostPage struct {
	Digest string `json:"digest"`
	// Base64 bytes preserve original whitespace and therefore the page SHA;
	// RawMessage would be reformatted by MarshalIndent and break replay.
	Body []byte `json:"body"`
}

type VendorCostEvidence struct {
	Query         VendorCostQuery  `json:"query"`
	Pages         []VendorCostPage `json:"pages"`
	Rows          []VendorBillRow  `json:"rows"`
	FetchedAt     time.Time        `json:"fetched_at"`
	PagesComplete bool             `json:"pages_complete"`
	CSVMatched    bool             `json:"csv_matched"`
}

type VendorCostReader interface {
	FetchCosts(context.Context, VendorCostQuery) (VendorCostEvidence, error)
}

type VendorCostValidator interface {
	ValidateCostEvidence(context.Context, VendorCostEvidence) (VendorCostEvidence, error)
}

type VendorReportMonitoringRepo interface {
	ListVendorMonitoringReports(context.Context, int) ([]VendorReconciliation, error)
}

func ValidateVendorCostQuery(q VendorCostQuery, now time.Time) error {
	s := q.Scope
	if (s.Provider != "openai" && s.Provider != "anthropic") || s.Account == "" || s.Model != "" || s.Granularity != "account_period" || s.Currency != "USD" || s.Unit != "currency" || s.Timezone != "UTC" || s.Start.Unix() <= 0 || !s.Start.Before(s.End) || s.End.Sub(s.Start) > 31*24*time.Hour || s.Start.Unix()%86400 != 0 || s.End.Unix()%86400 != 0 || s.Start.Nanosecond() != 0 || s.End.Nanosecond() != 0 || s.End.After(now) {
		return fmt.Errorf("cost API requires a closed UTC daily account/project scope, USD major units, at most 31 days")
	}
	if q.CredentialBindingEvidence == "" || len(q.CSVReportID) != 64 {
		return fmt.Errorf("reviewed credential/account binding and fixed CSV report identity required")
	}
	if _, err := vendorNumber(q.CSVTotal); err != nil {
		return err
	}
	return nil
}

// ValidateCSV fixes the reference to a complete, same-scope CSV report, not an
// agent-provided amount or tolerance. Cost-only APIs cannot invent token usage.
func (q VendorCostQuery) ValidateCSV(m VendorManifest, rows []VendorBillRow) error {
	if ValidateVendorManifest(m) != nil || !m.Complete || !m.PagesComplete || m.VendorWatermark.Before(m.Scope.End) || CanonicalVendorScope(q.Scope) != CanonicalVendorScope(m.Scope) || q.CSVReportID != VendorReportID(m, rows) {
		return fmt.Errorf("cost API reference must be a complete same-scope immutable CSV report")
	}
	total, err := VendorAmountTotal(rows)
	if err != nil {
		return err
	}
	a, err := vendorNumber(q.CSVTotal)
	if err != nil {
		return err
	}
	b, _ := vendorNumber(total)
	if a.Cmp(b) != 0 {
		return fmt.Errorf("CSV reference total mismatch")
	}
	return nil
}

func VendorAmountTotal(rows []VendorBillRow) (string, error) {
	if len(rows) == 0 {
		return "", fmt.Errorf("absent report amount is unknown")
	}
	total := new(big.Rat)
	for _, row := range rows {
		if row.Amount == nil {
			return "", fmt.Errorf("unknown report amount")
		}
		n, err := vendorNumber(*row.Amount)
		if err != nil {
			return "", err
		}
		total.Add(total, n)
	}
	return vendorString(total), nil
}

func (e VendorCostEvidence) ID() string {
	// Fetch time is operational metadata; the immutable identity is the fixed
	// scope/reference plus original pages and normalized rows.
	b, _ := jsonx.Marshal(struct {
		Query VendorCostQuery
		Pages []VendorCostPage
		Rows  []VendorBillRow
	}{e.Query, e.Pages, e.Rows})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
