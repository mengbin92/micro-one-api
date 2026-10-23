package biz

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"
)

const VendorRetrospectiveRevision = "retrospective-three-bucket-v1"

// VendorRetrospectiveEvidence is an operator-submitted archive, not a source
// of execution events, frozen prices, supplier finality or coverage. Artifact
// references commit to externally retained bytes; the owner cannot authenticate
// those bytes or reconstruct omitted timing/ledger identifiers from a digest.
type VendorRetrospectiveEvidence struct {
	ReportID           string                            `json:"report_id"`
	SupplierEvidenceID string                            `json:"supplier_evidence_id"`
	Scope              VendorScope                       `json:"scope"`
	SourceKind         string                            `json:"source_kind"`
	SourceID           int64                             `json:"source_id"`
	Model              string                            `json:"upstream_model_id"`
	MappingRevision    string                            `json:"mapping_revision"`
	MappingEvidenceID  string                            `json:"mapping_evidence_id"`
	SnapshotEvidenceID string                            `json:"snapshot_evidence_id"`
	RecordsEvidenceID  string                            `json:"records_evidence_id"`
	TariffEvidenceID   string                            `json:"tariff_evidence_id"`
	AnalysisEvidenceID string                            `json:"analysis_evidence_id,omitempty"`
	ExternalEvidenceID string                            `json:"external_evidence_id,omitempty"`
	TariffWindows      []VendorRetrospectiveTariffWindow `json:"tariff_windows"`
	Records            []VendorRetrospectiveRecord       `json:"records"`
}

type VendorRetrospectiveTariffWindow struct {
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	Band              string    `json:"band"`
	UncachedInputRate string    `json:"uncached_input_rate_per_million"`
	CacheReadRate     string    `json:"cache_read_rate_per_million"`
	OutputRate        string    `json:"output_rate_per_million"`
}

// Pointer counts distinguish an explicitly observed zero from missing usage.
// The two unsupported cache-creation buckets are deliberately absent.
type VendorRetrospectiveRecord struct {
	ID                  string    `json:"id"`
	Purpose             string    `json:"purpose"`
	TimeBasis           string    `json:"time_basis"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
	EndExclusive        time.Time `json:"end_exclusive"`
	UncachedInputTokens *int64    `json:"uncached_input_tokens"`
	CacheReadTokens     *int64    `json:"cache_read_tokens"`
	OutputTokens        *int64    `json:"output_tokens"`
}

type VendorRetrospectiveTotals struct {
	Records             int    `json:"records"`
	UncachedInputTokens int64  `json:"uncached_input_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	Amount              string `json:"amount"`
}

// VendorRetrospectiveResult reports exact arithmetic on submitted archive
// inputs. It never participates in the ordinary reconciliation comparisons.
type VendorRetrospectiveResult struct {
	Revision                string                               `json:"revision"`
	ReportID                string                               `json:"report_id"`
	EvidenceID              string                               `json:"evidence_id"`
	Basis                   string                               `json:"basis"`
	Provenance              string                               `json:"provenance"`
	ExecutionJoinVerified   bool                                 `json:"execution_join_verified"`
	OwnerArithmeticReplayed bool                                 `json:"owner_arithmetic_replayed"`
	Currency                string                               `json:"currency"`
	Unit                    string                               `json:"unit"`
	Records                 int                                  `json:"records"`
	Totals                  VendorRetrospectiveTotals            `json:"totals"`
	ByPurpose               map[string]VendorRetrospectiveTotals `json:"by_purpose"`
	ByBand                  map[string]VendorRetrospectiveTotals `json:"by_band"`
	Amount                  string                               `json:"amount"`
	SupplierAmount          string                               `json:"supplier_amount"`
	Difference              string                               `json:"difference"` // Supplier minus retrospective arithmetic.
	ArithmeticStatus        string                               `json:"arithmetic_status"`
}

var vendorRetrospectiveDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var vendorRetrospectiveLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

type retrospectiveWindow struct {
	start, end time.Time
	band       string
	rates      [3]*big.Rat
}

type retrospectiveAccumulator struct {
	records int
	tokens  [3]int64
	amount  big.Rat
}

func (a *retrospectiveAccumulator) add(tokens [3]int64, amount *big.Rat) error {
	for i, count := range tokens {
		if count > math.MaxInt64-a.tokens[i] {
			return fmt.Errorf("retrospective usage total overflows int64")
		}
		a.tokens[i] += count
	}
	a.records++
	a.amount.Add(&a.amount, amount)
	return nil
}

func (a *retrospectiveAccumulator) total() VendorRetrospectiveTotals {
	return VendorRetrospectiveTotals{Records: a.records, UncachedInputTokens: a.tokens[0], CacheReadTokens: a.tokens[1], OutputTokens: a.tokens[2], Amount: vendorString(&a.amount)}
}

// ReplayVendorRetrospective binds submitted inputs to one supplier report and
// observed source mapping, then repeats only their tariff arithmetic. It does
// not load local attempts or promote any execution, billing or coverage fact.
func ReplayVendorRetrospective(m VendorManifest, rows []VendorBillRow) (*VendorRetrospectiveResult, error) {
	e := m.RetrospectiveEvidence
	if e == nil {
		return nil, nil
	}
	if err := ValidateVendorManifest(m); err != nil {
		return nil, err
	}
	if m.Scope.Granularity != "account_period" || m.Scope.Unit != "currency" || m.Mode != "tokens" || m.Scope.Model == "" || e.Model != m.Scope.Model || CanonicalVendorScope(e.Scope) != CanonicalVendorScope(m.Scope) || e.ReportID != VendorReportID(m, rows) || e.SupplierEvidenceID != m.EvidenceID {
		return nil, fmt.Errorf("retrospective evidence requires the exact supplier report, model and account-period currency scope")
	}
	if len(e.Records) < 1 || len(e.Records) > 10000 || len(e.TariffWindows) < 1 || len(e.TariffWindows) > 1000 {
		return nil, fmt.Errorf("retrospective evidence exceeds bounded record or tariff-window limits")
	}
	raw, err := jsonx.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("retrospective evidence exceeds the 2 MiB document limit")
	}
	for _, id := range []string{e.SnapshotEvidenceID, e.RecordsEvidenceID, e.TariffEvidenceID} {
		if !vendorRetrospectiveDigest.MatchString(id) {
			return nil, fmt.Errorf("retrospective artifact references require canonical SHA256 identities")
		}
	}
	if e.AnalysisEvidenceID != "" && !vendorRetrospectiveDigest.MatchString(e.AnalysisEvidenceID) {
		return nil, fmt.Errorf("invalid retrospective analysis artifact identity")
	}
	if e.ExternalEvidenceID != "" && !vendorRetrospectiveDigest.MatchString(e.ExternalEvidenceID) {
		return nil, fmt.Errorf("invalid retrospective external artifact identity")
	}
	var mapping *VendorSourceMapping
	for _, candidate := range m.Mappings {
		if candidate.SourceKind == e.SourceKind && candidate.SourceID == e.SourceID && candidate.Revision == e.MappingRevision && candidate.EvidenceID == e.MappingEvidenceID && candidate.Provider == e.Scope.Provider && candidate.Account == e.Scope.Account && candidate.Project == e.Scope.Project {
			if mapping != nil {
				return nil, fmt.Errorf("retrospective source mapping is ambiguous")
			}
			copy := candidate
			mapping = &copy
		}
	}
	if mapping == nil {
		return nil, fmt.Errorf("retrospective source mapping does not match the report")
	}

	windows := make([]retrospectiveWindow, 0, len(e.TariffWindows))
	bandRates := map[string][3]*big.Rat{}
	previousEnd := m.Scope.Start
	for _, window := range e.TariffWindows {
		if !window.Start.Equal(previousEnd) || !window.Start.Before(window.End) || window.End.After(m.Scope.End) || !vendorRetrospectiveLabel.MatchString(window.Band) {
			return nil, fmt.Errorf("retrospective tariff windows must tile the report scope in order without gaps or overlap")
		}
		var rates [3]*big.Rat
		for i, text := range []string{window.UncachedInputRate, window.CacheReadRate, window.OutputRate} {
			_, fraction, _ := strings.Cut(text, ".")
			rate, err := vendorNumber(text)
			if err != nil || len(fraction) > 12 || rate.Sign() < 0 {
				return nil, fmt.Errorf("retrospective rates require nonnegative decimal values with at most 12 fractional digits")
			}
			rates[i] = rate
		}
		if previous, ok := bandRates[window.Band]; ok {
			for i, rate := range rates {
				if rate.Cmp(previous[i]) != 0 {
					return nil, fmt.Errorf("retrospective tariff band has inconsistent rates")
				}
			}
		}
		bandRates[window.Band] = rates
		windows = append(windows, retrospectiveWindow{start: window.Start, end: window.End, band: window.Band, rates: rates})
		previousEnd = window.End
	}
	if !previousEnd.Equal(m.Scope.End) {
		return nil, fmt.Errorf("retrospective tariff windows do not cover the full report scope")
	}

	var total retrospectiveAccumulator
	byPurpose, byBand := map[string]*retrospectiveAccumulator{}, map[string]*retrospectiveAccumulator{}
	seen := map[string]bool{}
	for _, record := range e.Records {
		if !vendorRetrospectiveLabel.MatchString(record.ID) || seen[record.ID] {
			return nil, fmt.Errorf("retrospective record identity is missing, invalid or duplicated")
		}
		seen[record.ID] = true
		switch record.Purpose {
		case "user", "internal_test", "canary", "health_probe":
		default:
			return nil, fmt.Errorf("invalid retrospective record purpose")
		}
		if record.Purpose == "internal_test" && e.ExternalEvidenceID == "" {
			return nil, fmt.Errorf("retrospective internal tests require an external artifact reference")
		}
		if record.TimeBasis != "request_handler_and_settlement_interval" && record.TimeBasis != "external_direct_call_interval" || record.TimeBasis == "external_direct_call_interval" && record.Purpose != "internal_test" {
			return nil, fmt.Errorf("invalid retrospective time basis or external test purpose")
		}
		if record.StartedAt.IsZero() || record.FinishedAt.Before(record.StartedAt) || !record.FinishedAt.Before(record.EndExclusive) || record.StartedAt.Before(m.Scope.Start) || record.EndExclusive.After(m.Scope.End) || record.StartedAt.Before(mapping.Start) || record.EndExclusive.After(mapping.End) {
			return nil, fmt.Errorf("retrospective interval is invalid or outside the report and observed mapping")
		}
		index := sort.Search(len(windows), func(i int) bool { return windows[i].end.After(record.StartedAt) })
		if index == len(windows) || record.StartedAt.Before(windows[index].start) || record.EndExclusive.After(windows[index].end) {
			return nil, fmt.Errorf("retrospective interval crosses a tariff boundary")
		}
		window := windows[index]
		var tokens [3]int64
		amount := new(big.Rat)
		for i, count := range []*int64{record.UncachedInputTokens, record.CacheReadTokens, record.OutputTokens} {
			if count == nil || *count < 0 {
				return nil, fmt.Errorf("retrospective usage requires all three explicitly observed nonnegative buckets")
			}
			tokens[i] = *count
			amount.Add(amount, new(big.Rat).Mul(new(big.Rat).SetInt64(*count), window.rates[i]))
		}
		amount.Quo(amount, big.NewRat(1000000, 1))
		if err := total.add(tokens, amount); err != nil {
			return nil, err
		}
		if byPurpose[record.Purpose] == nil {
			byPurpose[record.Purpose] = &retrospectiveAccumulator{}
		}
		if byBand[window.band] == nil {
			byBand[window.band] = &retrospectiveAccumulator{}
		}
		if err := byPurpose[record.Purpose].add(tokens, amount); err != nil {
			return nil, err
		}
		if err := byBand[window.band].add(tokens, amount); err != nil {
			return nil, err
		}
	}
	supplierAmount, err := VendorAmountTotal(rows)
	if err != nil {
		return nil, err
	}
	supplier, err := vendorNumber(supplierAmount)
	if err != nil {
		return nil, err
	}
	difference := new(big.Rat).Sub(supplier, &total.amount)
	status := "arithmetic_differs"
	if difference.Sign() == 0 {
		status = "arithmetic_agrees"
	}
	result := &VendorRetrospectiveResult{
		Revision: VendorRetrospectiveRevision, ReportID: e.ReportID, EvidenceID: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)),
		Basis: "retrospective_tariff_recomputation", Provenance: "operator_submitted_archive", ExecutionJoinVerified: false, OwnerArithmeticReplayed: true,
		Currency: m.Scope.Currency, Unit: m.Scope.Unit, Records: total.records, Totals: total.total(), Amount: vendorString(&total.amount), SupplierAmount: supplierAmount, Difference: vendorString(difference), ArithmeticStatus: status,
		ByPurpose: map[string]VendorRetrospectiveTotals{}, ByBand: map[string]VendorRetrospectiveTotals{},
	}
	for purpose, value := range byPurpose {
		result.ByPurpose[purpose] = value.total()
	}
	for band, value := range byBand {
		result.ByBand[band] = value.total()
	}
	return result, nil
}

// ValidateVendorRetrospectiveResult prevents a direct persistence caller from
// submitting invented derived amounts, stronger provenance or verification.
func ValidateVendorRetrospectiveResult(report *VendorReconciliation) error {
	if report == nil {
		return fmt.Errorf("missing retrospective reconciliation report")
	}
	expected, err := ReplayVendorRetrospective(report.Manifest, report.Rows)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, report.RetrospectiveResult) {
		return fmt.Errorf("retrospective result does not match owner arithmetic replay")
	}
	return nil
}
