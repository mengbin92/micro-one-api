package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	channelbiz "micro-one-api/internal/channel/biz"
	channelservice "micro-one-api/internal/channel/service"
	identitybiz "micro-one-api/internal/identity/biz"
	identityservice "micro-one-api/internal/identity/service"
	relaybiz "micro-one-api/internal/relay/biz"
	relaydata "micro-one-api/internal/relay/data"
	relayprovider "micro-one-api/internal/relay/provider"
	relayserver "micro-one-api/internal/relay/server"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// startMockUpstream starts a mock OpenAI-compatible upstream server and returns its base URL.
func startMockUpstream(t *testing.T, handler http.HandlerFunc) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for mock upstream: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", handler)
	srv := &http.Server{Handler: mux}
	go srv.Serve(lis)
	return fmt.Sprintf("http://%s", lis.Addr().String()), func() { srv.Close(); lis.Close() }
}

// setupChannelWithUpstream creates a channel service pointing at the given upstream URL.
func setupChannelWithUpstream(t *testing.T, addr, upstreamURL string) (func(), channelv1.ChannelServiceClient) {
	t.Helper()
	repo := &testChannelRepo{
		channels: map[int64]*channelbiz.Channel{
			1: {
				ID:       1,
				Type:     1, // OpenAI
				Name:     "test-channel",
				Status:   channelbiz.ChannelStatusEnabled,
				BaseURL:  upstreamURL,
				Group:    "default",
				Models:   []string{"gpt-4o-mini", "gpt-4o"},
				Priority: 10,
				Key:      "test-api-key",
			},
		},
		abilities: map[string][]channelbiz.Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
			},
			"default:gpt-4o": {
				{Group: "default", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}
	uc := channelbiz.NewChannelUsecase(repo, nil)
	svc := channelservice.NewChannelService(uc)
	grpcSrv := grpc.NewServer()
	channelv1.RegisterChannelServiceServer(grpcSrv, svc)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go grpcSrv.Serve(lis)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	return func() { conn.Close(); grpcSrv.Stop(); lis.Close() }, channelv1.NewChannelServiceClient(conn)
}

// setupIdentityAllowAll creates an identity service where the token has no model restrictions.
func setupIdentityAllowAll(t *testing.T, addr string) (func(), identityv1.IdentityServiceClient) {
	t.Helper()
	repo := &testIdentityRepo{
		tokens: map[string]*identitybiz.Token{
			"test-token": {
				ID:             1,
				UserID:         1,
				Key:            "test-token",
				Status:         identitybiz.TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    10000,
				UnlimitedQuota: false,
				Models:         []string{}, // no restriction
			},
		},
		users: map[int64]*identitybiz.User{
			1: {ID: 1, Username: "test-user", Group: "default", Status: identitybiz.UserStatusEnabled},
		},
	}
	uc := identitybiz.NewIdentityUsecase(repo)
	svc := identityservice.NewIdentityService(uc)
	grpcSrv := grpc.NewServer()
	identityv1.RegisterIdentityServiceServer(grpcSrv, svc)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go grpcSrv.Serve(lis)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	return func() { conn.Close(); grpcSrv.Stop(); lis.Close() }, identityv1.NewIdentityServiceClient(conn)
}

func TestChatCompletions_BasicFlow(t *testing.T) {
	// Start mock upstream that returns a simple response
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		var req relayprovider.ChatCompletionsRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := relayprovider.ChatCompletionsResponse{
			ID:      "mock-1",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []relayprovider.Choice{
				{Index: 0, Message: relayprovider.Message{Role: "assistant", Content: "Hello!"}, FinishReason: "stop"},
			},
			Usage: relayprovider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer upstreamCleanup()

	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19101")
	defer identityCleanup()
	channelCleanup, channelClient := setupChannelWithUpstream(t, "127.0.0.1:19102", upstreamURL)
	defer channelCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19103")
	defer billingCleanup()

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, nil, nil)
	providerFactory := relayprovider.NewProviderFactory(10 * time.Second)
	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	lis, _ := net.Listen("tcp", "127.0.0.1:19100")
	srv := khttp.NewServer(khttp.Listener(lis))
	httpServer.RegisterRoutes(srv)
	go srv.Start(context.Background())
	defer srv.Stop(context.Background())
	time.Sleep(100 * time.Millisecond)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:19100/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result relayprovider.ChatCompletionsResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Choices[0].Message.Content != "Hello!" {
		t.Fatalf("unexpected content: %s", result.Choices[0].Message.Content)
	}
	if result.Usage.TotalTokens != 15 {
		t.Fatalf("expected 15 tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestChatCompletions_ModelMapping(t *testing.T) {
	// Mock upstream that echoes the received model name
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		var req relayprovider.ChatCompletionsRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := relayprovider.ChatCompletionsResponse{
			ID: "mock-mm", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
			Choices: []relayprovider.Choice{
				{Index: 0, Message: relayprovider.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: relayprovider.Usage{TotalTokens: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer upstreamCleanup()

	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19201")
	defer identityCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19203")
	defer billingCleanup()

	// Custom channel setup that includes the mapped model name in abilities
	channelRepo := &testChannelRepo{
		channels: map[int64]*channelbiz.Channel{
			1: {
				ID: 1, Type: 1, Name: "test-channel",
				Status: channelbiz.ChannelStatusEnabled, BaseURL: upstreamURL,
				Group: "default", Models: []string{"gpt-4o", "gpt-4o-2024-08-06"},
				Priority: 10, Key: "test-api-key",
			},
		},
		abilities: map[string][]channelbiz.Ability{
			"default:gpt-4o": {
				{Group: "default", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
			},
			"default:gpt-4o-2024-08-06": {
				{Group: "default", Model: "gpt-4o-2024-08-06", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}
	channelUc := channelbiz.NewChannelUsecase(channelRepo, nil)
	channelSvc := channelservice.NewChannelService(channelUc)
	channelGrpc := grpc.NewServer()
	channelv1.RegisterChannelServiceServer(channelGrpc, channelSvc)
	channelLis, _ := net.Listen("tcp", "127.0.0.1:19202")
	go channelGrpc.Serve(channelLis)
	channelConn, _ := grpc.NewClient("127.0.0.1:19202", grpc.WithTransportCredentials(insecure.NewCredentials()))
	channelClient := channelv1.NewChannelServiceClient(channelConn)
	defer func() { channelConn.Close(); channelGrpc.Stop(); channelLis.Close() }()

	// Create model mapper: gpt-4o → gpt-4o-2024-08-06
	tmpDir := t.TempDir()
	modelsFile := filepath.Join(tmpDir, "models.yaml")
	os.WriteFile(modelsFile, []byte(`models:
  gpt-4o:
    actual_name: gpt-4o-2024-08-06
    capabilities:
      - function_call
      - streaming
`), 0644)
	modelMapper, err := relaybiz.NewModelMapper(modelsFile)
	if err != nil {
		t.Fatalf("failed to create model mapper: %v", err)
	}

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, modelMapper, nil)
	providerFactory := relayprovider.NewProviderFactory(10 * time.Second)
	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	lis, _ := net.Listen("tcp", "127.0.0.1:19200")
	srv := khttp.NewServer(khttp.Listener(lis))
	httpServer.RegisterRoutes(srv)
	go srv.Start(context.Background())
	defer srv.Stop(context.Background())
	time.Sleep(100 * time.Millisecond)

	// Request "gpt-4o" which should be mapped to "gpt-4o-2024-08-06"
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"test"}]}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:19200/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result relayprovider.ChatCompletionsResponse
	json.NewDecoder(resp.Body).Decode(&result)
	// The upstream should receive the mapped model name
	if result.Model != "gpt-4o-2024-08-06" {
		t.Fatalf("expected mapped model gpt-4o-2024-08-06, got %s", result.Model)
	}
}

func TestChatCompletions_ModelNotAllowed(t *testing.T) {
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer upstreamCleanup()

	// Identity service with a token restricted to "gpt-4o-mini" only
	repo := &testIdentityRepo{
		tokens: map[string]*identitybiz.Token{
			"restricted-token": {
				ID: 2, UserID: 2, Key: "restricted-token",
				Status: identitybiz.TokenStatusEnabled, ExpiredAt: time.Now().Add(time.Hour).Unix(),
				RemainQuota: 10000, Models: []string{"gpt-4o-mini"},
			},
		},
		users: map[int64]*identitybiz.User{
			2: {ID: 2, Username: "restricted", Group: "default", Status: identitybiz.UserStatusEnabled},
		},
	}
	uc := identitybiz.NewIdentityUsecase(repo)
	svc := identityservice.NewIdentityService(uc)
	identityGrpc := grpc.NewServer()
	identityv1.RegisterIdentityServiceServer(identityGrpc, svc)
	identityLis, _ := net.Listen("tcp", "127.0.0.1:19301")
	go identityGrpc.Serve(identityLis)
	identityConn, _ := grpc.NewClient("127.0.0.1:19301", grpc.WithTransportCredentials(insecure.NewCredentials()))
	identityClient := identityv1.NewIdentityServiceClient(identityConn)
	defer identityConn.Close(); defer identityGrpc.Stop(); defer identityLis.Close()

	channelCleanup, channelClient := setupChannelWithUpstream(t, "127.0.0.1:19302", upstreamURL)
	defer channelCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19303")
	defer billingCleanup()

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, nil, nil)
	providerFactory := relayprovider.NewProviderFactory(10 * time.Second)
	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	lis, _ := net.Listen("tcp", "127.0.0.1:19300")
	srv := khttp.NewServer(khttp.Listener(lis))
	httpServer.RegisterRoutes(srv)
	go srv.Start(context.Background())
	defer srv.Stop(context.Background())
	time.Sleep(100 * time.Millisecond)

	// Request "gpt-4" which is NOT in the allowed list
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:19300/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer restricted-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestChatCompletions_RetryOnFailure(t *testing.T) {
	// Mock upstream that fails first 2 attempts with 502, then succeeds
	var attempts int32
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("bad gateway"))
			return
		}
		var req relayprovider.ChatCompletionsRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := relayprovider.ChatCompletionsResponse{
			ID: "mock-retry", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
			Choices: []relayprovider.Choice{
				{Index: 0, Message: relayprovider.Message{Role: "assistant", Content: "recovered"}, FinishReason: "stop"},
			},
			Usage: relayprovider.Usage{TotalTokens: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer upstreamCleanup()

	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19401")
	defer identityCleanup()
	channelCleanup, channelClient := setupChannelWithUpstream(t, "127.0.0.1:19402", upstreamURL)
	defer channelCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19403")
	defer billingCleanup()

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	retryPolicy := &relaybiz.RetryPolicy{
		MaxAttempts:     4,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		Multiplier:      1.0,
		RetryableStatus: map[int]bool{502: true},
	}
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, nil, retryPolicy)
	providerFactory := relayprovider.NewProviderFactory(10 * time.Second)
	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	lis, _ := net.Listen("tcp", "127.0.0.1:19400")
	srv := khttp.NewServer(khttp.Listener(lis))
	httpServer.RegisterRoutes(srv)
	go srv.Start(context.Background())
	defer srv.Stop(context.Background())
	time.Sleep(100 * time.Millisecond)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"retry test"}]}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:19400/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retries, got %d", resp.StatusCode)
	}

	var result relayprovider.ChatCompletionsResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Choices[0].Message.Content != "recovered" {
		t.Fatalf("expected 'recovered', got %s", result.Choices[0].Message.Content)
	}

	totalAttempts := atomic.LoadInt32(&attempts)
	if totalAttempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", totalAttempts)
	}
}

func TestChatCompletions_MissingBody(t *testing.T) {
	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19501")
	defer identityCleanup()
	channelCleanup, channelClient := setupInMemoryChannelService(t, "127.0.0.1:19502")
	defer channelCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19503")
	defer billingCleanup()

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, nil, nil)
	providerFactory := relayprovider.NewProviderFactory(10 * time.Second)
	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	lis, _ := net.Listen("tcp", "127.0.0.1:19500")
	srv := khttp.NewServer(khttp.Listener(lis))
	httpServer.RegisterRoutes(srv)
	go srv.Start(context.Background())
	defer srv.Stop(context.Background())
	time.Sleep(100 * time.Millisecond)

	t.Run("MissingModel", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":"test"}]}`
		req, _ := http.NewRequest("POST", "http://127.0.0.1:19500/v1/chat/completions", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "http://127.0.0.1:19500/v1/chat/completions", bytes.NewBufferString("not json"))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("WrongMethod", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:19500/v1/chat/completions", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		resp, _ := http.DefaultClient.Do(req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", resp.StatusCode)
		}
	})
}
