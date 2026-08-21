package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"micro-one-api/app/channel/internal/biz"
	relayprovider "micro-one-api/domain/upstream/provider"
)

type channelModelProbeUsecaseStub struct {
	mu      sync.Mutex
	channel *biz.Channel
	updated *biz.Channel
}

type modelProbeRepoStub struct {
	biz.ModelRepo
	nextID               int64
	models               map[string]*biz.Model
	channelMappings      []*biz.ModelChannelMapping
	subscriptionMappings []*biz.ModelSubscriptionMapping
}

func newModelProbeRepoStub() *modelProbeRepoStub {
	return &modelProbeRepoStub{nextID: 1, models: make(map[string]*biz.Model)}
}

func (r *modelProbeRepoStub) GetModelByID(_ context.Context, modelID string) (*biz.Model, error) {
	model := r.models[biz.NormalizeModelID(modelID)]
	if model == nil {
		return nil, biz.ErrModelNotFound
	}
	clone := *model
	return &clone, nil
}

func (r *modelProbeRepoStub) CreateModel(_ context.Context, model *biz.Model) error {
	key := biz.NormalizeModelID(model.ModelID)
	if _, exists := r.models[key]; exists {
		return biz.ErrModelIDExists
	}
	model.ID = r.nextID
	r.nextID++
	clone := *model
	r.models[key] = &clone
	return nil
}

func (r *modelProbeRepoStub) UpsertChannelMapping(_ context.Context, mapping *biz.ModelChannelMapping) error {
	for i, existing := range r.channelMappings {
		if existing.ChannelID == mapping.ChannelID && existing.ModelPK == mapping.ModelPK {
			clone := *mapping
			r.channelMappings[i] = &clone
			return nil
		}
	}
	clone := *mapping
	r.channelMappings = append(r.channelMappings, &clone)
	return nil
}

func (r *modelProbeRepoStub) ListChannelMappings(_ context.Context, channelID int64) ([]*biz.ModelChannelMapping, error) {
	result := make([]*biz.ModelChannelMapping, 0, len(r.channelMappings))
	for _, mapping := range r.channelMappings {
		if channelID != 0 && mapping.ChannelID != channelID {
			continue
		}
		clone := *mapping
		result = append(result, &clone)
	}
	return result, nil
}

func (r *modelProbeRepoStub) DeleteChannelMapping(_ context.Context, channelID, modelPK int64) error {
	for i, mapping := range r.channelMappings {
		if mapping.ChannelID == channelID && mapping.ModelPK == modelPK {
			r.channelMappings = append(r.channelMappings[:i], r.channelMappings[i+1:]...)
			return nil
		}
	}
	return biz.ErrMappingNotFound
}

func (r *modelProbeRepoStub) UpsertSubscriptionMapping(_ context.Context, mapping *biz.ModelSubscriptionMapping) error {
	for i, existing := range r.subscriptionMappings {
		if existing.SubscriptionAccountID == mapping.SubscriptionAccountID && existing.ModelPK == mapping.ModelPK && existing.GroupName == mapping.GroupName {
			clone := *mapping
			r.subscriptionMappings[i] = &clone
			return nil
		}
	}
	clone := *mapping
	r.subscriptionMappings = append(r.subscriptionMappings, &clone)
	return nil
}

func (r *modelProbeRepoStub) ListSubscriptionMappings(_ context.Context, accountID int64) ([]*biz.ModelSubscriptionMapping, error) {
	result := make([]*biz.ModelSubscriptionMapping, 0, len(r.subscriptionMappings))
	for _, mapping := range r.subscriptionMappings {
		if accountID != 0 && mapping.SubscriptionAccountID != accountID {
			continue
		}
		clone := *mapping
		result = append(result, &clone)
	}
	return result, nil
}

func (r *modelProbeRepoStub) DeleteSubscriptionMapping(_ context.Context, accountID, modelPK int64, groupName string) error {
	for i, mapping := range r.subscriptionMappings {
		if mapping.SubscriptionAccountID == accountID && mapping.ModelPK == modelPK && mapping.GroupName == groupName {
			r.subscriptionMappings = append(r.subscriptionMappings[:i], r.subscriptionMappings[i+1:]...)
			return nil
		}
	}
	return biz.ErrMappingNotFound
}

func (s *channelModelProbeUsecaseStub) GetChannel(context.Context, int64) (*biz.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *s.channel
	cloned.Models = append([]string(nil), s.channel.Models...)
	return &cloned, nil
}

func (s *channelModelProbeUsecaseStub) ListChannels(context.Context, int32, int32, string, string, int32, int32) ([]*biz.Channel, int64, error) {
	return []*biz.Channel{s.channel}, 1, nil
}

func (s *channelModelProbeUsecaseStub) UpdateChannel(_ context.Context, channel *biz.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *channel
	cloned.Models = append([]string(nil), channel.Models...)
	s.updated = &cloned
	return nil
}

func TestChannelModelProbeDiscoversAndPersistsExactModelIDs(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"GLM-5.2"},{"id":"z-ai/glm-5.2"},{"id":"GLM-5.2"}]}`))
	}))
	defer upstream.Close()

	uc := &channelModelProbeUsecaseStub{channel: &biz.Channel{
		ID:      42,
		Type:    relayprovider.ChannelTypeOpenAI,
		BaseURL: upstream.URL + "/v1",
		Key:     "sk-provider",
	}}
	probe := NewChannelModelProbeService(uc)

	if err := probe.syncModelsForChannel(context.Background(), 42); err != nil {
		t.Fatalf("syncModelsForChannel() error = %v", err)
	}
	if authorization != "Bearer sk-provider" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if uc.updated == nil {
		t.Fatal("expected channel update")
	}
	if got := strings.Join(uc.updated.Models, ","); got != "GLM-5.2,z-ai/glm-5.2" {
		t.Fatalf("updated models = %q", got)
	}
}

func TestDecodeUpstreamModelsSupportsGeminiShape(t *testing.T) {
	models, err := decodeUpstreamModels([]byte(`{"models":[{"name":"models/gemini-2.5-pro"},{"name":"gemini-2.5-flash"}]}`))
	if err != nil {
		t.Fatalf("decodeUpstreamModels() error = %v", err)
	}
	if got := strings.Join(models, ","); got != "gemini-2.5-pro,gemini-2.5-flash" {
		t.Fatalf("models = %q", got)
	}
}

func TestChannelModelProbeSyncsCanonicalAndPrivateDiscoveries(t *testing.T) {
	repo := newModelProbeRepoStub()
	modelUC := biz.NewModelUsecase(repo)
	requireNoError(t, modelUC.CreateModel(context.Background(), &biz.Model{
		ModelID: "glm-5.2", DisplayName: "GLM 5.2",
		Status: biz.ModelStatusEnabled, IsPublic: true,
	}))
	probe := &ChannelModelProbeService{modelUC: modelUC}

	requireNoError(t, probe.syncRegistryModels(context.Background(), &biz.Channel{ID: 6}, []string{
		"z-ai/glm-5.2",
		"vendor/new-model",
	}))
	if len(repo.channelMappings) != 2 {
		t.Fatalf("channel mappings = %d, want 2", len(repo.channelMappings))
	}
	mappings := make(map[string]int64)
	for _, mapping := range repo.channelMappings {
		mappings[mapping.UpstreamModelID] = mapping.ModelPK
	}
	canonical := repo.models["glm-5.2"]
	if mappings["z-ai/glm-5.2"] != canonical.ID {
		t.Fatalf("NVIDIA route mapped to model %d, want canonical %d", mappings["z-ai/glm-5.2"], canonical.ID)
	}
	unknown := repo.models["vendor/new-model"]
	if unknown == nil || unknown.Status != biz.ModelStatusTesting || unknown.IsPublic {
		t.Fatalf("unknown discovery = %+v, want testing and private", unknown)
	}
}

func TestChannelModelProbeSyncsStoredModelsWithoutUpstreamProbe(t *testing.T) {
	repo := newModelProbeRepoStub()
	modelUC := biz.NewModelUsecase(repo)
	requireNoError(t, modelUC.CreateModel(context.Background(), &biz.Model{
		ModelID: "glm-5.2", DisplayName: "GLM 5.2",
		Status: biz.ModelStatusEnabled, IsPublic: true,
	}))
	channelUC := &channelModelProbeUsecaseStub{channel: &biz.Channel{
		ID: 6, Models: []string{"z-ai/glm-5.2"},
	}}
	probe := NewChannelModelProbeService(channelUC)
	probe.SetModelUsecase(modelUC)

	requireNoError(t, probe.syncStoredModelsForChannel(context.Background(), 6))
	if len(repo.channelMappings) != 1 {
		t.Fatalf("stored channel mappings = %d, want 1", len(repo.channelMappings))
	}
	if got := repo.channelMappings[0].UpstreamModelID; got != "z-ai/glm-5.2" {
		t.Fatalf("upstream model id = %q", got)
	}
}

func TestChannelModelProbeRemovesMappingsOutsideConfiguredModels(t *testing.T) {
	repo := newModelProbeRepoStub()
	modelUC := biz.NewModelUsecase(repo)
	for _, id := range []string{"gpt-4o", "o4-mini"} {
		requireNoError(t, modelUC.CreateModel(context.Background(), &biz.Model{ModelID: id, DisplayName: id}))
	}
	gpt := repo.models["gpt-4o"]
	o4 := repo.models["o4-mini"]
	repo.channelMappings = []*biz.ModelChannelMapping{
		{ChannelID: 6, ModelPK: gpt.ID, Enabled: true},
		{ChannelID: 6, ModelPK: o4.ID, Enabled: true},
	}
	probe := &ChannelModelProbeService{modelUC: modelUC}

	requireNoError(t, probe.syncRegistryModels(context.Background(), &biz.Channel{ID: 6}, []string{"gpt-4o"}))
	if len(repo.channelMappings) != 1 || repo.channelMappings[0].ModelPK != gpt.ID {
		t.Fatalf("mappings = %+v, want only configured gpt-4o", repo.channelMappings)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
