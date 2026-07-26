package service

import (
	"context"
	"errors"
	"fmt"
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
	UpdateChannel(ctx context.Context, channel *biz.Channel) error
}

// ChannelModelProbeService discovers model IDs from API-key upstreams that
// expose a models endpoint. Discovery is best-effort: a failed probe leaves the
// newly-created channel intact so operators can provide models manually.
type ChannelModelProbeService struct {
	uc      channelModelProbeUsecase
	factory *relayprovider.ProviderFactory
	mu      sync.Mutex
	pending map[int64]struct{}
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
		ctx, cancel := context.WithTimeout(context.Background(), channelModelProbeTimeout)
		defer cancel()
		_ = s.syncModelsForChannel(ctx, channelID)
	}()
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
	return s.uc.UpdateChannel(ctx, channel)
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
