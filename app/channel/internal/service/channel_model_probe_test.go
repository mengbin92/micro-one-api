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

func (s *channelModelProbeUsecaseStub) GetChannel(context.Context, int64) (*biz.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *s.channel
	cloned.Models = append([]string(nil), s.channel.Models...)
	return &cloned, nil
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
