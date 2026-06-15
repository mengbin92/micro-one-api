package data

import (
	"context"
	"testing"
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
