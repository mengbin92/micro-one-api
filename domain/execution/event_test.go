package execution

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/evidence"
	"strings"
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

func TestProbeOutputRequiresActualContentAndValidToolInput(t *testing.T) {
	for _, tc := range []struct {
		protocol, body string
		tools          bool
	}{
		{"openai_chat", `{"choices":[{"message":{}}]}`, false},
		{"responses", `{"output":[{}]}`, false},
		{"anthropic_messages", `{"delta":{}}`, false},
		{"gemini", `{"candidates":[{"content":{"parts":[{}]}}]}`, false},
		{"openai_chat", `{"choices":[{"message":{"tool_calls":[{"function":{"name":"p1_probe","arguments":"broken"}}]}}]}`, true},
		{"responses", `{"output":[{"type":"function_call","name":"p1_probe","arguments":"{\"unexpected\":1}"}]}`, true},
		{"anthropic_messages", `{"content":[{"type":"tool_use","name":"p1_probe","input":null}]}`, true},
		{"gemini", `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"p1_probe","args":[]}}]}}]}`, true},
	} {
		t.Run(tc.protocol+tc.body, func(t *testing.T) {
			require.Error(t, ValidateProbeOutput([]byte(tc.body), tc.protocol, false, tc.tools))
		})
	}
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"p1_probe\",\"arguments\":\"{\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n"
	require.NoError(t, ValidateProbeOutput([]byte(stream), "openai_chat", true, true))
	require.Error(t, ValidateProbeOutput([]byte(strings.Replace(stream, `"arguments":"}"`, `"arguments":"bad}"`, 1)), "openai_chat", true, true))
}
