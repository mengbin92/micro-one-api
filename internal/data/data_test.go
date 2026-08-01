package data

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"

	identityv1 "micro-one-api/api/identity/v1"
	appcache "micro-one-api/platform/cache"
)

// mockIdentityClient implements biz.IdentityClient for testing.
type mockIdentityClient struct {
	snapshot *mockAuthSnapshot
	err      error
}

type mockAuthSnapshot struct {
	UserID        int64
	TokenID       int64
	Group         string
	AllowedModels []string
	UserEnabled   bool
	TokenEnabled  bool
}

func (m *mockIdentityClient) GetAuthSnapshot(ctx context.Context, token string) (interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.snapshot, nil
}

// mockChannelClient implements biz.ChannelClient for testing.
type mockChannelClient struct {
	channel *mockChannel
	err     error
}

type mockChannel struct {
	ID      int64
	Type    int32
	Name    string
	Status  int32
	BaseURL string
	Group   string
	Models  []string
	Key     string
}

func (m *mockChannelClient) SelectChannel(ctx context.Context, group, model string, exclude bool) (interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.channel, nil
}

func (m *mockChannelClient) RecordSubscriptionAccountHealth(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (m *mockChannelClient) RecordChannelHealth(ctx context.Context, channelID int64, success bool, err string, responseTime int64) error {
	return nil
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty", "", nil},
		{"single", "gpt-4o-mini", []string{"gpt-4o-mini"}},
		{"multiple", "gpt-4o-mini,gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
		{"with spaces", "gpt-4o-mini, gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitCSV(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d items, got %d", len(tt.expected), len(result))
			}
		})
	}
}

type countingIdentityServiceClient struct {
	identityv1.IdentityServiceClient
	calls int
}

func (c *countingIdentityServiceClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	c.calls++
	return &identityv1.GetAuthSnapshotReply{UserId: 42, Group: "default"}, nil
}

func TestCachedIdentityClientUsesAuthCache(t *testing.T) {
	base := &countingIdentityServiceClient{}
	loader := appcache.NewAuthCacheLoader(base, nil, 0)
	cache, err := appcache.NewAuthCache(nil, nil, loader.Load)
	if err != nil {
		t.Fatalf("NewAuthCache: %v", err)
	}
	defer cache.Close()

	client := NewCachedIdentityClient(base, cache)
	for i := 0; i < 2; i++ {
		got, err := client.GetAuthSnapshot(context.Background(), &identityv1.GetAuthSnapshotRequest{Token: "token-1"})
		if err != nil {
			t.Fatalf("GetAuthSnapshot: %v", err)
		}
		if got.GetUserId() != 42 {
			t.Fatalf("user id = %d, want 42", got.GetUserId())
		}
	}
	if base.calls != 1 {
		t.Fatalf("base calls = %d, want 1", base.calls)
	}
}

func TestIdentityAdapterTokenQuotaBlock(t *testing.T) {
	// Use a client that returns the blocked token id.
	client := &fixedTokenIdentityClient{tokenID: 9}
	adapter := NewIdentityAdapter(client)
	adapter.BlockTokenQuota(9, time.Minute)
	if _, err := adapter.GetAuthSnapshot(context.Background(), "token", ""); err == nil {
		t.Fatal("blocked token was accepted")
	}
	adapter.ClearTokenQuotaBlock(9)
	if _, err := adapter.GetAuthSnapshot(context.Background(), "token", ""); err != nil {
		t.Fatalf("cleared token remained blocked: %v", err)
	}
}

type fixedTokenIdentityClient struct {
	identityv1.IdentityServiceClient
	tokenID int64
}

func (c *fixedTokenIdentityClient) GetAuthSnapshot(context.Context, *identityv1.GetAuthSnapshotRequest, ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	return &identityv1.GetAuthSnapshotReply{TokenId: c.tokenID, UserId: 42}, nil
}
