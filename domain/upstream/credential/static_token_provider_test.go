package credential

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// staticTestLookup is an in-memory AccountLookup for the static-provider
// tests. It is intentionally distinct from fakeLookup so the static path's
// no-cache behaviour can be asserted independently.
type staticTestLookup struct {
	mu      sync.Mutex
	byID    map[int64]*AccountCredentials
	lookups int
}

func newStaticTestLookup() *staticTestLookup {
	return &staticTestLookup{byID: make(map[int64]*AccountCredentials)}
}

func (l *staticTestLookup) Seed(id int64, creds *AccountCredentials) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := *creds
	l.byID[id] = &cp
}

func (l *staticTestLookup) Lookup(_ context.Context, id int64) (*AccountCredentials, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lookups++
	c, ok := l.byID[id]
	if !ok || c == nil {
		return nil, ErrAccountNotFound
	}
	cp := *c
	return &cp, nil
}

func (l *staticTestLookup) Store(_ context.Context, id int64, creds *AccountCredentials) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := *creds
	l.byID[id] = &cp
	return nil
}

func TestStaticTokenProvider_ReturnsStoredAccessToken(t *testing.T) {
	lookup := newStaticTestLookup()
	// Static Coding Plan key: no refresh token, zero expiry (never expires).
	lookup.Seed(42, &AccountCredentials{AccessToken: "glm-coding-plan-key"})

	p := NewStaticTokenProvider(lookup)
	tok, err := p.GetAccessToken(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetAccessToken: unexpected error: %v", err)
	}
	if tok != "glm-coding-plan-key" {
		t.Fatalf("GetAccessToken = %q, want %q", tok, "glm-coding-plan-key")
	}
}

func TestStaticTokenProvider_RereadsOnEachCall_NoCache(t *testing.T) {
	// A rotated key must take effect on the next call without any
	// Invalidate: the provider does not cache.
	lookup := newStaticTestLookup()
	lookup.Seed(7, &AccountCredentials{AccessToken: "old-key"})

	p := NewStaticTokenProvider(lookup)
	if _, err := p.GetAccessToken(context.Background(), 7); err != nil {
		t.Fatalf("first GetAccessToken: %v", err)
	}

	lookup.Seed(7, &AccountCredentials{AccessToken: "rotated-key"})
	tok, err := p.GetAccessToken(context.Background(), 7)
	if err != nil {
		t.Fatalf("second GetAccessToken: %v", err)
	}
	if tok != "rotated-key" {
		t.Fatalf("GetAccessToken = %q, want rotated %q", tok, "rotated-key")
	}
	if lookup.lookups != 2 {
		t.Fatalf("expected two lookups (no caching), got %d", lookup.lookups)
	}
}

func TestStaticTokenProvider_EmptyAccessTokenSurfacesNoRefreshToken(t *testing.T) {
	// A static account with no key cannot serve requests; surface the
	// same sentinel the OAuth provider uses for "no refresh token".
	lookup := newStaticTestLookup()
	lookup.Seed(99, &AccountCredentials{})

	p := NewStaticTokenProvider(lookup)
	_, err := p.GetAccessToken(context.Background(), 99)
	if !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("expected ErrNoRefreshToken, got %v", err)
	}
}

func TestStaticTokenProvider_MissingAccount(t *testing.T) {
	p := NewStaticTokenProvider(newStaticTestLookup())
	_, err := p.GetAccessToken(context.Background(), 1234)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
}

func TestStaticTokenProvider_NotConfiguredWhenLookupNil(t *testing.T) {
	p := &StaticTokenProvider{lookup: nil}
	_, err := p.GetAccessToken(context.Background(), 1)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestStaticTokenProvider_RefreshIsNoop(t *testing.T) {
	lookup := newStaticTestLookup()
	lookup.Seed(1, &AccountCredentials{AccessToken: "key"})
	p := NewStaticTokenProvider(lookup)

	if err := p.Refresh(context.Background(), 1); err != nil {
		t.Fatalf("Refresh should be a no-op, got %v", err)
	}
	// Nothing stored, nothing changed.
	creds, err := lookup.Lookup(context.Background(), 1)
	if err != nil {
		t.Fatalf("post-Refresh Lookup: %v", err)
	}
	if creds.AccessToken != "key" {
		t.Fatalf("Refresh mutated stored token: %q", creds.AccessToken)
	}
}

func TestStaticTokenProvider_InvalidateIsNoop(t *testing.T) {
	lookup := newStaticTestLookup()
	lookup.Seed(1, &AccountCredentials{AccessToken: "key"})
	p := NewStaticTokenProvider(lookup)

	// Invalidate must not panic and must not evict the stored account.
	p.Invalidate(1)
	tok, err := p.GetAccessToken(context.Background(), 1)
	if err != nil || tok != "key" {
		t.Fatalf("after Invalidate GetAccessToken = (%q,%v), want (key,nil)", tok, err)
	}
}

// Ensure a static account with ExpiresAt zero-time (the shape the data layer
// stores for a Coding Plan key) does not confuse the provider: it must
// return the token regardless of the (absent) expiry.
func TestStaticTokenProvider_ZeroExpiryStillServed(t *testing.T) {
	lookup := newStaticTestLookup()
	lookup.Seed(5, &AccountCredentials{
		AccessToken: "minimax-key",
		ExpiresAt:   time.Time{}, // zero — semantic "never expires"
	})
	p := NewStaticTokenProvider(lookup)
	tok, err := p.GetAccessToken(context.Background(), 5)
	if err != nil || tok != "minimax-key" {
		t.Fatalf("zero-expiry static account: GetAccessToken = (%q,%v)", tok, err)
	}
}
