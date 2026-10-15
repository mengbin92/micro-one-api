package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // Supplier periods must work in the minimal runtime image.

	"micro-one-api/pkg/jsonx"
)

// Vendor reconciliation never writes customer ledgers or upgrades tariff estimates
// to invoice evidence. Decimal strings preserve the supplier's original unit.
type VendorScope struct {
	Provider    string    `json:"provider"`
	Account     string    `json:"account"`
	Project     string    `json:"project,omitempty"`
	Model       string    `json:"upstream_model_id,omitempty"`
	Currency    string    `json:"currency"`
	Unit        string    `json:"unit"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Timezone    string    `json:"timezone"`
	Granularity string    `json:"granularity"` // request or account_period
}

type VendorSourceMapping struct {
	SourceKind string    `json:"source_kind"`
	SourceID   int64     `json:"source_id"`
	Provider   string    `json:"provider"`
	Account    string    `json:"account"`
	Project    string    `json:"project,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Revision   string    `json:"revision"`
	EvidenceID string    `json:"evidence_id"`
}

type VendorManifest struct {
	Scope                       VendorScope             `json:"scope"`
	Version                     string                  `json:"version"`
	EvidenceID                  string                  `json:"evidence_id"`
	CostBasis                   string                  `json:"cost_basis"` // invoice or provider_reported
	Mode                        string                  `json:"mode"`       // tokens, monthly, balance_only, unknown
	Complete                    bool                    `json:"complete"`
	PagesComplete               bool                    `json:"pages_complete"`
	VendorWatermark             time.Time               `json:"vendor_watermark"`
	LocalWatermark              time.Time               `json:"local_watermark"`
	CoverageEvidenceID          string                  `json:"coverage_evidence_id"`
	RequestIDKind               string                  `json:"request_id_kind,omitempty"` // header or response
	RequestIDContractEvidenceID string                  `json:"request_id_contract_evidence_id,omitempty"`
	Mappings                    []VendorSourceMapping   `json:"mappings"`
	AbsoluteTolerance           string                  `json:"absolute_tolerance"`
	RelativeTolerance           string                  `json:"relative_tolerance"`
	RoundingEvidenceID          string                  `json:"rounding_evidence_id,omitempty"`
	Supersedes                  string                  `json:"supersedes,omitempty"` // immutable prior import ID
	ContractEvidenceID          string                  `json:"contract_evidence_id,omitempty"`
	OverageEvidenceID           string                  `json:"overage_evidence_id,omitempty"`
	Actor                       string                  `json:"actor"`
	Reason                      string                  `json:"reason"`
	ExcludedAttempts            []VendorExcludedAttempt `json:"excluded_attempts,omitempty"`
}

type VendorExcludedAttempt struct {
	ID         string `json:"attempt_id"`
	Reason     string `json:"reason"`
	EvidenceID string `json:"evidence_id"`
}

type VendorBillRow struct {
	RequestID string    `json:"request_id,omitempty"`
	Amount    *string   `json:"amount"`
	Buckets   *[5]int64 `json:"buckets"`
}

type VendorLocalAttempt struct {
	ID                   string    `json:"attempt_id"`
	RootRequestID        string    `json:"root_request_id,omitempty"`
	ParserRevision       string    `json:"parser_revision,omitempty"`
	ContractRevision     string    `json:"contract_revision,omitempty"`
	BuildRevision        string    `json:"build_revision,omitempty"`
	TariffSnapshotID     string    `json:"tariff_snapshot_id,omitempty"`
	CostAlgorithmVersion string    `json:"cost_algorithm_version,omitempty"`
	SettlementPolicy     string    `json:"settlement_policy,omitempty"`
	ReservationID        string    `json:"reservation_id,omitempty"`
	SourceKind           string    `json:"source_kind"`
	SourceID             int64     `json:"source_id"`
	Model                string    `json:"upstream_model_id"`
	RequestID            string    `json:"provider_request_id,omitempty"`
	ResponseID           string    `json:"provider_response_id,omitempty"`
	Purpose              string    `json:"purpose"`
	ExecutedAt           time.Time `json:"executed_at"`
	Complete             bool      `json:"complete"`
	Buckets              *[5]int64 `json:"buckets"`
	EstimatedAmount      *string   `json:"estimated_amount"`
	Currency             string    `json:"currency,omitempty"`
	Unit                 string    `json:"unit,omitempty"`
	EvidenceIDs          []string  `json:"evidence_ids"`
}

type VendorAttribution struct {
	Category   string `json:"category"`
	Amount     string `json:"amount"` // supplier minus local, signed
	Status     string `json:"status"` // hypothesis or confirmed
	EvidenceID string `json:"evidence_id"`
	Scope      string `json:"verification_scope"`
}

type VendorComparison struct {
	Key                string              `json:"key"`
	Status             string              `json:"status"`
	UsageStatus        string              `json:"usage_status"`
	AmountStatus       string              `json:"amount_status"`
	VendorAmount       *string             `json:"vendor_amount"`
	EstimatedAmount    *string             `json:"estimated_amount"`
	Difference         *string             `json:"difference"`
	RelativeDifference *string             `json:"relative_difference"`
	VendorBuckets      *[5]int64           `json:"vendor_buckets"`
	LocalBuckets       *[5]int64           `json:"local_buckets"`
	UnknownAmounts     int                 `json:"unknown_amounts"`
	Attempts           []string            `json:"attempts"`
	PurposeCounts      map[string]int      `json:"purpose_counts"`
	Attributions       []VendorAttribution `json:"attributions,omitempty"`
	Unexplained        *string             `json:"unexplained"`
}

type VendorReconciliation struct {
	CostAPIEvidence     *VendorCostEvidence  `json:"cost_api_evidence,omitempty"`
	ID                  string               `json:"id"`
	Manifest            VendorManifest       `json:"manifest"`
	Rows                []VendorBillRow      `json:"rows"`
	Comparisons         []VendorComparison   `json:"comparisons"`
	Status              string               `json:"status"`
	VerificationScope   string               `json:"verification_scope"`
	CostBasis           string               `json:"cost_basis"`
	UnknownTimeAttempts int                  `json:"unknown_time_attempts"`
	UnmappedAttempts    int                  `json:"unmapped_attempts"`
	InternalAttempts    int                  `json:"internal_attempts"`
	UserReservations    int                  `json:"user_reservations"`
	CorrectionDelta     *string              `json:"correction_delta,omitempty"`
	Attempts            []VendorLocalAttempt `json:"attempt_evidence"`
	UnresolvedAttempts  []VendorLocalAttempt `json:"unresolved_attempt_evidence,omitempty"`
}

type VendorReconciliationRepo interface {
	LoadVendorAttempts(context.Context, VendorManifest) ([]VendorLocalAttempt, error)
	StoreVendorReconciliation(context.Context, *VendorReconciliation) error
	ListVendorReconciliations(context.Context, time.Time, time.Time, int) ([]VendorReconciliation, error)
}

var vendorDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]{1,18})?$`)

func vendorNumber(s string) (*big.Rat, error) {
	if len(s) > 80 || !vendorDecimal.MatchString(s) {
		return nil, fmt.Errorf("invalid decimal amount %q", s)
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid decimal")
	}
	return v, nil
}

// Decimal differences have at most the input precision, so FloatString(18)
// is exact here; relative differences are display-only, never used for decisions.
func vendorString(v *big.Rat) string {
	s := v.FloatString(18)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}
func vendorPtr(v *big.Rat) *string { s := vendorString(v); return &s }

func ValidateVendorManifest(m VendorManifest) error {
	s := m.Scope
	if s.Provider == "" || s.Account == "" || len(s.Account) > 255 || len(s.Model) > 255 || s.Currency == "" || s.Unit == "" || !s.Start.Before(s.End) || s.Start.Unix() <= 0 || s.End.Sub(s.Start) > 366*24*time.Hour || m.Version == "" || len(m.Version) > 128 || m.EvidenceID == "" || m.Actor == "" || strings.TrimSpace(m.Reason) == "" || len(m.Mappings) == 0 || len(m.Mappings) > 10000 {
		return fmt.Errorf("invalid scope, report identity, mappings or audit intent")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid supplier timezone: %w", err)
	}
	if s.Granularity != "request" && s.Granularity != "account_period" {
		return fmt.Errorf("unsupported report granularity")
	}
	if s.Granularity == "request" && (m.RequestIDKind != "header" && m.RequestIDKind != "response" || m.RequestIDContractEvidenceID == "") {
		return fmt.Errorf("request matching requires an evidenced supplier report/header-or-response ID contract")
	}
	if m.RequestIDKind != "" && m.RequestIDKind != "header" && m.RequestIDKind != "response" {
		return fmt.Errorf("unsupported supplier request ID contract")
	}
	if m.CostBasis != "invoice" && m.CostBasis != "provider_reported" {
		return fmt.Errorf("external report requires invoice or provider_reported evidence")
	}
	switch m.Mode {
	case "tokens", "monthly", "balance_only", "unknown":
	default:
		return fmt.Errorf("invalid verification mode")
	}
	if m.Mode == "monthly" && (m.ContractEvidenceID == "" || m.OverageEvidenceID == "" || s.Granularity != "account_period") {
		return fmt.Errorf("monthly verification requires contract and overage evidence at account_period scope")
	}
	a, err := vendorNumber(m.AbsoluteTolerance)
	if err != nil || a.Sign() < 0 {
		return fmt.Errorf("invalid absolute tolerance")
	}
	r, err := vendorNumber(m.RelativeTolerance)
	if err != nil || r.Sign() < 0 || r.Cmp(big.NewRat(1, 1)) > 0 {
		return fmt.Errorf("invalid relative tolerance")
	}
	if (a.Sign() != 0 || r.Sign() != 0) && m.RoundingEvidenceID == "" {
		return fmt.Errorf("nonzero tolerances require unit/rounding evidence")
	}
	for i, v := range m.Mappings {
		if (v.SourceKind != "channel" && v.SourceKind != "subscription") || v.SourceID <= 0 || v.Provider == "" || v.Account == "" || !v.Start.Before(v.End) || v.Revision == "" || v.EvidenceID == "" {
			return fmt.Errorf("invalid account/source mapping")
		}
		for _, old := range m.Mappings[:i] {
			if old.SourceKind == v.SourceKind && old.SourceID == v.SourceID && old.Start.Before(v.End) && v.Start.Before(old.End) {
				return fmt.Errorf("overlapping account/source mappings")
			}
		}
	}
	seenExcluded := map[string]bool{}
	for _, v := range m.ExcludedAttempts {
		if v.ID == "" || v.Reason == "" || v.EvidenceID == "" || seenExcluded[v.ID] {
			return fmt.Errorf("invalid/duplicate attempt exclusion")
		}
		seenExcluded[v.ID] = true
	}
	return nil
}

func VendorReportID(m VendorManifest, rows []VendorBillRow) string {
	// Local watermarks/audit operators may change between replays; supplier
	// import identity depends only on scope, version and original report content.
	b, _ := jsonx.Marshal(struct {
		Scope                                             VendorScope
		Version, EvidenceID, Basis, Mode                  string
		Rows                                              []VendorBillRow
		Complete, PagesComplete                           bool
		VendorWatermark                                   time.Time
		Supersedes, ContractEvidenceID, OverageEvidenceID string
		RequestIDKind, RequestIDContractEvidenceID        string
	}{CanonicalVendorScope(m.Scope), m.Version, m.EvidenceID, m.CostBasis, m.Mode, rows, m.Complete, m.PagesComplete, m.VendorWatermark.UTC(), m.Supersedes, m.ContractEvidenceID, m.OverageEvidenceID, m.RequestIDKind, m.RequestIDContractEvidenceID})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func CanonicalVendorScope(s VendorScope) VendorScope {
	s.Start = s.Start.UTC()
	s.End = s.End.UTC()
	return s
}

func ReconcileVendor(m VendorManifest, rows []VendorBillRow, local []VendorLocalAttempt) (VendorReconciliation, error) {
	out := VendorReconciliation{Manifest: m, Rows: rows, Status: "matched", CostBasis: m.CostBasis, VerificationScope: "none"}
	if err := ValidateVendorManifest(m); err != nil {
		return out, err
	}
	if len(rows) > 100000 || len(local) > 100000 {
		return out, fmt.Errorf("report exceeds bounded reconciliation limit")
	}
	vendor := map[string]VendorBillRow{}
	for _, r := range rows {
		key := r.RequestID
		if m.Scope.Granularity == "account_period" {
			key = "account_period"
			if r.RequestID != "" {
				return out, fmt.Errorf("account-period report cannot claim request granularity")
			}
		} else if key == "" {
			return out, fmt.Errorf("request report requires supplier request ID")
		}
		if _, found := vendor[key]; found {
			return out, fmt.Errorf("duplicate supplier key %q", key)
		}
		if r.Amount != nil {
			if _, err := vendorNumber(*r.Amount); err != nil {
				return out, err
			}
		}
		if r.Buckets != nil {
			for _, n := range r.Buckets {
				if n < 0 {
					return out, fmt.Errorf("negative supplier usage")
				}
			}
		}
		vendor[key] = r
	}
	out.ID = VendorReportID(m, rows)
	groups := map[string][]VendorLocalAttempt{}
	seen, reservations := map[string]bool{}, map[string]bool{}
	excluded := map[string]bool{}
	for _, v := range m.ExcludedAttempts {
		excluded[v.ID] = true
	}
	for _, a := range local {
		if a.ID == "" || seen[a.ID] {
			return out, fmt.Errorf("duplicate or missing local attempt ID")
		}
		seen[a.ID] = true
		if excluded[a.ID] {
			continue
		}
		if a.ExecutedAt.IsZero() {
			out.UnknownTimeAttempts++
			out.UnresolvedAttempts = append(out.UnresolvedAttempts, a)
			continue
		}
		if a.ExecutedAt.Before(m.Scope.Start) || !a.ExecutedAt.Before(m.Scope.End) {
			continue
		}
		var mapping *VendorSourceMapping
		for _, v := range m.Mappings {
			if v.SourceKind == a.SourceKind && v.SourceID == a.SourceID && !a.ExecutedAt.Before(v.Start) && a.ExecutedAt.Before(v.End) {
				copy := v
				mapping = &copy
				break
			}
		}
		if mapping == nil {
			out.UnmappedAttempts++
			out.UnresolvedAttempts = append(out.UnresolvedAttempts, a)
			continue
		}
		if mapping.Provider != m.Scope.Provider || mapping.Account != m.Scope.Account || m.Scope.Project != "" && mapping.Project != m.Scope.Project || m.Scope.Model != "" && a.Model != m.Scope.Model {
			continue
		}
		switch a.Purpose {
		case "user":
			if a.ReservationID == "" {
				return out, fmt.Errorf("user attempt missing reservation")
			}
			reservations[a.ReservationID] = true
		case "canary", "health_probe", "internal_test":
			out.InternalAttempts++
		default:
			return out, fmt.Errorf("invalid attempt purpose")
		}
		key := a.RequestID
		if m.Scope.Granularity == "account_period" {
			key = "account_period"
		} else if key == "" {
			key = "unidentified:" + a.ID
		}
		groups[key] = append(groups[key], a)
		out.Attempts = append(out.Attempts, a)
	}
	out.UserReservations = len(reservations)
	keys := map[string]bool{}
	for k := range vendor {
		keys[k] = true
	}
	for k := range groups {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	pending := !m.Complete || !m.PagesComplete || m.VendorWatermark.Before(m.Scope.End) || m.LocalWatermark.Before(m.Scope.End) || m.CoverageEvidenceID == "" || out.UnknownTimeAttempts > 0 || out.UnmappedAttempts > 0
	if len(ordered) == 0 {
		pending = true
	}
	for _, k := range ordered {
		v, hasVendor := vendor[k]
		attempts := groups[k]
		c := VendorComparison{Key: k, Status: "matched", UsageStatus: "unknown", AmountStatus: "unknown", VendorAmount: v.Amount, VendorBuckets: v.Buckets, PurposeCounts: map[string]int{}}
		amount := new(big.Rat)
		buckets := [5]int64{}
		usageKnown := len(attempts) > 0
		for _, a := range attempts {
			c.Attempts = append(c.Attempts, a.ID)
			c.PurposeCounts[a.Purpose]++
			if !a.Complete {
				pending = true
			}
			if a.EstimatedAmount == nil || a.Currency != m.Scope.Currency || a.Unit != m.Scope.Unit {
				c.UnknownAmounts++
			} else {
				n, err := vendorNumber(*a.EstimatedAmount)
				if err != nil {
					return out, err
				}
				if n.Sign() < 0 {
					return out, fmt.Errorf("negative local tariff estimate")
				}
				amount.Add(amount, n)
			}
			if a.Buckets == nil {
				usageKnown = false
			} else {
				for i, n := range a.Buckets {
					if n < 0 || n > int64(^uint64(0)>>1)-buckets[i] {
						return out, fmt.Errorf("invalid/overflowing attempt buckets")
					}
					buckets[i] += n
				}
			}
		}
		sort.Strings(c.Attempts)
		noActivity := false
		if hasVendor && len(attempts) == 0 && m.Scope.Granularity == "account_period" && m.Mode == "tokens" && v.Amount != nil {
			n, _ := vendorNumber(*v.Amount)
			noActivity = n.Sign() == 0 && (v.Buckets == nil || *v.Buckets == [5]int64{})
			if noActivity {
				c.EstimatedAmount = vendorPtr(amount)
				c.LocalBuckets = &buckets
			}
		}
		if len(attempts) > 0 && c.UnknownAmounts == 0 {
			c.EstimatedAmount = vendorPtr(amount)
		}
		if usageKnown {
			c.LocalBuckets = &buckets
		}
		if !hasVendor {
			c.Status = "missing_vendor"
		} else if len(attempts) == 0 && m.Mode == "tokens" && !noActivity {
			c.Status = "missing_local"
		} else if m.Scope.Granularity == "request" && len(attempts) > 1 {
			c.Status = "ambiguous"
			pending = true
		}
		if c.LocalBuckets != nil && v.Buckets != nil {
			c.UsageStatus = "matched"
			if *c.LocalBuckets != *v.Buckets {
				c.UsageStatus = "variance"
			}
		}
		if v.Buckets == nil {
			c.UsageStatus = "not_applicable"
		}
		if c.EstimatedAmount != nil && v.Amount != nil && m.Mode == "tokens" {
			n, _ := vendorNumber(*v.Amount)
			diff := new(big.Rat).Sub(n, amount)
			c.Difference = vendorPtr(diff)
			c.Unexplained = vendorPtr(diff)
			abs := new(big.Rat).Abs(diff)
			limit, _ := vendorNumber(m.AbsoluteTolerance)
			relative, _ := vendorNumber(m.RelativeTolerance)
			// Absolute discrepancy is always checked. A zero supplier denominator
			// never converts local>0 into a passing relative comparison.
			if n.Sign() != 0 {
				rel := new(big.Rat).Quo(abs, new(big.Rat).Abs(n))
				c.RelativeDifference = vendorPtr(rel)
				limit.Add(limit, new(big.Rat).Mul(new(big.Rat).Abs(n), relative))
			}
			c.AmountStatus = "matched"
			if abs.Cmp(limit) > 0 {
				c.AmountStatus = "variance"
			}
		}
		if c.Status == "matched" {
			if c.AmountStatus == "variance" || c.UsageStatus == "variance" {
				c.Status = "variance"
			} else if c.AmountStatus == "unknown" || c.UsageStatus == "unknown" {
				c.Status = "unknown"
			}
		}
		if m.Mode == "monthly" {
			c.UsageStatus = "not_applicable"
			c.AmountStatus = "not_applicable"
			c.EstimatedAmount = nil
			c.Difference = nil
			c.Unexplained = nil
			c.Status = "not_applicable"
			if v.Amount == nil {
				c.Status = "unknown"
			}
		}
		if m.Mode == "balance_only" || m.Mode == "unknown" {
			c.Status = "unknown"
			c.UsageStatus = "not_comparable"
			c.AmountStatus = "unknown"
			c.Difference = nil
			c.Unexplained = nil
		}
		out.Comparisons = append(out.Comparisons, c)
		if c.Status != "matched" && c.Status != "not_applicable" {
			out.Status = "attention"
		}
	}
	if m.Mode == "monthly" && out.Status == "matched" {
		out.Status = "not_applicable"
	}
	if pending {
		out.Status = "pending"
	}
	// Even a matched estimate does not prove a supplier charge. This field
	// describes only the independently reported supplier amount's true scope.
	if !pending && (m.Mode == "tokens" || m.Mode == "monthly") && len(rows) > 0 {
		allAmounts := true
		for _, r := range rows {
			if r.Amount == nil {
				allAmounts = false
			}
		}
		if allAmounts && m.CostBasis == "invoice" {
			out.VerificationScope = m.Scope.Granularity
		}
	}
	return out, nil
}

func AttributeVendorDifference(c *VendorComparison, items []VendorAttribution) error {
	if c.Difference == nil {
		return fmt.Errorf("unknown difference cannot be attributed")
	}
	remaining, err := vendorNumber(*c.Difference)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range items {
		switch a.Category {
		case "failed_or_cancelled", "retry_or_failover", "cache_semantics", "reasoning", "tools_multimodal", "internal", "contract_discount", "currency_tax_credit_rounding", "late_or_incomplete":
		default:
			return fmt.Errorf("invalid attribution category")
		}
		if a.EvidenceID == "" || seen[a.EvidenceID] || (a.Status != "hypothesis" && a.Status != "confirmed") || (a.Scope != "request" && a.Scope != "account_period") {
			return fmt.Errorf("invalid or repeated attribution evidence")
		}
		seen[a.EvidenceID] = true
		n, err := vendorNumber(a.Amount)
		if err != nil {
			return err
		}
		if a.Status == "confirmed" {
			remaining.Sub(remaining, n)
		}
	}
	c.Attributions = append([]VendorAttribution(nil), items...)
	c.Unexplained = vendorPtr(remaining)
	return nil
}
