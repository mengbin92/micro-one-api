package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"

	"micro-one-api/app/channel/internal/biz"
	relayprovider "micro-one-api/domain/upstream/provider"
)

const channelModelProbeTimeout = 30 * time.Second

type channelModelProbeScheduler interface {
	ProbeChannelAsync(channelID int64)
}

type channelModelProbeUsecase interface {
	GetChannel(ctx context.Context, channelID int64) (*biz.Channel, error)
	ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, channelType int32) ([]*biz.Channel, int64, error)
	UpdateChannel(ctx context.Context, channel *biz.Channel) error
}

// ChannelModelProbeService discovers model IDs from API-key upstreams that
// expose a models endpoint. Discovery is best-effort: a failed probe leaves the
// newly-created channel intact so operators can provide models manually.
type ChannelModelProbeService struct {
	uc      channelModelProbeUsecase
	modelUC *biz.ModelUsecase
	factory *relayprovider.ProviderFactory
	mu      sync.Mutex
	pending map[int64]struct{}
}

type discoveredRoute struct {
	model           *biz.Model
	upstreamModelID string
	rank            int
}

func NewChannelModelProbeService(uc channelModelProbeUsecase) *ChannelModelProbeService {
	if uc == nil {
		return nil
	}
	return &ChannelModelProbeService{
		uc:      uc,
		factory: relayprovider.NewProviderFactory(channelModelProbeTimeout),
		pending: make(map[int64]struct{}),
	}
}

func (s *ChannelModelProbeService) ProbeChannelAsync(channelID int64) {
	if s == nil || s.uc == nil || channelID <= 0 || !s.markPending(channelID) {
		return
	}
	go func() {
		defer s.unmarkPending(channelID)
		storedCtx, storedCancel := context.WithTimeout(context.Background(), channelModelProbeTimeout)
		if err := s.syncStoredModelsForChannel(storedCtx, channelID); err != nil {
			slog.Error("stored channel model synchronization failed", "channel_id", channelID, "error", err)
		}
		storedCancel()

		probeCtx, probeCancel := context.WithTimeout(context.Background(), channelModelProbeTimeout)
		defer probeCancel()
		if err := s.syncModelsForChannel(probeCtx, channelID); err != nil {
			slog.Error("channel model discovery failed", "channel_id", channelID, "error", err)
		}
	}()
}

func (s *ChannelModelProbeService) syncStoredModelsForChannel(ctx context.Context, channelID int64) error {
	if s == nil || s.uc == nil || s.modelUC == nil {
		return nil
	}
	channel, err := s.uc.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	if channel == nil || len(channel.Models) == 0 {
		return nil
	}
	return s.syncRegistryModels(ctx, channel, channel.Models)
}

// SyncExistingChannels replays discovery for channels that predate the model
// registry integration. Each probe is independently bounded and de-duplicated
// by ProbeChannelAsync, so a slow provider cannot block service startup.
func (s *ChannelModelProbeService) SyncExistingChannels(ctx context.Context) {
	if s == nil || s.uc == nil {
		return
	}
	channels, _, err := s.uc.ListChannels(ctx, 1, 1000, "", "", biz.ChannelStatusEnabled, 0)
	if err != nil {
		slog.Error("list existing channels for model discovery", "error", err)
		return
	}
	for _, channel := range channels {
		if channel != nil {
			s.ProbeChannelAsync(channel.ID)
		}
	}
}

func (s *ChannelModelProbeService) SetModelUsecase(uc *biz.ModelUsecase) {
	if s != nil {
		s.modelUC = uc
	}
}

func (s *ChannelModelProbeService) syncModelsForChannel(ctx context.Context, channelID int64) error {
	if s == nil || s.uc == nil || s.factory == nil {
		return errors.New("channel model prober is not configured")
	}
	channel, err := s.uc.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	// A manual model list is authoritative. This also makes the async create
	// probe harmless if an operator edits the channel while discovery is running.
	if len(channel.Models) > 0 {
		return nil
	}
	models, err := s.probeModels(ctx, channel)
	if err != nil {
		return err
	}
	channel.Models = models
	if err := s.uc.UpdateChannel(ctx, channel); err != nil {
		return err
	}
	return s.syncRegistryModels(ctx, channel, models)
}

func (s *ChannelModelProbeService) syncRegistryModels(ctx context.Context, channel *biz.Channel, upstreamModels []string) error {
	if s == nil || s.modelUC == nil || channel == nil {
		return nil
	}
	routes := make(map[int64]discoveredRoute)
	for _, upstreamModelID := range upstreamModels {
		model, err := findOrCreateDiscoveredModel(ctx, s.modelUC, upstreamModelID)
		if err != nil {
			return fmt.Errorf("register discovered model %q: %w", upstreamModelID, err)
		}
		rank := discoveredRouteRank(model.ModelID, upstreamModelID)
		if existing, ok := routes[model.ID]; !ok || rank > existing.rank {
			routes[model.ID] = discoveredRoute{model: model, upstreamModelID: upstreamModelID, rank: rank}
		}
	}
	for _, route := range routes {
		if err := s.modelUC.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{
			ChannelID:       channel.ID,
			ModelPK:         route.model.ID,
			Enabled:         true,
			EnabledHasValue: true,
			UpstreamModelID: route.upstreamModelID,
		}); err != nil {
			return fmt.Errorf("map discovered model %q to channel %d: %w", route.upstreamModelID, channel.ID, err)
		}
	}
	return nil
}

func discoveredRouteRank(canonicalModelID, upstreamModelID string) int {
	if strings.TrimSpace(canonicalModelID) == strings.TrimSpace(upstreamModelID) {
		return 3
	}
	if biz.ModelIDEqual(canonicalModelID, upstreamModelID) {
		return 2
	}
	return 1
}

func findOrCreateDiscoveredModel(ctx context.Context, modelUC *biz.ModelUsecase, upstreamModelID string) (*biz.Model, error) {
	model, err := modelUC.GetModelByID(ctx, upstreamModelID)
	if err == nil {
		return model, nil
	}
	if !errors.Is(err, biz.ErrModelNotFound) {
		return nil, err
	}

	// A namespaced upstream id is automatically bound only when its suffix is
	// already an explicitly managed canonical model. Unknown namespaced ids stay
	// distinct and private; the prefix is never stripped to invent a public id.
	if slash := strings.LastIndex(upstreamModelID, "/"); slash >= 0 && slash+1 < len(upstreamModelID) {
		model, err = modelUC.GetModelByID(ctx, upstreamModelID[slash+1:])
		if err == nil {
			return model, nil
		}
		if !errors.Is(err, biz.ErrModelNotFound) {
			return nil, err
		}
	}

	provider := ""
	if slash := strings.Index(upstreamModelID, "/"); slash > 0 {
		provider = upstreamModelID[:slash]
	}
	model = &biz.Model{
		ModelID:     upstreamModelID,
		DisplayName: upstreamModelID,
		Provider:    provider,
		ModelType:   "chat",
		Status:      biz.ModelStatusTesting,
		IsPublic:    false,
		Metadata:    `{"discovered":true}`,
	}
	if err := modelUC.CreateModel(ctx, model); err != nil {
		if errors.Is(err, biz.ErrModelIDExists) {
			return modelUC.GetModelByID(ctx, upstreamModelID)
		}
		return nil, err
	}
	return model, nil
}

func (s *ChannelModelProbeService) probeModels(ctx context.Context, channel *biz.Channel) ([]string, error) {
	if channel == nil {
		return nil, errors.New("channel is required")
	}
	provider, err := s.factory.CreateProviderWithConfig(channel.Type, channel.BaseURL, channel.Key, relayprovider.ProviderConfig{
		APIVersion: channel.Config.APIVersion,
	})
	if err != nil {
		return nil, err
	}
	resp, err := provider.Forward(ctx, &relayprovider.RawRequest{
		Method: http.MethodGet,
		Path:   "/models",
		Header: http.Header{"Accept": []string{"application/json"}},
	})
	if err != nil {
		return nil, err
	}
	models, err := decodeUpstreamModels(resp.Body)
	if err != nil {
		return nil, err
	}
	models = dedupeSortedStrings(models)
	if len(models) == 0 {
		return nil, errors.New("upstream returned no models")
	}
	return models, nil
}

// decodeUpstreamModels accepts the common OpenAI {data:[{id}]} shape and the
// Gemini-style {models:[{name}]} shape. Exact IDs are preserved because model
// names may be case-sensitive at the selected upstream.
func decodeUpstreamModels(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode upstream models: %w", err)
	}
	entries := append(payload.Data, payload.Models...)
	models := make([]string, 0, len(entries))
	for _, entry := range entries {
		model := strings.TrimSpace(entry.ID)
		if model == "" {
			model = strings.TrimSpace(entry.Name)
		}
		if strings.HasPrefix(model, "models/") {
			model = strings.TrimPrefix(model, "models/")
		}
		if model != "" {
			models = append(models, model)
		}
	}
	return models, nil
}

func (s *ChannelModelProbeService) markPending(channelID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.pending[channelID]; exists {
		return false
	}
	s.pending[channelID] = struct{}{}
	return true
}

func (s *ChannelModelProbeService) unmarkPending(channelID int64) {
	s.mu.Lock()
	delete(s.pending, channelID)
	s.mu.Unlock()
}
