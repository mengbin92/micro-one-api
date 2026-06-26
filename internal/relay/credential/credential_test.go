package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLookup is an in-memory AccountLookup for tests.
type fakeLookup struct {
	mu     sync.Mutex
	store  map[int64]*AccountCredentials
	stores int32
}

func newFakeLookup() *fakeLookup {
	return &fakeLookup{store: make(map[int64]*AccountCredentials)}
}

func (f *fakeLookup) Lookup(_ context.Context, id int64) (*AccountCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.store[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	// Return a copy.
	cp := *c
	return &cp, nil
}

func (f *fakeLookup) Store(_ context.Context, id int64, c *AccountCredentials) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.stores, 1)
	cp := *c
	f.store[id] = &cp
	return nil
}

// tokenServer returns a test OAuth token endpoint that issues a new access
// token and (optionally) rotates the refresh token.
func tokenServer(t *testing.T, expiresIn int, rotateRefresh bool) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		resp := tokenRefreshResponse{AccessToken: fmt.Sprintf("acc-%d", atomic.LoadInt32(&calls)), ExpiresIn: expiresIn, TokenType: "bearer"}
		if rotateRefresh {
			resp.RefreshToken = fmt.Sprintf("ref-%d", atomic.LoadInt32(&calls))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	return srv, &calls
}

func TestClaudeTokenProvider_GetAccessToken_RefreshesWhenStale(t *testing.T) {
	srv, calls := tokenServer(t, 3600, false)
	defer srv.Close()

	lookup := newFakeLookup()
	lookup.store[1] = &AccountCredentials{
		AccessToken:  "expired",
		RefreshToken: "rt-initial",
		ExpiresAt:    time.Now().Add(-time.Hour), // already expired
		RefreshURL:   srv.URL,
	}

	hc := &http.Client{Timeout: 5 * time.Second}
	p := NewClaudeTokenProviderWithHTTPClient(lookup, hc)

	token, err := p.GetAccessToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if token != "acc-1" {
		t.Fatalf("expected refreshed token acc-1, got %s", token)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("expected 1 refresh call, got %d", atomic.LoadInt32(calls))
	}
	// Persisted new token.
	if lookup.store[1].AccessToken != "acc-1" {
		t.Fatalf("store not updated: %s", lookup.store[1].AccessToken)
	}
}

func TestClaudeTokenProvider_GetAccessToken_CachesValidToken(t *testing.T) {
	srv, calls := tokenServer(t, 3600, false)
	defer srv.Close()

	lookup := newFakeLookup()
	lookup.store[1] = &AccountCredentials{
		AccessToken:  "valid",
		RefreshToken: "rt-initial",
		ExpiresAt:    time.Now().Add(time.Hour), // valid, well outside skew
		RefreshURL:   srv.URL,
	}
	p := NewClaudeTokenProviderWithHTTPClient(lookup, &http.Client{Timeout: 5 * time.Second})

	// Two rapid calls should only hit the cache.
	for i := 0; i < 2; i++ {
		token, err := p.GetAccessToken(context.Background(), 1)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if token != "valid" {
			t.Fatalf("call %d: expected cached 'valid', got %s", i, token)
		}
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("expected 0 refresh calls for valid token, got %d", atomic.LoadInt32(calls))
	}
}

func TestClaudeTokenProvider_RefreshRotatesToken(t *testing.T) {
	srv, _ := tokenServer(t, 3600, true) // rotate refresh token
	defer srv.Close()

	lookup := newFakeLookup()
	lookup.store[1] = &AccountCredentials{
		AccessToken:  "expired",
		RefreshToken: "rt-initial",
		ExpiresAt:    time.Now().Add(-time.Hour),
		RefreshURL:   srv.URL,
	}
	p := NewClaudeTokenProviderWithHTTPClient(lookup, &http.Client{Timeout: 5 * time.Second})

	if err := p.Refresh(context.Background(), 1); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if lookup.store[1].RefreshToken == "rt-initial" {
		t.Fatal("refresh token should have been rotated")
	}
}

func TestTokenProvider_NoRefreshToken(t *testing.T) {
	lookup := newFakeLookup()
	lookup.store[1] = &AccountCredentials{AccessToken: "x", ExpiresAt: time.Now().Add(-time.Hour)}
	p := NewClaudeTokenProvider(lookup)
	if _, err := p.GetAccessToken(context.Background(), 1); err == nil {
		t.Fatal("expected error when no refresh token")
	}
}

func TestTokenProvider_AccountNotFound(t *testing.T) {
	p := NewClaudeTokenProvider(newFakeLookup())
	if _, err := p.GetAccessToken(context.Background(), 999); err == nil {
		t.Fatal("expected ErrAccountNotFound")
	}
}

func TestTokenProvider_NotConfigured(t *testing.T) {
	p := NewClaudeTokenProvider(nil)
	if _, err := p.GetAccessToken(context.Background(), 1); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestRefreshSkew(t *testing.T) {
	// A token 1 minute in the future is within the 3-minute skew => stale.
	c := newTokenCache()
	c.set(1, "tok", time.Now().Add(time.Minute))
	if !c.stale(1) {
		t.Fatal("token 1m in future should be considered stale (within 3m skew)")
	}
	// A token 10 minutes in the future is fresh.
	c.set(2, "tok", time.Now().Add(10 * time.Minute))
	if c.stale(2) {
		t.Fatal("token 10m in future should not be stale")
	}
}

func TestRefreshTask_NoOpWithoutScanner(t *testing.T) {
	lookup := newFakeLookup()
	task := NewRefreshTask(
		map[Platform]TokenProvider{PlatformClaude: NewClaudeTokenProvider(lookup)},
		lookup,
		func(int64) Platform { return PlatformClaude },
		RefreshTaskConfig{Interval: 50 * time.Millisecond, Lookahead: time.Hour},
	)
	task.Start()
	time.Sleep(120 * time.Millisecond)
	task.Stop()
	// No panic / hang => pass.
}

func TestSentinelErrors(t *testing.T) {
	if ErrAccountNotFound == nil || ErrNoRefreshToken == nil || ErrRefreshFailed == nil || ErrNotConfigured == nil {
		t.Fatal("sentinel errors must be non-nil")
	}
}
