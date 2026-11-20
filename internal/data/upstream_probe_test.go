package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/adaptor"
	"micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

type probeBillingFixture struct {
	mu     sync.Mutex
	events []execution.Event
}

type proofProbeChannelFixture struct {
	probeChannelFixture
	snapshot   contract.Snapshot
	reported   *evidence.Record
	capability bool
}

func (f *proofProbeChannelFixture) GetProbeSource(context.Context, string, int64) (biz.ProbeSource, error) {
	return biz.ProbeSource{Revision: "fixed-source", Channel: &biz.Channel{ID: 11, Type: provider.ChannelTypeDeepSeek, BaseURL: f.url, Key: "fixture-only"}}, nil
}
func (f *proofProbeChannelFixture) AcquireContractProbe(context.Context, contract.Scope, string, string) (contract.ProbeLease, error) {
	return contract.ProbeLease{ID: "fixed-lease", Snapshot: f.snapshot, SourceRevision: "fixed-source"}, nil
}
func (f *proofProbeChannelFixture) ReportContractProbe(_ context.Context, _ contract.Scope, _ contract.ProbeLease, _ string, raw []byte, capability bool) error {
	var record evidence.Record
	if err := jsonx.Unmarshal(raw, &record); err != nil {
		return err
	}
	f.reported, f.capability = &record, capability
	return nil
}

func TestDeepSeekCanaryKeepsNativeIntentAndPersistsIndependentProof(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]any
				require.NoError(t, jsonx.NewDecoder(request.Body).Decode(&body))
				require.Equal(t, map[string]any{"type": "disabled"}, body["thinking"])
				if stream {
					require.Equal(t, map[string]any{"include_usage": true}, body["stream_options"])
				}
				payload := `{"id":"fixed-upstream","object":"chat.completion","choices":[{"finish_reason":"stop","message":{"content":"OK"}}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":8}}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"usage\":null}\n\ndata: "+payload+"\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, payload)
				}
			}))
			defer upstream.Close()
			doc := contract.Document{Scope: contract.Scope{Kind: "channel", ID: 11, Model: "deepseek-flash", Protocol: "openai_chat"}, Provider: "deepseek", AdapterBuild: contract.BuildID, ParserRevision: contract.DeepSeekProofParserID, UsageRevision: contract.DeepSeekProofUsageID, Inbound: []string{"openai_chat"}, FixtureEvidence: "official-pilot", BasisRevision: "official-schema"}
			snapshot, err := contract.Compile(doc)
			require.NoError(t, err)
			channels := &proofProbeChannelFixture{probeChannelFixture: probeChannelFixture{url: upstream.URL}, snapshot: snapshot}
			billing := &probeBillingFixture{}
			path := filepath.Join(t.TempDir(), "proof-outbox.db")
			data, cleanup, err := NewExecutionData(path, billing)
			require.NoError(t, err)
			defer cleanup()
			factory := provider.NewProviderFactory(0)
			adaptor.SetProviderFactory(factory)
			repo := NewUpstreamProbeRepo(channels, biz.NewExecutionDelivery(NewExecutionOutboxRepo(data, path)), factory)
			result, err := repo.Probe(context.Background(), execution.ProbeTarget{ID: "deepseek-proof", SourceKind: "channel", SourceID: 11, Model: "deepseek-flash", Protocol: "openai_chat", Stream: stream, MaxOutputTokens: 16, ContractRevision: snapshot.Revision}, "deepseek-proof-attempt")
			require.NoError(t, err)
			require.Equal(t, "contract_verified", result.Outcome)
			require.True(t, channels.capability)
			require.NotNil(t, channels.reported)
			require.Equal(t, "unsupported", channels.reported.Observation.Status)
			require.True(t, evidence.ContractVerified(channels.reported, doc))
			for _, event := range billing.events {
				require.Equal(t, evidence.ParserRevision, event.Intent.ParserRevision)
				require.Equal(t, evidence.ContractRevision, event.Intent.ContractRevision)
				require.Empty(t, event.Final)
			}
			require.NotNil(t, billing.events[len(billing.events)-1].Record.ContractProof)
		})
	}
}

func TestInventoryProbeUsesBodylessGET(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Empty(t, body, "metadata GET must not carry JSON null")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"}]}`))
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, cleanup, err := NewExecutionData(path, &probeBillingFixture{})
	require.NoError(t, err)
	defer cleanup()
	repo := NewUpstreamProbeRepo(&probeChannelFixture{url: upstream.URL}, biz.NewExecutionDelivery(NewExecutionOutboxRepo(d, path)), provider.NewProviderFactory(0))
	ctx, observations := probeObservationContext(t)
	target := execution.ProbeTarget{ID: "inventory", SourceKind: "channel", SourceID: 1, Inventory: true, Protocol: "openai_chat", MaxOutputTokens: 1}
	result, err := repo.Probe(ctx, target, "inventory-bodyless")
	require.NoError(t, err)
	require.Equal(t, "inventory_observed", result.Outcome)
	require.Equal(t, []string{"deepseek-flash"}, result.Inventory)
	require.Equal(t, 1, calls)
	checkProbeObservations(t, ctx, observations, evidence.PurposeHealthProbe, "inventory-bodyless")

	target.ManualModelIDs = []string{"operator-recorded"}
	result, err = repo.Probe(ctx, target, "manual-inventory")
	require.NoError(t, err)
	require.Equal(t, "inventory_manual", result.Outcome)
	require.Equal(t, 1, calls)
	pending, err := observations.Pending(ctx, 20)
	require.NoError(t, err)
	for _, item := range pending {
		require.NotEqual(t, "manual-inventory", item.Event.Send.RequestID)
	}
}

func probeObservationContext(t *testing.T) (context.Context, biz.UsageObservationRepo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "observations.db")
	data, cleanup, err := NewObservationData(path, nil)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	repo, err := NewObservationOutboxRepo(data, path)
	require.NoError(t, err)
	return biz.WithObservationDelivery(context.Background(), biz.NewObservationDelivery(repo, nil)), repo
}

func checkProbeObservations(t *testing.T, ctx context.Context, repo biz.UsageObservationRepo, purpose, attempt string) {
	t.Helper()
	pending, err := repo.Pending(ctx, 20)
	require.NoError(t, err)
	var categories []string
	var sendID string
	for _, item := range pending {
		event := item.Event
		if event.Send.RequestID != attempt {
			continue
		}
		require.Equal(t, purpose, event.Send.Purpose)
		require.Equal(t, attempt, event.Send.RootRequestID)
		require.Zero(t, event.Send.UserID)
		require.EqualValues(t, 1, event.Send.SourceID)
		if sendID == "" {
			sendID = event.Send.SendID
		}
		require.Equal(t, sendID, event.Send.SendID)
		if event.Category != "checkpoint" {
			categories = append(categories, event.Category)
		}
	}
	require.NotEmpty(t, sendID)
	require.ElementsMatch(t, []string{"started", "terminal"}, categories)
}

func (f *probeBillingFixture) Send(_ context.Context, e execution.Event) (execution.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, digest, err := e.Encode()
	if err != nil {
		return execution.Receipt{}, err
	}
	f.events = append(f.events, e)
	return execution.Receipt{Received: true, Digest: digest, Status: "evidence_received"}, nil
}

type probeChannelFixture struct {
	url    string
	models string
}

func (f *probeChannelFixture) GetProbeSource(context.Context, string, int64) (biz.ProbeSource, error) {
	return biz.ProbeSource{Channel: &biz.Channel{ID: 1, Type: provider.ChannelTypeOllama, BaseURL: f.url, Key: "probe-only-key"}}, nil
}
func (f *probeChannelFixture) RecordProbeInventory(context.Context, string, int64, []string, string, int64, bool) error {
	return nil
}
func TestCanaryUsesDurableIntentNativeParserAndNeverUserQuota(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		require.NoError(t, jsonx.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "vendor/Exact", body["model"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"provider-id","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`))
	}))
	defer upstream.Close()
	billing := &probeBillingFixture{}
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, billing)
	require.NoError(t, err)
	defer closeDB()
	delivery := biz.NewExecutionDelivery(NewExecutionOutboxRepo(d, path))
	factory := provider.NewProviderFactory(0)
	adaptor.SetProviderFactory(factory)
	channels := &probeChannelFixture{url: upstream.URL, models: "operator-authorized"}
	repo := NewUpstreamProbeRepo(channels, delivery, factory)
	target := execution.ProbeTarget{ID: "probe", SourceKind: "channel", SourceID: 1, Model: "vendor/Exact", Protocol: "openai_chat", MaxOutputTokens: 16}
	ctx, observations := probeObservationContext(t)
	result, err := repo.Probe(ctx, target, "canary-attempt")
	require.NoError(t, err)
	require.Equal(t, "contract_verified", result.Outcome)
	require.True(t, result.ValidContractProbe)
	require.Equal(t, 1, calls)
	checkProbeObservations(t, ctx, observations, evidence.PurposeCanary, "canary-attempt")
	require.Equal(t, "operator-authorized", channels.models)
	require.Len(t, billing.events, 2)
	for _, e := range billing.events {
		require.Equal(t, "canary", e.Intent.Purpose)
		require.Empty(t, e.Intent.ReservationID)
		require.Empty(t, e.Final)
	}
	require.Equal(t, "intent", billing.events[0].Category)
	pending, err := NewExecutionOutboxRepo(d, path).Pending(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "checkpoint", pending[0].Event.Category)
	require.Equal(t, "provider-id", billing.events[1].Record.Observation.ProviderRequestID)
	_, err = repo.Probe(ctx, target, "canary-attempt")
	require.ErrorIs(t, err, biz.ErrExecutionAlreadyAdmitted)
	require.Equal(t, 1, calls, "repeated attempt must never resend upstream")

	wrongSource := target
	wrongSource.SourceID = 2
	_, err = repo.Probe(ctx, wrongSource, "wrong-source")
	require.ErrorContains(t, err, "probe source identity changed")
	require.Equal(t, 1, calls, "a mismatched source must be rejected before sending")
	require.Len(t, billing.events, 2, "a mismatched source must not create admitted execution evidence")

	target.Purpose = evidence.PurposeInternal
	result, err = repo.Probe(ctx, target, "internal-attempt")
	require.NoError(t, err)
	require.Equal(t, "internal_test_observed", result.Outcome)
	require.False(t, result.ValidContractProbe, "an internal test must not contribute to Monitor contract drift incidents")
	require.Equal(t, 2, calls)
	checkProbeObservations(t, ctx, observations, evidence.PurposeInternal, "internal-attempt")
	require.Equal(t, evidence.PurposeInternal, billing.events[len(billing.events)-1].Intent.Purpose)
}
