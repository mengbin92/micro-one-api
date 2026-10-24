package data

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"micro-one-api/domain/upstream/repair"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func contextFixture(t *testing.T) repair.AgentContext {
	t.Helper()
	var p repair.Proposal
	b, err := io.ReadAll(strings.NewReader(`{"candidate":{"scope":{"source_kind":"channel","source_id":1,"upstream_model_id":"vendor/Exact","protocol":"openai_chat"},"provider":"fixture","adapter_build":"finite-contract-runtime-2026-10-24.1","parser_revision":"native-shadow-2026-10-13.1","usage_revision":"native-static-2026-10-13.1","inbound_protocols":["openai_chat"],"rule_sets":[],"fixture_evidence_id":"independent","basis_revision":"schema-v2"},"classification":"protocol_drift","contract_probe_failed":true,"source_revision":"source-1","incident_id":"incident-1","evidence_ids":["redacted"]}`))
	require.NoError(t, err)
	require.NoError(t, jsonx.Unmarshal(b, &p))
	schema := "trusted fixed official schema"
	p.BasisDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(schema)))
	return repair.AgentContext{Template: p, OfficialSchema: schema, Usage: jsonx.RawMessage(`{"prompt_tokens":1}`), Model: "fixture-explicit-model", MaxOutputTokens: 512}
}
func TestAgentInvokesFixedAPIWithoutToolsAndRebindsOperatorFacts(t *testing.T) {
	c := contextFixture(t)
	generated := c.Template
	generated.IncidentID = "invented"
	output, _ := jsonx.Marshal(generated)
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https://api.openai.com/v1/responses", req.URL.String())
		require.Equal(t, "Bearer isolated-key", req.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, jsonx.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, false, body["store"])
		require.Empty(t, body["tools"])
		require.Equal(t, "fixture-explicit-model", body["model"])
		b, _ := jsonx.Marshal(map[string]any{"id": "response-id", "status": "completed", "usage": map[string]any{"input_tokens": 30, "output_tokens": 100}, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	g, err := NewAgent(&Data{Client: client, Key: "isolated-key"}).Generate(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, "incident-1", g.Proposal.IncidentID)
	require.Nil(t, g.Amount)
	require.Equal(t, "unknown", g.CostBasis)
	require.Equal(t, "internal_test", g.Purpose)
}
func TestAgentRejectsUnredactedEvidenceAndIncompleteOutput(t *testing.T) {
	c := contextFixture(t)
	c.Usage = jsonx.RawMessage(`{"message":"secret conversation"}`)
	require.Error(t, c.Validate())
	c = contextFixture(t)
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"response","status":"incomplete","output":[]}`))}, nil
	})}
	_, err := NewAgent(&Data{Client: client, Key: "isolated-key"}).Generate(context.Background(), c)
	require.ErrorContains(t, err, "incomplete")
}
