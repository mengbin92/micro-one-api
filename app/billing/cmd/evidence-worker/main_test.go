package main

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"micro-one-api/app/billing/internal/biz"
	ops "micro-one-api/domain/operations/biz"
	"micro-one-api/domain/upstream/repair"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testWorker(t *testing.T) (*worker, string, string) {
	t.Helper()
	input, state := filepath.Join(t.TempDir(), "inputs"), filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.Mkdir(input, 0700))
	require.NoError(t, os.Mkdir(state, 0700))
	a, e := os.OpenRoot(input)
	require.NoError(t, e)
	b, e := os.OpenRoot(state)
	require.NoError(t, e)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return &worker{inputs: a, state: b, client: &http.Client{}}, input, state
}
func TestScheduledCostFetchArchivesScopeAndKeepsMismatchesForReview(t *testing.T) {
	w, input, state := testWorker(t)
	now := time.Now().UTC()
	start := now.Add(-48 * time.Hour).Truncate(24 * time.Hour)
	end := start.Add(24 * time.Hour)
	scope := biz.VendorScope{Provider: "openai", Account: "fixture", Start: start, End: end, Timezone: "UTC", Currency: "USD", Unit: "currency", Granularity: "account_period"}
	zero := "0"
	rows := []biz.VendorBillRow{{Amount: &zero}}
	m := biz.VendorManifest{Scope: scope, Version: "CSV-1", EvidenceID: "independent", CostBasis: "provider_reported", Mode: "tokens", Complete: true, PagesComplete: true, VendorWatermark: end, LocalWatermark: end, CoverageEvidenceID: "coverage", AbsoluteTolerance: "0", RelativeTolerance: "0", Actor: "fixture", Reason: "reviewed", Mappings: []biz.VendorSourceMapping{{SourceKind: "channel", SourceID: 1, Provider: "openai", Account: "fixture", Start: start, End: end, Revision: "v1", EvidenceID: "binding"}}}
	var csv strings.Builder
	require.NoError(t, biz.WriteVendorCSV(&csv, scope, rows))
	require.NoError(t, os.WriteFile(filepath.Join(input, "csv"), []byte(csv.String()), 0600))
	payload := costPayload{Query: biz.VendorCostQuery{Scope: scope, CredentialBindingEvidence: "bound", CSVReportID: biz.VendorReportID(m, rows), CSVTotal: "0"}, Manifest: m, CSVFile: "csv", CSVDigest: hash([]byte(csv.String())), CredentialRef: "FIXTURE"}
	bytes, _ := jsonx.Marshal(payload)
	require.NoError(t, os.WriteFile(filepath.Join(input, "payload"), bytes, 0600))
	p := ops.Plan{ID: "fixed", Kind: "cost", PayloadFile: "payload", PayloadDigest: hash(bytes)}
	t.Setenv("P3_COST_FIXTURE", "independent-cost-key")
	w.client.Transport = transport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "api.openai.com", req.URL.Host)
		require.Equal(t, "Bearer independent-cost-key", req.Header.Get("Authorization"))
		raw, _ := jsonx.Marshal(map[string]any{"data": []any{map[string]any{"start_time": start.Unix(), "end_time": end.Unix(), "results": []any{map[string]any{"amount": map[string]any{"value": 1, "currency": "usd"}}}}}, "has_more": false, "next_page": nil})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	receipt, err := w.Run(context.Background(), p, ops.Claim{Key: strings.Repeat("a", 64)})
	require.NoError(t, err)
	require.Equal(t, "attention", receipt.Status)
	require.Len(t, receipt.ArtifactID, 64)
	artifact, err := os.ReadFile(filepath.Join(state, strings.Repeat("a", 64)+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(artifact), "independent-cost-key")
	require.Contains(t, string(artifact), `"csv_matched": false`)
	p.PayloadDigest = strings.Repeat("b", 64)
	_, err = w.Run(context.Background(), p, ops.Claim{Key: strings.Repeat("c", 64)})
	require.ErrorContains(t, err, "digest")
}
func TestWorkerInputRootCannotEscapeWithSymlink(t *testing.T) {
	w, input, _ := testWorker(t)
	secret := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.WriteFile(secret, []byte("not input"), 0600))
	require.NoError(t, os.Symlink(secret, filepath.Join(input, "payload")))
	_, err := w.read("payload", "")
	require.Error(t, err)
}

func TestScheduledAgentProducesIndependentReplayAndSeparateUnsignedProposal(t *testing.T) {
	w, input, state := testWorker(t)
	raw, err := os.ReadFile("../../../../domain/upstream/repair/testdata/finite-proposal.json")
	require.NoError(t, err)
	var template repair.Proposal
	require.NoError(t, jsonx.Unmarshal(raw, &template))
	fixtureRaw, err := os.ReadFile("../../../../domain/upstream/repair/testdata/independent-fixtures.json")
	require.NoError(t, err)
	var fixtures repair.FixtureSet
	require.NoError(t, jsonx.Unmarshal(fixtureRaw, &fixtures))
	schema := "fixed official equivalent parameter schema"
	template.BasisDigest = hash([]byte(schema))
	fixtures.BasisDigest = template.BasisDigest
	fixtureRaw, _ = jsonx.Marshal(fixtures)
	require.NoError(t, os.WriteFile(filepath.Join(input, "fixtures"), fixtureRaw, 0600))
	payload := repairPayload{Context: repair.AgentContext{Template: template, OfficialSchema: schema, Usage: jsonx.RawMessage(`{"prompt_tokens":0}`), Model: "fixture-model", MaxOutputTokens: 2048}, FixturesFile: "fixtures", FixturesDigest: hash(fixtureRaw)}
	bytes, _ := jsonx.Marshal(payload)
	require.NoError(t, os.WriteFile(filepath.Join(input, "payload"), bytes, 0600))
	p := ops.Plan{ID: "incident", Kind: "repair", PayloadFile: "payload", PayloadDigest: hash(bytes)}
	t.Setenv("P3_REPAIR_AGENT_KEY", "isolated-model-key")
	generated, _ := jsonx.Marshal(template)
	w.client.Transport = transport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "api.openai.com", req.URL.Host)
		body, _ := jsonx.Marshal(map[string]any{"id": "fixture-response", "status": "completed", "usage": map[string]any{"input_tokens": 100, "output_tokens": 200}, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(generated)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"fixture-request"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	key := strings.Repeat("a", 64)
	receipt, err := w.Run(context.Background(), p, ops.Claim{Key: key})
	require.NoError(t, err)
	require.Equal(t, "succeeded", receipt.Status)
	require.Equal(t, "review_required", receipt.Reason)
	proposalBytes, err := os.ReadFile(filepath.Join(state, key+".proposal.json"))
	require.NoError(t, err)
	var proposal repair.Proposal
	require.NoError(t, jsonx.Unmarshal(proposalBytes, &proposal))
	require.Nil(t, proposal.Candidate.Repair)
	packet, err := os.ReadFile(filepath.Join(state, key+".json"))
	require.NoError(t, err)
	require.Contains(t, string(packet), `"failures": 0`)
	require.Contains(t, string(packet), `"amount": null`)
	require.NotContains(t, string(packet), "isolated-model-key")
}

func TestWeeklyArchiveIsReadOnlyAndRejectsUntrustedOwnerURL(t *testing.T) {
	w, input, state := testWorker(t)
	raw := []byte(`{"window_days":7}`)
	require.NoError(t, os.WriteFile(filepath.Join(input, "payload"), raw, 0600))
	plan := ops.Plan{Kind: "weekly", PayloadFile: "payload", PayloadDigest: hash(raw)}
	claim := ops.Claim{Key: strings.Repeat("b", 64), StartedAt: time.Now().Unix()}
	t.Setenv("P3_REVIEW_ADMIN_URL", "https://owner-fixture.invalid")
	t.Setenv("P3_REVIEW_OPERATOR_TOKEN", "read-only-operator")
	w.client.Transport = transport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/api/admin/vendor-reports", req.URL.Path)
		require.Equal(t, "Bearer read-only-operator", req.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"reports":[],"weekly_review":{"pending":0,"attention":0,"unknown_amounts":0}}`))}, nil
	})
	receipt, err := w.Run(context.Background(), plan, claim)
	require.NoError(t, err)
	require.Equal(t, "attention", receipt.Status, "no coverage is not confirmed zero expense")
	body, err := os.ReadFile(filepath.Join(state, claim.Key+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(body), "read-only-operator")
	t.Setenv("P3_REVIEW_ADMIN_URL", "http://arbitrary-owner.invalid")
	_, err = w.Run(context.Background(), plan, claim)
	require.ErrorContains(t, err, "trusted")
}
