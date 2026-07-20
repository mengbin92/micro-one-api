package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	khttp "github.com/go-kratos/kratos/v3/transport/http"

	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayprovider "micro-one-api/domain/upstream/provider"
)

// mockUpstreamWSServer is a Responses WebSocket upstream that reads the first
// response.create frame, replays a fixed event sequence (created -> completed
// with usage), and closes. It records the model it observed.
func mockUpstreamWSServer(t *testing.T, responseID string) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Errorf("upstream accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Read the first response.create frame.
		_, first, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("upstream read first: %v", err)
			return
		}
		t.Logf("upstream received first frame: %s", string(first))

		// Emit response.created.
		created := `{"type":"response.created","response":{"id":"` + responseID + `","status":"in_progress"}}`
		if err := conn.Write(ctx, coderws.MessageText, []byte(created)); err != nil {
			t.Errorf("upstream write created: %v", err)
			return
		}

		// Emit response.completed with usage.
		completed := `{"type":"response.completed","response":{"id":"` + responseID + `","status":"completed","usage":{"input_tokens":12,"output_tokens":8,"input_tokens_details":{"cached_tokens":3}}}}`
		if err := conn.Write(ctx, coderws.MessageText, []byte(completed)); err != nil {
			t.Errorf("upstream write completed: %v", err)
			return
		}
	}))
	return upstream
}

func wsURL(httpURL string) string {
	parsed, _ := url.Parse(httpURL)
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	default:
		parsed.Scheme = "ws"
	}
	return parsed.String()
}

// TestResponsesWebSocketE2E exercises the full Accept -> Plan -> Dial -> Relay
// -> Billing path against a mock upstream Responses WS server. It verifies the
// client receives the upstream events and the billing layer commits usage.
func TestResponsesWebSocketE2E(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	// Mock upstream Responses WebSocket server.
	const responseID = "resp_e2e_1"
	upstream := mockUpstreamWSServer(t, responseID)
	defer upstream.Close()

	// Build the relay HTTPServer with in-memory gRPC client stubs whose channel
	// points at the mock upstream. The WS forwarder builds the upstream URL from
	// the channel BaseURL, so we point it at the mock server's base.
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(rawIdentityClient{}),
		relaydata.NewChannelAdapter(channelClient),
		nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(
		rawIdentityClient{},
		channelClient,
		billingClient,
		relayprovider.NewProviderFactory(time.Second),
		relayUsecase,
		logClient,
	)
	// Short timeouts to keep the test fast.
	httpServer.SetOpenAIWSTimeouts(time.Second, 3*time.Second, 2*time.Second, 2*time.Second)

	// Register routes on a kratos http server backed by a real listener.
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)
	gateway := httptest.NewServer(srv)
	defer gateway.Close()

	// Dial the gateway's /v1/responses as a WebSocket client (Upgrade).
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDial()
	conn, _, err := coderws.Dial(dialCtx, wsURL(gateway.URL)+"/v1/responses", &coderws.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer user-token"},
		},
	})
	if err != nil {
		t.Fatalf("gateway dial: %v", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(openAIWSClientReadLimitBytes)

	// Send the first response.create frame.
	first := []byte(`{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"hi"}],"stream":true}`)
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWrite()
	if err := conn.Write(writeCtx, coderws.MessageText, first); err != nil {
		t.Fatalf("client write first: %v", err)
	}

	// Read frames until we see the terminal response.completed event.
	var sawCreated, sawCompleted bool
	readLoopCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	for !sawCompleted {
		_, payload, readErr := conn.Read(readLoopCtx)
		if readErr != nil {
			t.Fatalf("client read: %v", readErr)
		}
		msg := string(payload)
		t.Logf("client received: %s", msg)
		if strings.Contains(msg, `"response.created"`) {
			sawCreated = true
		}
		if strings.Contains(msg, `"response.completed"`) {
			sawCompleted = true
		}
	}

	if !sawCreated {
		t.Error("expected to receive response.created frame")
	}
	if !sawCompleted {
		t.Error("expected to receive response.completed frame")
	}

	// Give the post-response billing goroutine a moment to flush.
	waitFor(t, 2*time.Second, func() bool {
		return billingClient.commits >= 1
	})
	if billingClient.commits < 1 {
		t.Errorf("expected at least 1 billing commit, got %d", billingClient.commits)
	}
	if len(billingClient.commitRequests) > 0 {
		c := billingClient.commitRequests[len(billingClient.commitRequests)-1]
		if c.PromptTokens != 12 || c.CompletionTokens != 8 {
			t.Errorf("unexpected committed usage: prompt=%d completion=%d", c.PromptTokens, c.CompletionTokens)
		}
		if c.CacheReadTokens != 3 {
			t.Errorf("expected cache_read_tokens=3, got %d", c.CacheReadTokens)
		}
	}

	// Verify the response route was stored for sticky follow-up turns.
	httpServer.responsesMu.RLock()
	_, stored := httpServer.responseRoutes[responseID]
	httpServer.responsesMu.RUnlock()
	if !stored {
		t.Errorf("expected response route stored for %s", responseID)
	}
}

// waitFor polls cond until it returns true or the timeout elases.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
