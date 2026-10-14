package biz

import (
	"math/big"
	"sort"
	"time"
)

// Review items keep scope and currency separate; overlapping periods and
// unknown costs are never added to an invented financial total.
type VendorReviewItem struct {
	ReportID          string      `json:"report_id"`
	Scope             VendorScope `json:"scope"`
	Version           string      `json:"version"`
	Status            string      `json:"status"`
	AgeSeconds        int64       `json:"age_seconds"`
	UnknownAmounts    int         `json:"unknown_amounts"`
	InternalAttempts  int         `json:"internal_attempts"`
	Unexplained       *string     `json:"unexplained"`
	CorrectionDelta   *string     `json:"correction_delta,omitempty"`
	ParserRevisions   []string    `json:"parser_revisions"`
	ContractRevisions []string    `json:"contract_revisions"`
	NextAction        string      `json:"next_action"`
}
type VendorWeeklyReview struct {
	AsOf           time.Time          `json:"as_of"`
	Reports        int                `json:"reports"`
	Pending        int                `json:"pending"`
	Attention      int                `json:"attention"`
	UnknownAmounts int                `json:"unknown_amounts"`
	Items          []VendorReviewItem `json:"items"`
}

func SummarizeVendorWeekly(rows []VendorReconciliation, asOf time.Time) VendorWeeklyReview {
	out := VendorWeeklyReview{AsOf: asOf.UTC(), Reports: len(rows)}
	for _, r := range rows {
		item := VendorReviewItem{ReportID: r.ID, Scope: r.Manifest.Scope, Version: r.Manifest.Version, Status: r.Status, InternalAttempts: r.InternalAttempts, CorrectionDelta: r.CorrectionDelta, NextAction: "retain_evidence"}
		if asOf.After(r.Manifest.Scope.End) {
			item.AgeSeconds = int64(asOf.Sub(r.Manifest.Scope.End) / time.Second)
		}
		if r.Status == "pending" {
			out.Pending++
			item.NextAction = "complete_watermarks_or_late_evidence"
		}
		if r.Status == "attention" {
			out.Attention++
			item.NextAction = "review_unexplained_difference"
		}
		if r.UnknownTimeAttempts+r.UnmappedAttempts > 0 {
			item.NextAction = "resolve_attempt_scope"
		}
		for _, c := range r.Comparisons {
			item.UnknownAmounts += c.UnknownAmounts
			if r.Manifest.Scope.Granularity == "account_period" {
				item.Unexplained = c.Unexplained
			}
		}
		out.UnknownAmounts += item.UnknownAmounts
		parsers, contracts := map[string]bool{}, map[string]bool{}
		for _, a := range r.Attempts {
			parsers[a.ParserRevision] = true
			contracts[a.ContractRevision] = true
		}
		for k := range parsers {
			if k != "" {
				item.ParserRevisions = append(item.ParserRevisions, k)
			}
		}
		for k := range contracts {
			if k != "" {
				item.ContractRevisions = append(item.ContractRevisions, k)
			}
		}
		sort.Strings(item.ParserRevisions)
		sort.Strings(item.ContractRevisions)
		out.Items = append(out.Items, item)
	}
	rank := func(s string) int {
		if s == "attention" {
			return 0
		}
		if s == "pending" {
			return 1
		}
		return 2
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if rank(a.Status) != rank(b.Status) {
			return rank(a.Status) < rank(b.Status)
		}
		if a.Status == "pending" && a.AgeSeconds != b.AgeSeconds {
			return a.AgeSeconds > b.AgeSeconds
		}
		// Group currency/unit before amount comparisons to retain a total order.
		if a.Scope.Currency != b.Scope.Currency {
			return a.Scope.Currency < b.Scope.Currency
		}
		if a.Scope.Unit != b.Scope.Unit {
			return a.Scope.Unit < b.Scope.Unit
		}
		if (a.Unexplained == nil) != (b.Unexplained == nil) {
			return a.Unexplained != nil
		}
		if a.Unexplained != nil && b.Unexplained != nil {
			x, xOK := new(big.Rat).SetString(*a.Unexplained)
			y, yOK := new(big.Rat).SetString(*b.Unexplained)
			if xOK && yOK {
				x.Abs(x)
				y.Abs(y)
				if x.Cmp(y) != 0 {
					return x.Cmp(y) > 0
				}
			}
		}
		return a.ReportID < b.ReportID
	})
	return out
}
