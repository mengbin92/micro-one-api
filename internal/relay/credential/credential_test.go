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
	mu       sync.Mutex
	store    map[int64]*AccountCredentials
	stores   int32
	expiring []int64
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

func (f *fakeLookup) ExpiringSoon(_ context.Context, _ time.Duration) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.expiring...), nil
}

type fakeRefreshProvider struct {
	mu          sync.Mutex
	errs        []error
	calls       int
	invalidated []int64
}

func (p *fakeRefreshProvider) GetAccessToken(context.Context, int64) (string, error) {
	return "token", nil
}

func (p *fakeRefreshProvider) Refresh(context.Context, int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.errs) == 0 {
		return nil
	}
	err := p.errs[0]
	p.errs = p.errs[1:]
	return err
}

func (p *fakeRefreshProvider) Invalidate(accountID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.invalidated = append(p.invalidated, accountID)
}

type fakeRefreshHook struct {
	mu             sync.Mutex
	success        []int64
	nonRetryable   []string
	retryExhausted []string
	until          []time.Time
}

func (h *fakeRefreshHook) OnRefreshSuccess(_ context.Context, accountID int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.success = append(h.success, accountID)
	return nil
}

func (h *fakeRefreshHook) OnRefreshNonRetryable(_ context.Context, accountID int64, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nonRetryable = append(h.nonRetryable, fmt.Sprintf("%d:%s", accountID, reason))
	return nil
}

func (h *fakeRefreshHook) OnRefreshRetryExhausted(_ context.Context, accountID int64, until time.Time, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.retryExhausted = append(h.retryExhausted, fmt.Sprintf("%d:%s", accountID, reason))
	h.until = append(h.until, until)
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
	c.set(2, "tok", time.Now().Add(10*time.Minute))
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

func TestRefreshTask_SuccessInvalidatesAndCallsHook(t *testing.T) {
	lookup := newFakeLookup()
	lookup.expiring = []int64{42}
	provider := &fakeRefreshProvider{}
	hook := &fakeRefreshHook{}
	task := NewRefreshTask(
		map[Platform]TokenProvider{PlatformClaude: provider},
		lookup,
		func(int64) Platform { return PlatformClaude },
		RefreshTaskConfig{Interval: time.Second, Lookahead: time.Hour, Hook: hook},
	)
	task.sweep()
	task.Stop()

	if provider.calls != 1 {
		t.Fatalf("expected 1 refresh call, got %d", provider.calls)
	}
	if len(provider.invalidated) != 1 || provider.invalidated[0] != 42 {
		t.Fatalf("expected account 42 invalidated, got %#v", provider.invalidated)
	}
	if len(hook.success) != 1 || hook.success[0] != 42 {
		t.Fatalf("expected success hook for account 42, got %#v", hook.success)
	}
}

func TestRefreshTask_RetryExhaustedMarksTemporarilyUnschedulable(t *testing.T) {
	lookup := newFakeLookup()
	lookup.expiring = []int64{7}
	provider := &fakeRefreshProvider{errs: []error{ErrRefreshFailed, ErrRefreshFailed, ErrRefreshFailed}}
	hook := &fakeRefreshHook{}
	task := NewRefreshTask(
		map[Platform]TokenProvider{PlatformClaude: provider},
		lookup,
		func(int64) Platform { return PlatformClaude },
		RefreshTaskConfig{
			Interval:                  time.Second,
			Lookahead:                 time.Hour,
			MaxRetries:                3,
			RetryBackoff:              time.Millisecond,
			TempUnschedulableDuration: time.Minute,
			Hook:                      hook,
		},
	)
	before := time.Now()
	task.sweep()
	task.Stop()

	if provider.calls != 3 {
		t.Fatalf("expected 3 refresh attempts, got %d", provider.calls)
	}
	if len(hook.retryExhausted) != 1 || hook.retryExhausted[0] == "" {
		t.Fatalf("expected retry-exhausted hook, got %#v", hook.retryExhausted)
	}
	if len(hook.until) != 1 || hook.until[0].Before(before.Add(55*time.Second)) {
		t.Fatalf("expected temp unschedulable deadline near +1m, got %#v", hook.until)
	}
}

func TestRefreshTask_NonRetryableDoesNotRetry(t *testing.T) {
	lookup := newFakeLookup()
	lookup.expiring = []int64{9}
	provider := &fakeRefreshProvider{errs: []error{fmt.Errorf("%w: invalid_grant", ErrRefreshFailed)}}
	hook := &fakeRefreshHook{}
	task := NewRefreshTask(
		map[Platform]TokenProvider{PlatformClaude: provider},
		lookup,
		func(int64) Platform { return PlatformClaude },
		RefreshTaskConfig{
			Interval:     time.Second,
			Lookahead:    time.Hour,
			MaxRetries:   3,
			RetryBackoff: time.Millisecond,
			Hook:         hook,
		},
	)
	task.sweep()
	task.Stop()

	if provider.calls != 1 {
		t.Fatalf("expected non-retryable error to stop after 1 attempt, got %d", provider.calls)
	}
	if len(hook.nonRetryable) != 1 || hook.nonRetryable[0] == "" {
		t.Fatalf("expected non-retryable hook, got %#v", hook.nonRetryable)
	}
	if len(hook.retryExhausted) != 0 {
		t.Fatalf("non-retryable error should not mark retry exhausted: %#v", hook.retryExhausted)
	}
}

func TestSentinelErrors(t *testing.T) {
	if ErrAccountNotFound == nil || ErrNoRefreshToken == nil || ErrRefreshFailed == nil || ErrNotConfigured == nil {
		t.Fatal("sentinel errors must be non-nil")
	}
}
