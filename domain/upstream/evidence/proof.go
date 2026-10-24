package evidence

import (
	"bytes"
	"context"
	"fmt"

	"micro-one-api/domain/upstream/contract"
	"micro-one-api/pkg/jsonx"
)

// ContractProof is an independent observation of the same upstream response.
// It proves an installed parameter contract, never a billing parser or charge.
type ContractProof struct {
	Observation Observation `json:"observation"`
	Complete    bool        `json:"complete"`
}

func independentProof(d contract.Document) bool {
	return contract.InstalledProfile(d) && d.ParserRevision == contract.DeepSeekProofParserID && d.UsageRevision == contract.DeepSeekProofUsageID
}

func proofParser(d contract.Document) func([]byte) Observation {
	if !independentProof(d) {
		return nil
	}
	return func(raw []byte) Observation {
		o := ParseDeepSeekCandidate(raw)
		o.ParserRevision, o.ContractRevision = contract.DeepSeekProofParserID, contract.DeepSeekProofUsageID
		return o
	}
}

func selectedProof(ctx context.Context, trace *contract.Trace) (func([]byte) Observation, *Tracker) {
	if trace == nil {
		return nil, nil
	}
	for _, snapshot := range contract.Snapshots(ctx) {
		if snapshot.Revision != trace.Revision || snapshot.Document.Scope != trace.Scope {
			continue
		}
		if parse := proofParser(snapshot.Document); parse != nil {
			return parse, &Tracker{protocol: trace.Scope.Protocol, usage: map[string]any{}, candidateParser: parse}
		}
	}
	return nil, nil
}

// ContractObservation selects proof only through the authoritative document.
// A producer cannot choose a parser by putting a revision on its observation.
func ContractObservation(r *Record, d contract.Document) Observation {
	if r == nil {
		return Observation{}
	}
	if independentProof(d) {
		if r.ContractProof != nil {
			return r.ContractProof.Observation
		}
		return Observation{}
	}
	return r.Observation
}

func ContractComplete(r *Record, d contract.Document) bool {
	if r == nil {
		return false
	}
	if independentProof(d) {
		return r.ContractProof != nil && r.ContractProof.Complete
	}
	return r.Complete
}

// ValidateContractObservation re-parses raw metering under the installed
// document profile. It intentionally does not turn a false completion claim
// into a successful proof; completion is checked separately by ContractVerified.
func ValidateContractObservation(r *Record, d contract.Document) error {
	if r == nil || !contract.InstalledProfile(d) {
		return fmt.Errorf("contract proof profile unavailable")
	}
	o := ContractObservation(r, d)
	if o.Protocol != d.Scope.Protocol || o.ParserRevision != d.ParserRevision || o.ContractRevision != d.UsageRevision || o.Status != "verified" || o.Buckets == nil || len(o.Usage) == 0 || len(o.Usage) > MaxUsageBytes {
		return fmt.Errorf("contract proof identity or usage invalid")
	}
	if independentProof(d) {
		trace := r.ParameterContract
		if trace == nil || trace.Scope != d.Scope || trace.Revision != d.Revision() || trace.Build != d.AdapterBuild {
			return fmt.Errorf("contract proof does not match frozen request snapshot")
		}
		// JSON has one native usage object. SSE proof follows its own tracker,
		// because the native parser may have stopped at an earlier null chunk.
		if !r.Stream && (!bytes.Equal(o.Usage, r.Observation.Usage) || o.UsageDigest != r.Observation.UsageDigest) {
			return fmt.Errorf("contract proof differs from native response usage")
		}
	} else if r.ContractProof != nil {
		return fmt.Errorf("independent proof is not installed for this contract")
	}
	field := "usage"
	if d.Scope.Protocol == "gemini" {
		field = "usageMetadata"
	}
	raw, err := jsonx.Marshal(map[string]jsonx.RawMessage{field: o.Usage})
	if err != nil {
		return fmt.Errorf("contract proof usage is not JSON")
	}
	parse := proofParser(d)
	var actual Observation
	if parse == nil {
		actual = Parse(raw, d.Scope.Protocol)
	} else {
		actual = parse(raw)
	}
	if actual.Status != "verified" || actual.Buckets == nil || *actual.Buckets != *o.Buckets || actual.Semantics != o.Semantics || actual.UsageDigest != o.UsageDigest {
		return fmt.Errorf("contract proof does not match independently parsed native usage")
	}
	return nil
}

func ContractVerified(r *Record, d contract.Document) bool {
	return ContractComplete(r, d) && (r.HTTPStatus == 0 || r.HTTPStatus >= 200 && r.HTTPStatus < 300) && ValidateContractObservation(r, d) == nil
}
