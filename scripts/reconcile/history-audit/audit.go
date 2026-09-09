package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"
)

// Snapshot field order is the v1 hash payload in billing/pricing_snapshot.go.
// This audit projection deliberately imports neither service internals nor
// current pricing configuration. A hash mismatch disables cost reconstruction.
type snapshot struct {
	Version int     `json:"v"`
	Model   string  `json:"model"`
	Input   float64 `json:"in"`
	Output  float64 `json:"out"`
	Read    float64 `json:"cr"`
	Write5m float64 `json:"c5"`
	Write1h float64 `json:"c1"`
	Ratio   float64 `json:"grp"`
	Mode    string  `json:"mode"`
}

type evidence struct {
	Model           string    `json:"model_name"`
	SourceKind      string    `json:"source_kind"`
	ChannelID       int64     `json:"channel_id"`
	AccountID       int64     `json:"subscription_account_id"`
	UpstreamModel   string    `json:"upstream_model_id"`
	Endpoint        string    `json:"endpoint"`
	Prompt          int64     `json:"prompt_tokens"`
	Output          int64     `json:"completion_tokens"`
	Read            int64     `json:"cache_read_tokens"`
	Write5m         int64     `json:"cache_creation_5m_tokens"`
	Write1h         int64     `json:"cache_creation_1h_tokens"`
	Uncached        int64     `json:"uncached_input_tokens"`
	ReportedPrompt  int64     `json:"reported_prompt_tokens"`
	ReportedTotal   int64     `json:"reported_total_tokens"`
	BillableTotal   int64     `json:"billable_total_tokens"`
	Semantics       string    `json:"usage_semantics"`
	Protocol        string    `json:"usage_protocol"`
	FieldShape      string    `json:"usage_field_shape"`
	ParseStatus     string    `json:"usage_parse_status"`
	ContractVersion int       `json:"usage_contract_version"`
	Canonical       int       `json:"canonical_present"`
	DecisionReason  string    `json:"usage_decision_reason"`
	SubsetCost      int64     `json:"subset_candidate_cost"`
	ExclusiveCost   int64     `json:"exclusive_candidate_cost"`
	PricingHash     string    `json:"pricing_config_hash"`
	Snapshot        *snapshot `json:"snapshot"`
}

type ledger struct {
	ID                 int64     `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	UserID             string    `json:"user_id"`
	ReferenceID        string    `json:"reference_id"`
	DedupeKey          string    `json:"ledger_dedupe_key"`
	RequestLedgerCount int       `json:"request_ledger_count"`
	Amount             int64     `json:"amount"`
	CostSource         string    `json:"cost_source"`
	SubscriptionCost   int64     `json:"subscription_cost"`
	BalanceCost        int64     `json:"balance_cost"`
	SubscriptionID     int64     `json:"subscription_id"`
	UpstreamCost       int64     `json:"upstream_cost"`
	CostAuditStatus    string    `json:"cost_audit_status"`
	// input, cache read, creation 5m, creation 1h, output; stored costs only.
	BucketCosts [5]int64 `json:"bucket_costs"`
	Evidence    evidence `json:"evidence"`
}

type record struct {
	Classification string   `json:"classification"`
	Reason         string   `json:"reason"`
	EvidenceSource string   `json:"evidence_source"`
	ChargedCost    int64    `json:"charged_cost"`
	CanonicalCost  *int64   `json:"canonical_cost"`
	CanonicalDelta *int64   `json:"canonical_delta"`
	SubsetCost     *int64   `json:"subset_candidate_cost"`
	SubsetDelta    *int64   `json:"subset_candidate_delta"`
	ExclusiveCost  *int64   `json:"exclusive_candidate_cost"`
	ExclusiveDelta *int64   `json:"exclusive_candidate_delta"`
	Ledgers        []ledger `json:"ledgers"`
}

func audit(rows []ledger) ([]record, error) {
	rows = append([]ledger(nil), rows...)
	for i := range rows {
		rows[i].CreatedAt = rows[i].CreatedAt.UTC()
		if rows[i].ID <= 0 || rows[i].CreatedAt.IsZero() {
			return nil, errors.New("ledger evidence requires a positive id and created_at")
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	type key struct{ user, reference, standalone string }
	groups := map[key]int{}
	out := []record{}
	for i, row := range rows {
		if i > 0 && row.ID == rows[i-1].ID && row.CreatedAt.Equal(rows[i-1].CreatedAt) {
			return nil, errors.New("duplicate ledger identity in input")
		}
		k := key{user: row.UserID, reference: row.ReferenceID}
		if k.reference == "" {
			k.standalone = fmt.Sprintf("%d/%s", row.ID, row.CreatedAt.Format(time.RFC3339Nano))
		}
		index, ok := groups[k]
		if !ok {
			index = len(out)
			groups[k] = index
			out = append(out, record{Ledgers: []ledger{}})
		}
		out[index].Ledgers = append(out[index].Ledgers, row)
	}
	for i := range out {
		classify(&out[i])
	}
	return out, nil
}

func classify(r *record) {
	r.Classification, r.Reason, r.EvidenceSource = "unknown", "invalid_or_incomplete_request", "billing_ledgers"
	first := r.Ledgers[0]
	e := first.Evidence
	seen := map[string]bool{}
	costSeen := map[string]bool{}
	for _, l := range r.Ledgers {
		if l.Amount > 0 || l.Amount == math.MinInt64 || -l.Amount > math.MaxInt64-r.ChargedCost {
			return
		}
		r.ChargedCost += -l.Amount
	}
	for _, l := range r.Ledgers {
		if l.RequestLedgerCount != len(r.Ledgers) || !reflect.DeepEqual(l.Evidence, e) {
			return
		}
		if l.DedupeKey != "" && seen[l.DedupeKey] {
			return
		}
		seen[l.DedupeKey] = true
		// Two rows are only a valid split when each payment dimension occurs
		// once and its own amount agrees. Never allocate a request delta here.
		if len(r.Ledgers) > 1 {
			if len(r.Ledgers) != 2 || costSeen[l.CostSource] || l.SubscriptionID != first.SubscriptionID {
				return
			}
			costSeen[l.CostSource] = true
			switch l.CostSource {
			case "subscription":
				if l.SubscriptionCost != -l.Amount || l.BalanceCost != 0 {
					return
				}
			case "balance":
				if l.BalanceCost != -l.Amount || l.SubscriptionCost != 0 {
					return
				}
			default:
				return
			}
		}
	}
	values := []int64{e.Prompt, e.Output, e.Read, e.Write5m, e.Write1h, e.Uncached, e.ReportedPrompt, e.ReportedTotal, e.BillableTotal}
	for _, n := range values {
		if n < 0 {
			r.Reason = "invalid_usage"
			return
		}
	}
	total, ok := tokenSum(e.Prompt, e.Output, e.Read, e.Write5m, e.Write1h)
	if !ok || total == 0 {
		r.Reason = "missing_or_overflowing_usage"
		return
	}
	if e.ContractVersion != 0 && e.ContractVersion != 1 {
		r.Reason = "unsupported_usage_contract"
		return
	}
	r.Classification, r.Reason = "candidate", "legacy_or_unproven_semantics"
	verified := false
	var canonicalOutput int64
	if e.ContractVersion == 1 && e.ParseStatus == "verified" {
		input, valid := tokenSum(e.Uncached, e.Read, e.Write5m, e.Write1h)
		if !valid || e.Canonical != 1 || e.BillableTotal < input || !knownProtocolEvidence(e) ||
			(e.Semantics != "" && e.Semantics != "openai_subset" && e.Semantics != "anthropic_exclusive") ||
			(e.Semantics == "" && (e.Read > 0 || e.Write5m > 0 || e.Write1h > 0)) {
			r.Classification, r.Reason = "unknown", "invalid_v1_evidence"
			return
		}
		canonicalOutput = e.BillableTotal - input
		// Check the persisted upstream prompt against the claimed semantics;
		// reported total is deliberately NOT used as a protocol heuristic.
		if (e.Semantics == "anthropic_exclusive" && e.Uncached != e.ReportedPrompt) ||
			(e.Semantics != "anthropic_exclusive" && (e.Read > e.ReportedPrompt || e.Uncached != e.ReportedPrompt-e.Read)) {
			r.Classification, r.Reason = "unknown", "inconsistent_reported_usage"
			return
		}
		verified = true
		r.EvidenceSource = "billing_ledgers:v1_verified_usage"
	} else if e.ParseStatus == "ambiguous" {
		r.Reason = "ambiguous_semantics"
	} else if e.ParseStatus == "estimated" {
		r.Reason = "estimated_usage"
	}
	if e.UpstreamModel == "" || (e.SourceKind != "channel" && e.SourceKind != "subscription") ||
		(e.SourceKind == "channel" && e.ChannelID <= 0) || (e.SourceKind == "subscription" && e.AccountID <= 0) {
		r.Reason = "missing_source_evidence"
		return
	}
	if !validSnapshot(e) {
		r.Reason = "missing_or_invalid_pricing_snapshot"
		return
	}
	r.EvidenceSource += "+billing_pricing_snapshots:sha256"
	if verified {
		r.Classification, r.Reason = "verified", "immutable_usage_and_pricing"
		cost := priceBuckets(e.Snapshot, [5]int64{e.Uncached, e.Read, e.Write5m, e.Write1h, canonicalOutput})
		delta := cost - r.ChargedCost
		r.CanonicalCost, r.CanonicalDelta = &cost, &delta
		return
	}
	// Hypotheses only: neither token relationships nor a model name select a
	// winning meaning. Pre-088/ratio-priced rows never get guessed prices.
	prompt := e.Prompt
	if e.ContractVersion == 1 {
		prompt = e.ReportedPrompt
	}
	subset := priceBuckets(e.Snapshot, [5]int64{max(prompt-e.Read, 0), e.Read, e.Write5m, e.Write1h, e.Output})
	exclusive := priceBuckets(e.Snapshot, [5]int64{prompt, e.Read, e.Write5m, e.Write1h, e.Output})
	subsetDelta, exclusiveDelta := subset-r.ChargedCost, exclusive-r.ChargedCost
	r.SubsetCost, r.SubsetDelta, r.ExclusiveCost, r.ExclusiveDelta = &subset, &subsetDelta, &exclusive, &exclusiveDelta
}

func tokenSum(values ...int64) (int64, bool) {
	var total int64
	for _, n := range values {
		if n < 0 || n > math.MaxInt64-total {
			return 0, false
		}
		total += n
	}
	return total, true
}

// Recognize the persisted descriptors produced by usage/extract.go and the
// typed Anthropic provider. Unknown conversion paths cannot prove semantics.
func knownProtocolEvidence(e evidence) bool {
	if e.FieldShape == "provider:anthropic_messages" {
		return e.Protocol == "anthropic_messages" && e.Semantics == "anthropic_exclusive"
	}
	parts := strings.Split(e.FieldShape, "+")
	protocol := ""
	switch parts[0] {
	case "prompt_tokens":
		protocol = "openai_chat"
	case "input_tokens":
		protocol = "responses"
	default:
		return false
	}
	anthropic, cached := false, false
	for _, field := range parts[1:] {
		switch field {
		case "cache_read_input_tokens", "cache_creation":
			anthropic = true
		case "details.cached_tokens":
			cached = true
		case "cache_read_tokens":
		default:
			return false
		}
	}
	if anthropic {
		return !cached && e.Protocol == "anthropic_messages" && e.Semantics == "anthropic_exclusive"
	}
	return e.Protocol == protocol && e.Semantics != "anthropic_exclusive"
}

func validSnapshot(e evidence) bool {
	s := e.Snapshot
	if s == nil || s.Version != 1 || s.Model == "" || (s.Mode != "observe" && s.Mode != "charge") {
		return false
	}
	for _, n := range []float64{s.Input, s.Output, s.Read, s.Write5m, s.Write1h, s.Ratio} {
		if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	raw, err := jsonx.Marshal(s)
	return err == nil && fmt.Sprintf("%x", sha256.Sum256(raw)) == e.PricingHash
}

// Mirror billing.go roundScaled's exact float64 multiplication order and
// per-bucket rounding. DECIMAL ROUND differs at half-quota boundaries.
func priceBuckets(s *snapshot, tokens [5]int64) int64 {
	prices := [5]float64{s.Input, s.Read, s.Write5m, s.Write1h, s.Output}
	var total int64
	for i, n := range tokens {
		if (i == 2 || i == 3) && s.Mode != "charge" {
			continue
		}
		raw := float64(n) * prices[i] * s.Ratio * 10000
		if raw <= 0 {
			continue
		}
		if raw >= float64(math.MaxInt64) {
			return math.MaxInt64
		}
		cost := int64(math.Round(raw))
		if cost > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += cost
	}
	return max(total, 1)
}
