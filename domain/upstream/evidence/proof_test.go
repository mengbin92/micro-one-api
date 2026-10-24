package evidence

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/pkg/jsonx"
)

func deepSeekProofDocument() contract.Document {
	return contract.Document{Scope: contract.Scope{Kind: "channel", ID: 11, Model: "deepseek-flash", Protocol: "openai_chat"}, Provider: "deepseek", AdapterBuild: contract.BuildID, ParserRevision: contract.DeepSeekProofParserID, UsageRevision: contract.DeepSeekProofUsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "deepseek-official-pilot-2026-10-14", BasisRevision: "deepseek-official-cache-partition"}
}

func TestContractProofPreservesNativeProfileAcrossJSONAndSSE(t *testing.T) {
	raw, err := os.ReadFile("testdata/deepseek-official-pilot.json")
	require.NoError(t, err)
	var fixtures []struct {
		Name            string
		Stream          bool
		Body            string
		ExpectedBuckets [5]int64 `json:"expected_buckets"`
	}
	require.NoError(t, jsonx.Unmarshal(raw, &fixtures))
	doc := deepSeekProofDocument()
	snapshot, err := contract.Compile(doc)
	require.NoError(t, err)
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			var record Record
			ctx := contract.WithSelection(context.Background(), contract.Selection{Snapshots: []contract.Snapshot{snapshot}, Inbound: "openai_chat"})
			ctx = WithRecorder(ctx, func(r Record) { record = r })
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				contentType := "application/json"
				if f.Stream {
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(f.Body))}, nil
			})}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/chat/completions", strings.NewReader(`{"model":"deepseek-flash"}`))
			require.NoError(t, err)
			response, err := Do(client, req, "openai_chat")
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, f.Body, string(body))
			require.NoError(t, response.Body.Close())
			require.Equal(t, "unsupported", record.Observation.Status)
			require.Equal(t, ParserRevision, record.Observation.ParserRevision)
			encoded, err := jsonx.Marshal(record)
			require.NoError(t, err)
			var payload map[string]jsonx.RawMessage
			require.NoError(t, jsonx.Unmarshal(encoded, &payload))
			require.Contains(t, payload, "contract_proof", "the installed contract needs independent proof while native billing evidence remains unchanged")
			require.NotNil(t, record.ContractProof)
			require.True(t, record.ContractProof.Complete)
			require.Equal(t, contract.DeepSeekProofParserID, record.ContractProof.Observation.ParserRevision)
			require.Equal(t, contract.DeepSeekProofUsageID, record.ContractProof.Observation.ContractRevision)
			require.Equal(t, f.ExpectedBuckets, *record.ContractProof.Observation.Buckets)
			require.True(t, ContractVerified(&record, doc))
			var restored Record
			require.NoError(t, jsonx.Unmarshal(encoded, &restored))
			require.True(t, ContractVerified(&restored, doc), "the independent proof must survive durable JSON replay")
		})
	}
}

func verifiedDeepSeekProofRecord() Record {
	d := deepSeekProofDocument()
	body := []byte(`{"usage":` + deepseekPilotUsage + `}`)
	return Record{ParameterContract: &contract.Trace{Scope: d.Scope, Revision: d.Revision(), Build: d.AdapterBuild}, Observation: Parse(body, "openai_chat"), ContractProof: &ContractProof{Observation: proofParser(d)(body), Complete: true}, Complete: true, HTTPStatus: 200}
}

func TestContractProofRejectsUnboundForgedAndUnknownMetering(t *testing.T) {
	d := deepSeekProofDocument()
	for _, tc := range []struct {
		name   string
		change func(*Record)
	}{
		{"missing", func(r *Record) { r.ContractProof = nil }},
		{"wrong_revision", func(r *Record) { r.ContractProof.Observation.ParserRevision = ParserRevision }},
		{"wrong_usage_revision", func(r *Record) { r.ContractProof.Observation.ContractRevision = ContractRevision }},
		{"wrong_buckets", func(r *Record) { r.ContractProof.Observation.Buckets = &[5]int64{99, 0, 0, 0, 1} }},
		{"wrong_semantics", func(r *Record) { r.ContractProof.Observation.Semantics = "anthropic_exclusive" }},
		{"wrong_digest", func(r *Record) { r.ContractProof.Observation.UsageDigest = strings.Repeat("0", 64) }},
		{"wrong_contract", func(r *Record) { r.ParameterContract.Revision = strings.Repeat("0", 64) }},
		{"wrong_scope", func(r *Record) { r.ParameterContract.Scope.ID++ }},
		{"wrong_build", func(r *Record) { r.ParameterContract.Build = "old-build" }},
		{"incomplete", func(r *Record) { r.ContractProof.Complete = false }},
		{"http_failure", func(r *Record) { r.HTTPStatus = 500 }},
		{"different_native_usage", func(r *Record) { r.Observation.Usage = jsonx.RawMessage(`{}`) }},
		{"unknown_dimension", func(r *Record) {
			r.Stream = true
			r.ContractProof.Observation.Usage = jsonx.RawMessage(strings.TrimSuffix(deepseekPilotUsage, "}") + `,"new_billed_dimension":1}`)
		}},
		{"changed_type", func(r *Record) {
			r.Stream = true
			r.ContractProof.Observation.Usage = jsonx.RawMessage(strings.ReplaceAll(deepseekPilotUsage, `"prompt_cache_hit_tokens":0`, `"prompt_cache_hit_tokens":"0"`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := verifiedDeepSeekProofRecord()
			tc.change(&r)
			require.False(t, ContractVerified(&r, d))
		})
	}
	r := verifiedDeepSeekProofRecord()
	r.Stream, r.Complete = true, false
	r.Observation = Observation{Protocol: "openai_chat", ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "invalid", Reason: "usage_not_object"}
	require.True(t, ContractVerified(&r, d), "a native null-chunk fault cannot discard independently completed proof")
	require.False(t, r.Complete, "contract proof cannot rewrite native completion")
	require.Equal(t, "invalid", r.Observation.Status)
}

func TestContractProofSSETruncationNeverBecomesComplete(t *testing.T) {
	d := deepSeekProofDocument()
	snapshot, err := contract.Compile(d)
	require.NoError(t, err)
	ctx := contract.WithSelection(context.Background(), contract.Selection{Snapshots: []contract.Snapshot{snapshot}, Inbound: "openai_chat"})
	var record Record
	ctx = WithRecorder(ctx, func(r Record) { record = r })
	body := "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":" + deepseekPilotUsage + "}\n\n"
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	response, err := Do(client, req, "openai_chat")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.NotNil(t, record.ContractProof)
	require.Equal(t, "verified", record.ContractProof.Observation.Status)
	require.False(t, record.ContractProof.Complete)
	require.False(t, ContractVerified(&record, d))
}

func TestContractProofCompleteSSEUsageFaultRemainsComplete(t *testing.T) {
	d := deepSeekProofDocument()
	snapshot, err := contract.Compile(d)
	require.NoError(t, err)
	for _, tc := range []struct{ name, usage string }{
		{"changed_type", strings.ReplaceAll(deepseekPilotUsage, `"prompt_tokens":8`, `"prompt_tokens":"8"`)},
		{"alias_changed_type", strings.ReplaceAll(deepseekPilotUsage, `"prompt_cache_hit_tokens":0`, `"prompt_cache_hit_tokens":"0"`)},
		{"unknown_dimension", strings.TrimSuffix(deepseekPilotUsage, "}") + `,"new_billable_dimension":1}`},
		{"oversized_usage", strings.TrimSuffix(deepseekPilotUsage, "}") + `,"padding":"` + strings.Repeat("x", MaxUsageBytes) + `"}`},
		{"oversized_frame", strings.TrimSuffix(deepseekPilotUsage, "}") + `,"padding":"` + strings.Repeat("x", MaxFrameBytes) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var record Record
			ctx := contract.WithSelection(context.Background(), contract.Selection{Snapshots: []contract.Snapshot{snapshot}, Inbound: "openai_chat"})
			ctx = WithRecorder(ctx, func(r Record) { record = r })
			body := "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":" + tc.usage + "}\n\ndata: [DONE]\n\n"
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/chat/completions", strings.NewReader(`{}`))
			require.NoError(t, err)
			response, err := Do(client, req, "openai_chat")
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.NotNil(t, record.ContractProof)
			require.True(t, record.ContractProof.Complete, "a usage fault must not hide a fully received protocol terminator")
			require.NotEqual(t, "verified", record.ContractProof.Observation.Status)
			require.False(t, ContractVerified(&record, d))
		})
	}
}
