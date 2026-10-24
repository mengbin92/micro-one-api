// Package execution defines the durable upstream evidence delivery contract.
package execution

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"time"

	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
)

const Version int32 = 1
const AlgorithmVersion = "five-bucket-fixedpoint-2026-10-13.1"
const Policy = "native-v2-release-unproven-2026-10-13"
const MaxEventBytes = 256 << 10

type Intent struct {
	TariffSnapshotID     string           `json:"supplier_tariff_snapshot_id,omitempty"`
	UsageContractVersion int32            `json:"usage_contract_version"`
	ParserRevision       string           `json:"parser_revision"`
	ContractRevision     string           `json:"contract_revision"`
	CostAlgorithmVersion string           `json:"cost_algorithm_version"`
	ReservationID        string           `json:"reservation_id,omitempty"`
	RequestID            string           `json:"request_id"`
	RootRequestID        string           `json:"root_request_id"`
	AttemptNumber        int32            `json:"attempt_number"`
	Purpose              string           `json:"purpose"`
	SourceKind           string           `json:"source_kind"`
	SourceID             int64            `json:"source_id"`
	Model                string           `json:"upstream_model_id"`
	Policy               string           `json:"settlement_policy_version"`
	LeaseUntil           int64            `json:"lease_until"`
	EvidenceDeadline     int64            `json:"evidence_deadline"`
	Tariff               jsonx.RawMessage `json:"supplier_tariff,omitempty"`
	CostBasis            string           `json:"cost_basis"`
}

type Event struct {
	Version       int32            `json:"version"`
	ID            string           `json:"event_id"`
	Intent        Intent           `json:"intent"`
	Category      string           `json:"category"`
	Sequence      int64            `json:"sequence"`
	ObservedAt    int64            `json:"observed_at"`
	Record        *evidence.Record `json:"record,omitempty"`
	Final         jsonx.RawMessage `json:"final,omitempty"`
	BuildRevision string           `json:"build_revision"`
}

type Receipt struct {
	Comparison         jsonx.RawMessage
	Received           bool
	SettlementAccepted bool
	Settled            bool
	Status             string
	Digest             string
	Amount             int64
}

func (i Intent) Key() string {
	if i.ReservationID != "" {
		return i.ReservationID
	}
	return i.RequestID + "/" + i.Purpose
}
func (e Event) Key() string { return fmt.Sprintf("%s/%s/%d", e.Intent.Key(), e.Category, e.Sequence) }
func (e Event) Encode() ([]byte, string, error) {
	if err := e.Validate(); err != nil {
		return nil, "", err
	}
	if e.Record != nil {
		copy := *e.Record
		e.Record = &copy
	}
	b, err := jsonx.Marshal(e)
	if err != nil {
		return nil, "", err
	}
	// Keep the terminal summary and account for secondary samples dropped by the byte cap.
	for len(b) > MaxEventBytes && e.Record != nil && len(e.Record.Events) > 0 {
		e.Record.Events = e.Record.Events[:len(e.Record.Events)-1]
		e.Record.DroppedEvents++
		b, err = jsonx.Marshal(e)
		if err != nil {
			return nil, "", err
		}
	}
	if len(b) > MaxEventBytes {
		return nil, "", fmt.Errorf("execution event exceeds limit")
	}
	return b, fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
func (e Event) Validate() error {
	i := e.Intent
	if e.Version != Version || e.Sequence < 0 || e.ID != e.Key() || len(e.ID) > 255 || e.ObservedAt <= 0 || i.RequestID == "" || len(i.RequestID) > 128 || i.RootRequestID == "" || len(i.RootRequestID) > 128 || i.AttemptNumber < 1 || i.SourceID <= 0 || (i.SourceKind != "channel" && i.SourceKind != "subscription") || (strings.TrimSpace(i.Model) == "" && i.Purpose != "health_probe") || len(i.Model) > 255 || i.Policy != Policy || i.UsageContractVersion != 2 || i.ParserRevision == "" || i.ContractRevision == "" || i.CostAlgorithmVersion != AlgorithmVersion || i.LeaseUntil <= 0 || i.EvidenceDeadline < i.LeaseUntil || (i.CostBasis != "unknown" && i.CostBasis != "tariff_estimate") {
		return fmt.Errorf("invalid execution identity or frozen policy")
	}
	switch i.Purpose {
	case "user":
		if i.ReservationID == "" {
			return fmt.Errorf("user execution requires reservation")
		}
	case "canary", "health_probe", "internal_test":
		if i.ReservationID != "" || len(e.Final) > 0 {
			return fmt.Errorf("internal execution cannot settle user funds")
		}
	default:
		return fmt.Errorf("invalid purpose")
	}
	switch e.Category {
	case "intent":
		if e.Sequence != 0 || e.Record != nil || len(e.Final) > 0 {
			return fmt.Errorf("invalid intent")
		}
	case "checkpoint", "terminal":
		if e.Record == nil || len(e.Final) > 0 {
			return fmt.Errorf("evidence required")
		}
	case "final":
		if e.Sequence != 0 || len(e.Final) == 0 || !jsonx.Valid(e.Final) {
			return fmt.Errorf("invalid final")
		}
	default:
		return fmt.Errorf("invalid category")
	}
	if r := e.Record; r != nil {
		if len(r.Events) > evidence.MaxEvents {
			return fmt.Errorf("native evidence exceeds limits")
		}
		observations := []evidence.Observation{r.Observation}
		if r.ContractProof != nil {
			observations = append(observations, r.ContractProof.Observation)
		}
		for _, observation := range observations {
			if len(observation.Usage) > evidence.MaxUsageBytes || len(observation.Fingerprint) > 2048 || len(observation.ProviderRequestID) > 256 {
				return fmt.Errorf("native evidence exceeds limits")
			}
			if b := observation.Buckets; b != nil {
				sum := int64(0)
				for _, v := range b {
					if v < 0 || v > math.MaxInt64-sum {
						return fmt.Errorf("invalid native buckets")
					}
					sum += v
				}
			}
		}
	}
	if i.CostBasis == "tariff_estimate" && (len(i.Tariff) == 0 && i.TariffSnapshotID == "" || len(i.Tariff) > 0 && !jsonx.Valid(i.Tariff)) {
		return fmt.Errorf("tariff estimate requires frozen tariff")
	}
	return nil
}
func NewIntentEvent(i Intent, now time.Time) Event {
	e := Event{Version: Version, Intent: i, Category: "intent", ObservedAt: now.Unix(), BuildRevision: evidence.BuildRevision}
	e.ID = e.Key()
	return e
}

func Freeze(i Intent) Intent {
	i.UsageContractVersion = 2
	i.ParserRevision = evidence.ParserRevision
	i.ContractRevision = evidence.ContractRevision
	i.CostAlgorithmVersion = AlgorithmVersion
	return i
}
