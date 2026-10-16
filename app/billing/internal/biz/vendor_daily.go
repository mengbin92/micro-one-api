package biz

import (
	"sort"
)

func SummarizeVendorDaily(rows []VendorReconciliation) []VendorDailySummary {
	// Reports may cover a monthly period or multiple overlapping scopes. Daily
	// operational coverage never sums them into invented per-day invoice money.
	grouped := map[string]VendorDailySummary{}
	seen := map[string]bool{}
	for _, r := range rows {
		m := r.Manifest
		s := m.Scope
		day := s.End.UTC().Format("2006-01-02")
		key := day + "|" + s.Provider + "|" + s.Account + "|" + s.Currency + "|" + s.Unit
		g := grouped[key]
		g.Day, g.Provider, g.Account, g.Currency, g.Unit = day, s.Provider, s.Account, s.Currency, s.Unit
		g.Reports++
		if r.Status == "pending" {
			g.Pending++
		}
		if r.Status == "attention" {
			g.Attention++
		}
		for _, a := range r.Attempts {
			attemptKey := key + "|" + a.SourceKind + "|" + a.ID
			if seen[attemptKey] {
				continue
			}
			seen[attemptKey] = true
			if a.Purpose != "user" {
				g.InternalAttempts++
			}
			if a.EstimatedAmount == nil || a.Currency != s.Currency || a.Unit != s.Unit {
				g.UnknownAmounts++
			}
		}
		// Repository results arrive newest first.
		if g.LatestEvidenceID == "" {
			g.LatestEvidenceID = m.EvidenceID
		}
		grouped[key] = g
	}
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]VendorDailySummary, 0, len(keys))
	for _, k := range keys {
		out = append(out, grouped[k])
	}
	return out
}
