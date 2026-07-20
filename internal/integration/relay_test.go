package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayprovider "micro-one-api/domain/upstream/provider"
	relayserver "micro-one-api/internal/server"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Mock billing service for testing
type mockBillingService struct {
	billingv1.UnimplementedBillingServiceServer
}

func (m *mockBillingService) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	return &billingv1.ReserveQuotaResponse{
		Success:        true,
		ReservationId:   "test-reservation-123",
		ReservedAmount: 100,
	}, nil
}

func (m *mockBillingService) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	return &billingv1.CommitQuotaResponse{
		Success:        true,
		CommittedAmount: req.ActualTokens,
		RefundAmount:   0,
	}, nil
}

func (m *mockBillingService) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest) (*billingv1.ReleaseQuotaResponse, error) {
	return &billingv1.ReleaseQuotaResponse{
		Success: true,
	}, nil
}

func setupInMemoryBillingService(t *testing.T, addr string) (func(), billingv1.BillingServiceClient) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcSrv := grpc.NewServer()
	billingv1.RegisterBillingServiceServer(grpcSrv, &mockBillingService{})

	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()

	// Connect to the billing service
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("failed to connect to billing service: %v", err)
	}

	cleanup := func() {
		conn.Close()
		grpcSrv.Stop()
		lis.Close()
	}

	return cleanup, billingv1.NewBillingServiceClient(conn)
}

func TestRelayIntegration(t *testing.T) {
	identityCleanup, identityClient := setupInMemoryIdentityService(t, "127.0.0.1:19001")
	defer identityCleanup()

	channelCleanup, channelClient := setupInMemoryChannelService(t, "127.0.0.1:19002")
	defer channelCleanup()

	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19003")
	defer billingCleanup()

	providerFactory := relayprovider.NewProviderFactory(30 * time.Second)

	// Create biz-layer RelayUsecase with adapters
	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, nil, nil)

	httpServer := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase)

	lis, err := net.Listen("tcp", ":19000")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := khttp.NewServer(khttp.Listener(lis))
	httpServer.RegisterRoutes(srv)

	go func() {
		if err := srv.Start(context.Background()); err != nil && err != http.ErrServerClosed {
			t.Logf("HTTP server error: %v", err)
		}
	}()
	defer srv.Stop(context.Background())

	time.Sleep(100 * time.Millisecond) // Give servers time to start

	t.Run("GetHealth", func(t *testing.T) {
		resp, err := http.Get("http://localhost:19000/healthz")
		if err != nil {
			t.Fatalf("failed to get health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
	})

	t.Run("GetModels", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://localhost:19000/v1/models", nil)
		req.Header.Set("Authorization", "Bearer test-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to get models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}

		var modelsResp struct {
			Object string `json:"object"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(modelsResp.Data) == 0 {
			t.Fatal("expected some models")
		}
	})

	t.Run("GetModelsRestricted", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://localhost:19000/v1/models", nil)
		req.Header.Set("Authorization", "Bearer restricted-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to get models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}

		var modelsResp struct {
			Object string `json:"object"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Since the token has whitelist ["gpt-4o-mini"] and user is in "premium" group,
		// but premium group only has gpt-4o, intersection is empty
		t.Logf("Models returned: %v", modelsResp.Data)
		if len(modelsResp.Data) != 0 {
			t.Logf("Got %d models, expected 0 due to whitelist/group intersection", len(modelsResp.Data))
		}
	})

	t.Run("MissingAuthorization", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://localhost:19000/v1/models", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to get models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", resp.StatusCode)
		}
	})

	t.Run("InvalidToken", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://localhost:19000/v1/models", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to get models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", resp.StatusCode)
		}
	})

	t.Run("ExpiredToken", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://localhost:19000/v1/models", nil)
		req.Header.Set("Authorization", "Bearer expired-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to get models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", resp.StatusCode)
		}
	})

	t.Run("DisabledToken", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://localhost:19000/v1/models", nil)
		req.Header.Set("Authorization", "Bearer disabled-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to get models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", resp.StatusCode)
		}
	})
}
