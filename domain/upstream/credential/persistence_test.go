package credential

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"micro-one-api/platform/metrics"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failingStore struct {
	*fakeLookup
	fail bool
}

func (s *failingStore) Store(ctx context.Context, id int64, c *AccountCredentials) error {
	if s.fail {
		return errors.New("database unavailable")
	}
	return s.fakeLookup.Store(ctx, id, c)
}

func TestPendingRotationSurvivesBackgroundSuccess(t *testing.T) {
	srv, _ := tokenServer(t, 3600, true)
	defer srv.Close()
	lookup := &failingStore{fakeLookup: newFakeLookup(), fail: true}
	lookup.store[1] = &AccountCredentials{RefreshToken: "initial", RefreshURL: srv.URL}
	provider := NewClaudeTokenProviderWithHTTPClient(lookup, srv.Client())
	beforeFailures := testutil.ToFloat64(metrics.CredentialPersistFailures.WithLabelValues("claude"))
	beforeSuccess := testutil.ToFloat64(metrics.CredentialPersistResults.WithLabelValues("claude", "success"))
	hook := &fakeRefreshHook{}
	task := NewRefreshTask(map[Platform]TokenProvider{PlatformClaude: provider}, lookup,
		func(int64) Platform { return PlatformClaude }, RefreshTaskConfig{Hook: hook})
	if err := task.refreshAccount(context.Background(), provider, 1); err != nil {
		t.Fatal(err)
	}
	if creds, ok := provider.cache.getCreds(1); !ok || creds.RefreshToken != "ref-1" {
		t.Fatal("background success discarded the only rotated credential")
	}
	if testutil.ToFloat64(metrics.CredentialPersistFailures.WithLabelValues("claude")) != beforeFailures+1 || testutil.ToFloat64(metrics.CredentialPending.WithLabelValues("claude")) != 1 {
		t.Fatal("pending persistence metrics missing")
	}
	lookup.fail = false
	// This cold account is absent from ExpiringSoon: only pending writes can find it.
	task.sweep()
	if testutil.ToFloat64(metrics.CredentialPending.WithLabelValues("claude")) != 0 || testutil.ToFloat64(metrics.CredentialPendingAge.WithLabelValues("claude")) != 0 || testutil.ToFloat64(metrics.CredentialPersistResults.WithLabelValues("claude", "success")) != beforeSuccess+1 {
		t.Fatal("recovery metrics not reset")
	}
	if lookup.store[1].RefreshToken != "ref-1" {
		t.Fatal("sweep did not persist the cold account without another OAuth refresh")
	}
}

func TestPendingRotationUsedByNextRefresh(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				want := "initial"
				if calls > 1 {
					want = "rotated"
				}
				if got := r.FormValue("refresh_token"); got != want {
					t.Errorf("refresh %d reused stale credential: got %q, want %q", calls, got, want)
				}
				_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"rotated","expires_in":60}`))
			}))
			defer srv.Close()
			lookup := &failingStore{fakeLookup: newFakeLookup(), fail: true}
			lookup.store[1] = &AccountCredentials{RefreshToken: "initial", RefreshURL: srv.URL, ExpiresAt: time.Now().Add(-time.Hour)}
			provider := NewClaudeTokenProviderWithHTTPClient(lookup, srv.Client())
			if err := provider.Refresh(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if _, err := provider.resolve(context.Background(), 1, force); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPendingPersistenceConflictDropsStaleRotation(t *testing.T) {
	lookup := NewNoopAccountLookup()
	lookup.Seed(1, PlatformClaude, &AccountCredentials{Revision: 2, AccessToken: "manual", RefreshToken: "manual-refresh", ExpiresAt: time.Now().Add(time.Hour)})
	provider := NewClaudeTokenProvider(lookup)
	provider.cache.markDirty(1, &AccountCredentials{Revision: 1, AccessToken: "stale", RefreshToken: "stale-refresh", ExpiresAt: time.Now().Add(time.Hour)})
	provider.PersistPending(context.Background(), time.Minute)
	got, err := provider.GetAccessToken(context.Background(), 1)
	if err != nil || got != "manual" {
		t.Fatalf("conflict retained stale credential: %q %v", got, err)
	}
	if len(provider.cache.pending()) != 0 {
		t.Fatal("superseded write still pending")
	}
}

func TestPendingSweepDoesNotRotateAgainWhenScannerAlsoFindsAccount(t *testing.T) {
	srv, calls := tokenServer(t, 3600, true)
	defer srv.Close()
	lookup := NewNoopAccountLookup()
	lookup.Seed(1, PlatformClaude, &AccountCredentials{RefreshToken: "initial", RefreshURL: srv.URL, ExpiresAt: time.Now()})
	provider := NewClaudeTokenProviderWithHTTPClient(lookup, srv.Client())
	provider.cache.markDirty(1, &AccountCredentials{RefreshToken: "rotated", AccessToken: "new", RefreshURL: srv.URL, ExpiresAt: time.Now().Add(time.Hour)})
	task := NewRefreshTask(map[Platform]TokenProvider{PlatformClaude: provider}, lookup, lookup.PlatformOf, RefreshTaskConfig{})
	task.sweep()
	if atomic.LoadInt32(calls) != 0 {
		t.Fatal("pending write performed another OAuth rotation")
	}
	if len(provider.cache.pending()) != 0 {
		t.Fatal("pending credential not written")
	}
}

func TestRefreshErrorsNeverExposeCredentialResponse(t *testing.T) {
	for _, status := range []int{200, 400, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":"invalid_grant","secret":"credential-marker" invalid}`)
			}))
			defer srv.Close()
			_, err := (&refresher{httpClient: srv.Client()}).refresh(context.Background(), srv.URL, "credential-marker")
			if err == nil || strings.Contains(err.Error(), "credential-marker") {
				t.Fatalf("unsafe refresh error: %v", err)
			}
		})
	}
}
