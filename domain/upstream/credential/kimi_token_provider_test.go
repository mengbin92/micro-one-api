package credential

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestKimiTokenProvider_RefreshSuccess exercises the full refresh path:
// stored token is stale -> provider POSTs to the token endpoint -> new token
// is cached and returned. This mirrors the Claude/Codex provider tests but
// uses a live httptest.Server so the Kimi refresh URL is exercised end-to-end.
func TestKimiTokenProvider_RefreshSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", r.FormValue("grant_type"))
		}
		if r.FormValue("client_id") != KimiOAuthClientID {
			t.Errorf("client_id = %q, want %q", r.FormValue("client_id"), KimiOAuthClientID)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"kimi-new","refresh_token":"kimi-rt-new","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	lookup := newFakeLookup()
	lookup.store[1] = &AccountCredentials{
		AccessToken:  "kimi-old",
		RefreshToken: "kimi-rt",
		ExpiresAt:    time.Now().Add(-time.Hour), // already expired
	}

	prevURL := KimiTokenRefreshURL
	KimiTokenRefreshURL = srv.URL
	defer func() { KimiTokenRefreshURL = prevURL }()

	p := NewKimiTokenProviderWithHTTPClient(lookup, srv.Client())
	tok, err := p.GetAccessToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if tok != "kimi-new" {
		t.Fatalf("GetAccessToken = %q, want kimi-new", tok)
	}

	// The refreshed credentials must be persisted.
	stored := lookup.store[1]
	if stored.AccessToken != "kimi-new" {
		t.Fatalf("stored access_token = %q, want kimi-new", stored.AccessToken)
	}
	if stored.RefreshToken != "kimi-rt-new" {
		t.Fatalf("stored refresh_token = %q, want kimi-rt-new", stored.RefreshToken)
	}
}

// TestKimiTokenProvider_RefreshFailure surfaces ErrRefreshFailed when the
// upstream token endpoint rejects the refresh.
func TestKimiTokenProvider_RefreshFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	lookup := newFakeLookup()
	lookup.store[2] = &AccountCredentials{
		AccessToken:  "kimi-old",
		RefreshToken: "kimi-rt",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}

	prevURL := KimiTokenRefreshURL
	KimiTokenRefreshURL = srv.URL
	defer func() { KimiTokenRefreshURL = prevURL }()

	p := NewKimiTokenProviderWithHTTPClient(lookup, srv.Client())
	_, err := p.GetAccessToken(context.Background(), 2)
	if err == nil {
		t.Fatal("expected refresh error, got nil")
	}
}

// TestKimiTokenProvider_ConcurrentDedup verifies that concurrent
// GetAccessToken calls for the same account do not all hit the token
// endpoint: the per-account mutex in baseTokenProvider serialises them so
// only one refresh occurs.
func TestKimiTokenProvider_ConcurrentDedup(t *testing.T) {
	var refreshes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Simulate a slow token endpoint so concurrent callers pile up.
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&refreshes, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"kimi-dedup","refresh_token":"rt","expires_in":3600}`))
	}))
	defer srv.Close()

	lookup := newFakeLookup()
	lookup.store[3] = &AccountCredentials{
		AccessToken:  "",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}

	prevURL := KimiTokenRefreshURL
	KimiTokenRefreshURL = srv.URL
	defer func() { KimiTokenRefreshURL = prevURL }()

	p := NewKimiTokenProviderWithHTTPClient(lookup, srv.Client())

	const n = 5
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := p.GetAccessToken(context.Background(), 3)
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent GetAccessToken[%d]: %v", i, err)
		}
	}
	// Exactly one refresh should have hit the server: baseTokenProvider's
	// per-account mutex serialises concurrent callers so only the first
	// refreshes; the rest read the updated cache.
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Fatalf("expected exactly one upstream refresh, got %d", got)
	}
}
