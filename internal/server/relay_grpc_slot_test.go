package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	relayv1 "micro-one-api/api/relay/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relayservice "micro-one-api/internal/service"
)

type grpcSlotEvent struct {
	channelID int64
	acquired  bool
}

type grpcSlotChannelClient struct {
	orchestratorFailoverChannelClient
	mu             sync.Mutex
	events         []grpcSlotEvent
	active         int
	leases         map[string]bool
	rejectSlot     bool
	healthFailures int
}

func (c *grpcSlotChannelClient) RecordChannelSlot(_ context.Context, id int64, slotID string, acquired bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, grpcSlotEvent{id, acquired})
	if acquired {
		if c.rejectSlot {
			return errors.New("channel at slot limit")
		}
		if c.leases == nil {
			c.leases = make(map[string]bool)
		}
		if !c.leases[slotID] {
			c.leases[slotID] = true
			c.active++
		}
	} else if c.leases[slotID] {
		delete(c.leases, slotID)
		c.active--
	}
	return nil
}

func (c *grpcSlotChannelClient) RecordChannelHealth(_ context.Context, _ int64, success bool, _ string, _ int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !success {
		c.healthFailures++
	}
	return nil
}

func TestGRPCChannelSlotsTrackActualAttempts(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name        string
		failFirst   bool
		attempts    int
		rejectQuota bool
		rejectSlot  bool
		wantErr     bool
		wantEvents  []grpcSlotEvent
	}{
		{name: "success", attempts: 1, wantEvents: []grpcSlotEvent{{11, true}, {11, false}}},
		{name: "upstream failure", failFirst: true, attempts: 1, wantErr: true, wantEvents: []grpcSlotEvent{{11, true}, {11, false}}},
		{name: "failover", failFirst: true, attempts: 2, wantEvents: []grpcSlotEvent{{11, true}, {11, false}, {22, true}, {22, false}}},
		{name: "quota rejection", rejectQuota: true, attempts: 1, wantErr: true},
		{name: "slot rejection", rejectSlot: true, attempts: 1, wantErr: true, wantEvents: []grpcSlotEvent{{11, true}, {11, false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channels := &grpcSlotChannelClient{rejectSlot: tc.rejectSlot}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				channels.mu.Lock()
				active := channels.active
				calls++
				callNumber := calls
				channels.mu.Unlock()
				if active != 1 {
					t.Errorf("active slots during upstream execution = %d, want 1", active)
				}
				if tc.failFirst && callNumber == 1 {
					http.Error(w, `{"error":"temporary"}`, http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"ok","model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
			}))
			defer upstream.Close()
			channels.first = &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL}
			channels.second = &relaybiz.Channel{ID: 22, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL}
			uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, channels, nil, &relaybiz.RetryPolicy{MaxAttempts: tc.attempts, RetryableStatus: map[int]bool{502: true}})
			billing := &rawBillingClient{}
			if tc.rejectQuota {
				billing.reserveMessage = "quota unavailable"
			}
			srv := relayservice.NewRelayGrpcService(rawIdentityClient{}, rawChannelClient{}, nil, billing, relayprovider.NewProviderFactory(time.Second), uc)
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer test-token"))
			_, err := srv.ChatCompletion(ctx, &relayv1.ChatCompletionRequest{Model: "gpt-4o-mini", Messages: []*relayv1.Message{{Role: "user", Content: "hello"}}})
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			channels.mu.Lock()
			defer channels.mu.Unlock()
			require.Equal(t, tc.wantEvents, channels.events)
			require.Zero(t, channels.active, "every attempt must release its slot")
			if tc.rejectQuota || tc.rejectSlot {
				require.Zero(t, calls, "quota rejection must stop before upstream execution")
			}
			if tc.rejectSlot {
				require.Equal(t, 1, billing.releases, "slot rejection must release reserved quota")
				require.Zero(t, billing.commits)
				require.Zero(t, channels.healthFailures, "local slot rejection must not mark channel unhealthy")
			}
		})
	}
}
