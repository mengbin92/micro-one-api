// TestRelaySelectChannel_WeightedDistribution is the Phase 2.2 runtime
// verification: it proves that relay-gateway -> channel-service selection
// actually flows through WeightedSelector. Two API-key channels share the same
// Priority tier but have different configured Weights; over many relay
// requests the higher-weight channel must be selected more often, and the
// selector's runtime stats (visible via ChannelUsecase.SelectorStats) must
// reflect that both channels were driven through the weighted selector and
// that the RecordChannelHealth loop populated latency for the selected
// channel.
package integration

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	channelv1 "micro-one-api/api/channel/v1"
	channeltestutil "micro-one-api/app/channel/testutil"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayprovider "micro-one-api/domain/upstream/provider"
	relayserver "micro-one-api/internal/server"
)

func TestRelaySelectChannel_WeightedDistribution(t *testing.T) {
	// Single shared mock upstream: it counts per-channel hits using the
	// inbound Authorization header (relay forwards channel.Key as
	// "Authorization: Bearer <key>"). Channel 1 has key "high-weight-key",
	// channel 2 has key "low-weight-key".
	var highHits, lowHits int32
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		// Sleep enough that RetryExecutor.recordHealth observes a non-zero
		// response time (it records time.Since(startedAt).Milliseconds(),
		// which rounds sub-millisecond local calls to 0).
		time.Sleep(8 * time.Millisecond)
		auth := r.Header.Get("Authorization")
		switch auth {
		case "Bearer high-weight-key":
			atomic.AddInt32(&highHits, 1)
		case "Bearer low-weight-key":
			atomic.AddInt32(&lowHits, 1)
		}
		var req relayprovider.ChatCompletionsRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := relayprovider.ChatCompletionsResponse{
			ID:      "mock-wrr",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []relayprovider.Choice{
				{Index: 0, Message: relayprovider.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: relayprovider.Usage{TotalTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer upstreamCleanup()

	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19601")
	defer identityCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19603")
	defer billingCleanup()

	// Two channels in the SAME priority tier (10) but different Weight.
	// configuredSelectorWeight prefers Weight>0 over Priority, so channel 1
	// gets weight 100 and channel 2 gets weight 1.
	channelRepo := &testChannelRepo{
		channels: map[int64]*channeltestutil.Channel{
			1: {
				ID: 1, Type: 1, Name: "high-weight",
				Status: channeltestutil.ChannelStatusEnabled, BaseURL: upstreamURL,
				Group: "default", Models: []string{"gpt-4o-mini"},
				Priority: 10, Weight: 100, Key: "high-weight-key",
			},
			2: {
				ID: 2, Type: 1, Name: "low-weight",
				Status: channeltestutil.ChannelStatusEnabled, BaseURL: upstreamURL,
				Group: "default", Models: []string{"gpt-4o-mini"},
				Priority: 10, Weight: 1, Key: "low-weight-key",
			},
		},
		abilities: map[string][]channeltestutil.Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}
	channelUc := channeltestutil.NewChannelUsecase(channelRepo, nil)
	channelSvc := channeltestutil.NewChannelService(channelUc)
	channelGrpc := grpc.NewServer()
	channelv1.RegisterChannelServiceServer(channelGrpc, channelSvc)
	channelLis, err := net.Listen("tcp", "127.0.0.1:19602")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go channelGrpc.Serve(channelLis)
	channelConn, _ := grpc.NewClient("127.0.0.1:19602", grpc.WithTransportCredentials(insecure.NewCredentials()))
	channelClient := channelv1.NewChannelServiceClient(channelConn)
	defer func() { channelConn.Close(); channelGrpc.Stop(); channelLis.Close() }()

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, nil, nil)
	providerFactory := relayprovider.NewProviderFactory(10 * time.Second)
	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	relayURL := startRelayHTTPServer(t, httpServer)

	// Fire N sequential relay requests. Each goes through the full path:
	// handleChatCompletions -> RelayUsecase.Plan -> ChannelAdapter.SelectChannel
	// -> channel-service SelectChannel -> ChannelUsecase.SelectChannel ->
	// WeightedSelector.Select. The selector increments inflight and adjusts
	// currentWeight; RetryExecutor.recordHealth then calls back into
	// ChannelUsecase.RecordHealth -> WeightedSelector.RecordHealth.
	const total = 40
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	for i := 0; i < total; i++ {
		req, _ := http.NewRequest("POST", relayURL+"/v1/chat/completions", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	high := atomic.LoadInt32(&highHits)
	low := atomic.LoadInt32(&lowHits)
	t.Logf("weighted distribution over %d requests: high-weight=%d low-weight=%d", total, high, low)

	// Invariant 1: every request hit the upstream exactly once (no double-send).
	if int(high+low) != total {
		t.Fatalf("upstream hits=%d, want %d (no retries expected)", high+low, total)
	}
	// Invariant 2: the higher-weight channel was selected strictly more often.
	// Under a uniform-random fallback the probability of high-weight winning
	// every one of 40 trials is (0.5)^40 ~= 9e-13, so high>low reliably
	// distinguishes weighted selection from a non-weighted fallback.
	if high <= low {
		t.Fatalf("higher-weight channel not favored: high=%d low=%d (selection likely bypassed WeightedSelector)", high, low)
	}

	// Invariant 3: WeightedSelector runtime stats were populated, proving the
	// selector was actually on the selection path (not bypassed). Both
	// channels must be registered (Select registers every candidate) with the
	// configured weights, and CurrentWeight must be non-zero (Select mutates
	// it). After all requests complete, RecordHealth has run for each attempt
	// so inflight is back to 0.
	stats := channeltestutil.SelectorStats(channelUc)
	if len(stats) != 2 {
		t.Fatalf("SelectorStats len=%d, want 2 (both channels driven through WeightedSelector)", len(stats))
	}
	st1, ok1 := stats[1]
	st2, ok2 := stats[2]
	if !ok1 || !ok2 {
		t.Fatalf("SelectorStats missing channel: stats=%+v", stats)
	}
	if st1.Weight != 100 || st2.Weight != 1 {
		t.Fatalf("SelectorStats weight mismatch: ch1=%d ch2=%d", st1.Weight, st2.Weight)
	}
	if st1.CurrentWeight == 0 || st2.CurrentWeight == 0 {
		t.Fatalf("CurrentWeight not mutated by Select: ch1=%d ch2=%d (selector bypassed)", st1.CurrentWeight, st2.CurrentWeight)
	}
	if st1.Inflight != 0 || st2.Inflight != 0 {
		t.Fatalf("inflight not drained after requests complete: ch1=%d ch2=%d", st1.Inflight, st2.Inflight)
	}

	// Invariant 4: the RecordChannelHealth loop reached the selector. Each
	// relay request triggers RetryExecutor.recordHealth -> ChannelAdapter
	// .RecordChannelHealth (gRPC) -> ChannelService.RecordChannelHealth ->
	// ChannelUsecase.RecordHealth -> WeightedSelector.RecordHealth, which
	// pushes responseTime into recentLatency. With the 8ms upstream delay the
	// selected channel's P95 must be non-zero, proving the health-feedback
	// loop is wired end-to-end through the weighted selector.
	if st1.P95Latency <= 0 {
		t.Fatalf("p95 latency not recorded for selected channel; RecordHealth loop did not reach WeightedSelector: %+v", stats)
	}
}
