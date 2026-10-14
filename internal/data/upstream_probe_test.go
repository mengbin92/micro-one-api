package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"micro-one-api/domain/execution"
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

func TestInventoryProbeUsesBodylessGET(t *testing.T) {
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
	result, err := repo.Probe(context.Background(), execution.ProbeTarget{ID: "inventory", SourceKind: "channel", SourceID: 1, Inventory: true, Protocol: "openai_chat", MaxOutputTokens: 1}, "inventory-bodyless")
	require.NoError(t, err)
	require.Equal(t, "inventory_observed", result.Outcome)
	require.Equal(t, []string{"deepseek-flash"}, result.Inventory)
	require.Equal(t, 1, calls)
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
	result, err := repo.Probe(context.Background(), target, "internal-attempt")
	require.NoError(t, err)
	require.Equal(t, "contract_verified", result.Outcome)
	require.True(t, result.ValidContractProbe)
	require.Equal(t, 1, calls)
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
	_, err = repo.Probe(context.Background(), target, "internal-attempt")
	require.ErrorIs(t, err, biz.ErrExecutionAlreadyAdmitted)
	require.Equal(t, 1, calls, "repeated attempt must never resend upstream")
}
