package integration

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	configv1 "micro-one-api/api/config/v1"
	configbiz "micro-one-api/app/config/internal/biz"
	configservice "micro-one-api/app/config/internal/service"
)

// ========== Config Service Tests ==========

type testConfigRepo struct {
	entries map[string]*configbiz.ConfigEntry
}

func (r *testConfigRepo) Get(ctx context.Context, namespace, key string) (*configbiz.ConfigEntry, error) {
	k := namespace + "/" + key
	entry, ok := r.entries[k]
	if !ok {
		return nil, configbiz.ErrConfigNotFound
	}
	return entry, nil
}

func (r *testConfigRepo) List(ctx context.Context, namespace string, page, pageSize int32) ([]*configbiz.ConfigEntry, int64, error) {
	var result []*configbiz.ConfigEntry
	for _, e := range r.entries {
		if e.Namespace == namespace {
			result = append(result, e)
		}
	}
	return result, int64(len(result)), nil
}

func (r *testConfigRepo) Set(ctx context.Context, entry *configbiz.ConfigEntry) error {
	k := entry.Namespace + "/" + entry.Key
	r.entries[k] = entry
	return nil
}

func (r *testConfigRepo) Delete(ctx context.Context, namespace, key string) error {
	k := namespace + "/" + key
	delete(r.entries, k)
	return nil
}

func setupConfigService(t *testing.T, addr string) (func(), configv1.ConfigServiceClient) {
	repo := &testConfigRepo{entries: make(map[string]*configbiz.ConfigEntry)}
	uc := configbiz.NewConfigUsecase(repo, nil)
	svc := configservice.NewConfigService(uc)

	server := grpc.NewServer()
	configv1.RegisterConfigServiceServer(server, svc)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("config server error: %v", err)
		}
	}()

	cleanup := func() {
		server.Stop()
		lis.Close()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	return cleanup, configv1.NewConfigServiceClient(conn)
}

func TestConfigIntegration(t *testing.T) {
	cleanup, client := setupConfigService(t, "127.0.0.1:19010")
	defer cleanup()

	ctx := context.Background()

	t.Run("SetAndGet", func(t *testing.T) {
		_, err := client.SetConfig(ctx, &configv1.SetConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
			Value:     "1.5",
			Comment:   "test config",
		})
		if err != nil {
			t.Fatalf("SetConfig failed: %v", err)
		}

		resp, err := client.GetConfig(ctx, &configv1.GetConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
		})
		if err != nil {
			t.Fatalf("GetConfig failed: %v", err)
		}
		if resp.Value != "1.5" {
			t.Fatalf("expected value '1.5', got '%s'", resp.Value)
		}
	})

	t.Run("ListConfigs", func(t *testing.T) {
		_, err := client.SetConfig(ctx, &configv1.SetConfigRequest{
			Namespace: "test",
			Key:       "group_ratio",
			Value:     "0.8",
		})
		if err != nil {
			t.Fatalf("SetConfig failed: %v", err)
		}

		resp, err := client.ListConfigs(ctx, &configv1.ListConfigsRequest{
			Namespace: "test",
			Page:      1,
			PageSize:  10,
		})
		if err != nil {
			t.Fatalf("ListConfigs failed: %v", err)
		}
		if resp.Total < 2 {
			t.Fatalf("expected at least 2 configs, got %d", resp.Total)
		}
	})

	t.Run("DeleteConfig", func(t *testing.T) {
		_, err := client.DeleteConfig(ctx, &configv1.DeleteConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
		})
		if err != nil {
			t.Fatalf("DeleteConfig failed: %v", err)
		}

		_, err = client.GetConfig(ctx, &configv1.GetConfigRequest{
			Namespace: "test",
			Key:       "model_ratio",
		})
		if err == nil {
			t.Fatal("expected error for deleted config, got nil")
		}
	})
}
