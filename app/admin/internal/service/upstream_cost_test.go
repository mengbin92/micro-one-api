package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	adminbiz "micro-one-api/app/admin/internal/biz"
)

func TestUpstreamCostKey(t *testing.T) {
	cases := []struct {
		name    string
		entry   UpstreamCostEntry
		want    string
		wantErr bool
	}{
		{"channel", UpstreamCostEntry{SourceKind: "channel", SourceID: 5, UpstreamModelID: "z-ai/glm-5.2"}, "channel:5:z-ai/glm-5.2", false},
		{"subscription", UpstreamCostEntry{SourceKind: "subscription", SourceID: 7, UpstreamModelID: "claude-sonnet-4-5"}, "subscription:7:claude-sonnet-4-5", false},
		{"bare model", UpstreamCostEntry{SourceKind: "model", PublicModelID: "  GLM-5.2  "}, "glm-5.2", false},
		{"empty kind = model default", UpstreamCostEntry{PublicModelID: "gpt-4o"}, "gpt-4o", false},
		{"channel missing id", UpstreamCostEntry{SourceKind: "channel", UpstreamModelID: "x"}, "", true},
		{"channel missing upstream", UpstreamCostEntry{SourceKind: "channel", SourceID: 5}, "", true},
		{"unknown kind", UpstreamCostEntry{SourceKind: "cdn", SourceID: 5, UpstreamModelID: "x"}, "", true},
		{"bare missing model", UpstreamCostEntry{SourceKind: "model"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := upstreamCostKey(tc.entry)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseLegacyUpstreamKey(t *testing.T) {
	cases := []struct {
		key       string
		channelID int64
		model     string
		ok        bool
	}{
		{"5:glm-5.2", 5, "glm-5.2", true},
		{"12:gpt-4o", 12, "gpt-4o", true},
		{"channel:5:glm-5.2", 0, "", false}, // canonical, not legacy
		{"subscription:7:x", 0, "", false},  // canonical
		{"glm-5.2", 0, "", false},           // bare model
		{"abc:def", 0, "", false},           // non-numeric channel
		{"0:model", 0, "", false},           // zero id
	}
	for _, tc := range cases {
		chID, model, ok := parseLegacyUpstreamKey(tc.key)
		assert.Equal(t, tc.ok, ok, tc.key)
		if ok {
			assert.Equal(t, tc.channelID, chID, tc.key)
			assert.Equal(t, tc.model, model, tc.key)
		}
	}
}

func TestParseCanonicalUpstreamKey(t *testing.T) {
	cases := []struct {
		key        string
		kind       string
		sourceID   int64
		upstreamID string
	}{
		{"channel:5:z-ai/glm-5.2", "channel", 5, "z-ai/glm-5.2"},
		{"subscription:7:claude-sonnet-4-5", "subscription", 7, "claude-sonnet-4-5"},
		{"channel:abc:x", "", 0, ""}, // non-numeric id
		{"cdn:5:x", "", 0, ""},       // unknown kind
		{"5:glm-5.2", "", 0, ""},     // legacy, not canonical
		{"glm-5.2", "", 0, ""},       // bare
	}
	for _, tc := range cases {
		kind, sourceID, upstreamID := parseCanonicalUpstreamKey(tc.key)
		assert.Equal(t, tc.kind, kind, tc.key)
		assert.Equal(t, tc.sourceID, sourceID, tc.key)
		assert.Equal(t, tc.upstreamID, upstreamID, tc.key)
	}
}

func TestParseUpstreamCostEntries_SplitsCanonicalAndLegacy(t *testing.T) {
	raw, err := json.Marshal(map[string]map[string]any{
		"channel:5:z-ai/glm-5.2":  {"input_price": 1.0, "output_price": 2.0},
		"subscription:7:claude-3": {"input_price": 0.5, "output_price": 1.0},
		"5:gpt-4o":                {"input_price": 3.0, "output_price": 6.0}, // legacy
		"gpt-3.5":                 {"input_price": 0.1, "output_price": 0.2}, // bare default
	})
	require.NoError(t, err)

	canonical, legacy := parseUpstreamCostEntries(string(raw))
	// canonical = channel + subscription + bare = 3
	assert.Len(t, canonical, 3)
	// legacy = "5:gpt-4o"
	require.Len(t, legacy, 1)
	assert.Equal(t, "5:gpt-4o", legacy[0].Key)
	assert.Equal(t, int64(5), legacy[0].SourceID)
	assert.Equal(t, "gpt-4o", legacy[0].PublicModelID)

	// Verify the canonical entries are classified.
	kinds := map[string]string{}
	for _, e := range canonical {
		kinds[e.Key] = e.SourceKind
	}
	assert.Equal(t, "channel", kinds["channel:5:z-ai/glm-5.2"])
	assert.Equal(t, "subscription", kinds["subscription:7:claude-3"])
	assert.Equal(t, "model", kinds["gpt-3.5"])
}

func TestParseUpstreamCostEntries_EmptyAndCorrupt(t *testing.T) {
	canonical, legacy := parseUpstreamCostEntries("")
	assert.Nil(t, canonical)
	assert.Nil(t, legacy)

	// Corrupt JSON → nil, nil (best-effort, no panic).
	canonical, legacy = parseUpstreamCostEntries("{not json")
	assert.Nil(t, canonical)
	assert.Nil(t, legacy)
}

func TestDecodeUpstreamCostMap_TypedModelPrice(t *testing.T) {
	// The stored value may be map[string]ModelPrice (typed) or a generic map.
	// decodeUpstreamCostMap must handle both forms without error.
	raw := `{"channel:5:glm-5.2":{"input_price":1.5,"output_price":3.0}}`
	m, err := decodeUpstreamCostMap(raw)
	require.NoError(t, err)
	require.Contains(t, m, "channel:5:glm-5.2")
	assert.Equal(t, 1.5, m["channel:5:glm-5.2"]["input_price"])
}

// ── migration plan unit tests (pure parsing, no storage) ───────────────────

func TestParseUpstreamCostEntries_LegacyDetection(t *testing.T) {
	// A legacy "<channel_id>:<model>" key must be classified as legacy
	// (SourceKind="channel", PublicModelID set) so the migration tool can find
	// it, while canonical keys and bare models are NOT flagged as legacy.
	raw := `{
		"5:glm-5.2": {"input_price": 1},
		"channel:5:z-ai/glm-5.2": {"input_price": 2},
		"gpt-4o": {"input_price": 3}
	}`
	canonical, legacy := parseUpstreamCostEntries(raw)
	require.Len(t, legacy, 1, "only the 5:glm-5.2 key is legacy")
	assert.Equal(t, "5:glm-5.2", legacy[0].Key)
	assert.Equal(t, int64(5), legacy[0].SourceID)
	assert.Equal(t, "glm-5.2", legacy[0].PublicModelID)
	assert.Len(t, canonical, 2, "channel:5:... and gpt-4o are canonical/bare")
}

func TestUpstreamCostKey_CanonicalOnlyForWrite(t *testing.T) {
	// SetUpstreamCost must never accept a legacy "<channel_id>:<model>" key —
	// new writes always use the canonical form so the migration tool has less
	// to do over time.
	_, err := upstreamCostKey(UpstreamCostEntry{SourceKind: "channel", SourceID: 5, UpstreamModelID: "glm-5.2"})
	require.NoError(t, err)
	// A legacy-style entry (no upstream_model_id, only public model) is
	// rejected for source_kind=channel.
	_, err = upstreamCostKey(UpstreamCostEntry{SourceKind: "channel", SourceID: 5, PublicModelID: "glm-5.2"})
	assert.Error(t, err, "channel entry without upstream_model_id must be rejected")
}

func TestUpstreamCostValuePreservesAndUpdatesOptionalPrices(t *testing.T) {
	cacheRead := 2.52e-9
	existing := map[string]any{
		"input_price":             1.0,
		"cache_read_price":        9.9e-9,
		"cache_creation_5m_price": 1.1e-7,
	}
	got := upstreamCostValue(UpstreamCostEntry{
		InputPrice:     1.26e-7,
		OutputPrice:    2.52e-7,
		CacheReadPrice: &cacheRead,
	}, existing)

	assert.Equal(t, 1.26e-7, got["input_price"])
	assert.Equal(t, 2.52e-7, got["output_price"])
	assert.Equal(t, 2.52e-9, got["cache_read_price"])
	assert.Equal(t, 1.1e-7, got["cache_creation_5m_price"], "unset optional prices must be preserved")
}

func TestSetUpstreamCostValidatesPrices(t *testing.T) {
	negative := -1.0
	valid := UpstreamCostEntry{SourceKind: "model", PublicModelID: "gpt-5.5"}
	for name, mutate := range map[string]func(*UpstreamCostEntry){
		"input_price":       func(e *UpstreamCostEntry) { e.InputPrice = -1 },
		"output_price":      func(e *UpstreamCostEntry) { e.OutputPrice = -1 },
		"cache_read_price":  func(e *UpstreamCostEntry) { e.CacheReadPrice = &negative },
		"cache_creation_5m": func(e *UpstreamCostEntry) { e.CacheCreation5mPrice = &negative },
		"cache_creation_1h": func(e *UpstreamCostEntry) { e.CacheCreation1hPrice = &negative },
	} {
		entry := valid
		mutate(&entry)
		err := validateUpstreamCostPrices(entry)
		require.Error(t, err, name)
	}
	require.NoError(t, validateUpstreamCostPrices(valid))
}

func TestUpstreamCostValueClearsOptionalPricesExplicitly(t *testing.T) {
	existing := map[string]any{
		"input_price":             1.0,
		"cache_read_price":        9.9e-9,
		"cache_creation_5m_price": 1.1e-7,
		"cache_creation_1h_price": 2.2e-7,
	}
	got := upstreamCostValue(UpstreamCostEntry{
		InputPrice:              1.26e-7,
		OutputPrice:             2.52e-7,
		CacheReadPriceSet:       true,
		CacheCreation5mPriceSet: true,
	}, existing)

	assert.Equal(t, 1.26e-7, got["input_price"])
	assert.Equal(t, 2.52e-7, got["output_price"])
	assert.NotContains(t, got, "cache_read_price", "explicit clear must remove the stored price")
	assert.NotContains(t, got, "cache_creation_5m_price", "explicit clear must remove the stored price")
	assert.Equal(t, 2.2e-7, got["cache_creation_1h_price"], "unset optional price must be preserved without *_set")
}

func TestSetUpstreamCostClearsCacheReadPrice(t *testing.T) {
	store := &fakeSystemOptionsRepo{values: map[string]string{
		"UpstreamModelPrice": `{"channel:1:gpt":{"input_price":1e-6,"output_price":2e-6,"cache_read_price":3e-9}}`,
	}}
	svc := &AdminService{systemOptsUc: adminbiz.NewSystemOptionsUsecase(store)}

	err := svc.SetUpstreamCost(context.Background(), UpstreamCostEntry{
		SourceKind: "channel", SourceID: 1, UpstreamModelID: "gpt",
		InputPrice: 1e-6, OutputPrice: 2e-6,
		CacheReadPriceSet: true,
	})
	require.NoError(t, err)

	var stored map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(store.values["UpstreamModelPrice"]), &stored))
	require.Contains(t, stored, "channel:1:gpt")
	assert.NotContains(t, stored["channel:1:gpt"], "cache_read_price")
}

type fakeUpstreamResolverChannelClient struct {
	channelv1.ChannelServiceClient
}

func (c *fakeUpstreamResolverChannelClient) ListModels(ctx context.Context, req *channelv1.ListModelsRequest, opts ...grpc.CallOption) (*channelv1.ListModelsResponse, error) {
	return &channelv1.ListModelsResponse{Models: []*channelv1.ModelSummary{{Id: 7, ModelId: "gpt"}}}, nil
}

func (c *fakeUpstreamResolverChannelClient) ListChannels(ctx context.Context, req *channelv1.ListChannelsRequest, opts ...grpc.CallOption) (*channelv1.ListChannelsResponse, error) {
	return &channelv1.ListChannelsResponse{Channels: []*commonv1.ChannelSummary{{Id: 1, Name: "main"}}}, nil
}

func (c *fakeUpstreamResolverChannelClient) ListChannelModelMappings(ctx context.Context, req *channelv1.ListChannelModelMappingsRequest, opts ...grpc.CallOption) (*channelv1.ListChannelModelMappingsResponse, error) {
	return &channelv1.ListChannelModelMappingsResponse{Mappings: []*channelv1.ModelChannelMapping{{ModelPk: 7, UpstreamModelId: "gpt"}}}, nil
}

func (c *fakeUpstreamResolverChannelClient) ListSubscriptionAccounts(ctx context.Context, req *channelv1.ListSubscriptionAccountsRequest, opts ...grpc.CallOption) (*channelv1.ListSubscriptionAccountsResponse, error) {
	return &channelv1.ListSubscriptionAccountsResponse{}, nil
}

func (c *fakeUpstreamResolverChannelClient) ListSubscriptionModelMappings(ctx context.Context, req *channelv1.ListSubscriptionModelMappingsRequest, opts ...grpc.CallOption) (*channelv1.ListSubscriptionModelMappingsResponse, error) {
	return &channelv1.ListSubscriptionModelMappingsResponse{}, nil
}

func TestMigrateUpstreamCostKeysPreservesExistingTarget(t *testing.T) {
	store := &fakeSystemOptionsRepo{values: map[string]string{
		"UpstreamModelPrice": `{
			"1:gpt": {"input_price":1,"output_price":2},
			"channel:1:gpt": {"input_price":9,"output_price":9}
		}`,
	}}
	svc := &AdminService{
		systemOptsUc:  adminbiz.NewSystemOptionsUsecase(store),
		channelClient: &fakeUpstreamResolverChannelClient{},
	}
	plan, err := svc.MigrateUpstreamCostKeys(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, plan.ToRewrite, 1)
	require.Len(t, plan.Skipped, 1)
	assert.Equal(t, 0, plan.Executed, "an existing target key must prevent the rewrite")

	var stored map[string]map[string]float64
	require.NoError(t, json.Unmarshal([]byte(store.values["UpstreamModelPrice"]), &stored))
	require.Contains(t, stored, "1:gpt")
	require.Contains(t, stored, "channel:1:gpt")
	assert.Equal(t, 9.0, stored["channel:1:gpt"]["input_price"], "existing canonical price must not be overwritten")
}

type fakeSystemOptionsRepo struct {
	values map[string]string
}

func (r *fakeSystemOptionsRepo) Get(ctx context.Context, key string) (string, error) {
	return r.values[key], nil
}

func (r *fakeSystemOptionsRepo) Set(ctx context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
