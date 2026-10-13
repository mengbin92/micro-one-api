package execution

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/evidence"
	"testing"
	"time"
)

func TestExecutionEventIdentityPurposeAndDigest(t *testing.T) {
	i := Intent{ReservationID: "res", RequestID: "attempt", RootRequestID: "root", AttemptNumber: 1, Purpose: "user", SourceKind: "channel", SourceID: 1, Model: "vendor/ExactModel", UsageContractVersion: 2, ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, CostAlgorithmVersion: AlgorithmVersion, Policy: Policy, LeaseUntil: 10, EvidenceDeadline: 20, CostBasis: "unknown"}
	e := NewIntentEvent(i, time.Unix(1, 0))
	_, digest, err := e.Encode()
	require.NoError(t, err)
	require.Len(t, digest, 64)
	e.Intent.Purpose = "canary"
	e.ID = e.Key()
	_, _, err = e.Encode()
	require.Error(t, err, "canary must never carry a user reservation")
	e.Intent.ReservationID = ""
	e.ID = e.Key()
	_, _, err = e.Encode()
	require.NoError(t, err)
	e.Final = []byte(`{}`)
	e.Category = "final"
	e.ID = e.Key()
	_, _, err = e.Encode()
	require.Error(t, err)
}
func TestProbeRequiresExplicitUnknownExposure(t *testing.T) {
	base := `[{"id":"probe","source_kind":"channel","source_id":1,"model":"Exact","protocol":"openai_chat","calls_per_day":2,"input_tokens_per_day":256,"output_tokens_per_day":32,"max_output_tokens":16,"cost_per_day":0,"unknown_cost_calls_per_day":%d}]`
	_, err := DecodeProbeTargets(fmt.Sprintf(base, 0))
	require.Error(t, err)
	targets, err := DecodeProbeTargets(fmt.Sprintf(base, 1))
	require.NoError(t, err)
	require.Nil(t, targets[0].CostUpperBound)
}

func TestProbeOutputDoesNotTrustTextToolMarkers(t *testing.T) {
	require.Error(t, ValidateProbeOutput([]byte(`{"usage":{"prompt_tokens":1},"choices":[{"message":{"content":"p1_probe"}}]}`), "openai_chat", false, true))
	require.NoError(t, ValidateProbeOutput([]byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"p1_probe","arguments":"{}"}}]}}]}`), "openai_chat", false, true))
	require.Error(t, ValidateProbeOutput([]byte(`{"usage":{"prompt_tokens":1}}`), "openai_chat", false, false))
	diff := CompareFields([]string{"/input:number", "/old:number"}, []string{"/input:string", "/new:number"})
	require.Equal(t, []string{"/new"}, diff.Added)
	require.Equal(t, []string{"/old"}, diff.Missing)
	require.Equal(t, []string{"/input:number->string"}, diff.TypeChanged)
}
