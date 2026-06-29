package cache

import (
	"context"
	"testing"
)

// These tests cover the loader's guard paths (no client, empty token, invalid
// key) and the pure-function key splitter. The happy path requires a real
// gRPC client stub; it is covered by integration tests with a live service.

func TestAuthCacheLoader_NoClient(t *testing.T) {
	l := NewAuthCacheLoader(nil, nil, 0)
	_, err := l.Load(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error with no client, got nil")
	}
}

func TestAuthCacheLoader_EmptyToken(t *testing.T) {
	// Even with a client, an empty token must be rejected before any RPC.
	l := NewAuthCacheLoader(nil, nil, 0)
	_, err := l.Load(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func TestChannelCacheLoader_NoClient(t *testing.T) {
	l := NewChannelCacheLoader(nil, nil, 0)
	_, err := l.Load(context.Background(), "g:m")
	if err == nil {
		t.Fatal("expected error with no client, got nil")
	}
}

func TestChannelCacheLoader_InvalidKey(t *testing.T) {
	l := NewChannelCacheLoader(nil, nil, 0)
	// No colon separator → invalid key.
	_, err := l.Load(context.Background(), "no-separator")
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
}

func TestSplitChannelKey(t *testing.T) {
	cases := []struct {
		in           string
		group, model string
		ok           bool
	}{
		{"default:gpt-4", "default", "gpt-4", true},
		{"default:gpt-4:o3", "default", "gpt-4:o3", true}, // model may contain colon
		{"noseparator", "", "", false},
		{"", "", "", false},
		{":onlymodel", "", "onlymodel", true},
	}
	for _, c := range cases {
		g, m, ok := splitChannelKey(c.in)
		if g != c.group || m != c.model || ok != c.ok {
			t.Errorf("splitChannelKey(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, g, m, ok, c.group, c.model, c.ok)
		}
	}
}
