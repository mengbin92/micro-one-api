package data

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/credential"
)

func credentialRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func TestCredentialLeaseExpiryCannotDeleteSuccessor(t *testing.T) {
	mr, rdb := credentialRedis(t)
	coordinator := NewCredentialRefreshCoordinator(rdb)
	ctx := context.Background()
	first, err := coordinator.Acquire(ctx, 1)
	require.NoError(t, err)
	_, err = coordinator.Acquire(ctx, 1)
	require.ErrorIs(t, err, credential.ErrRefreshBusy)
	mr.FastForward(credentialRefreshLeaseTTL)
	second, err := coordinator.Acquire(ctx, 1)
	require.NoError(t, err)
	require.ErrorIs(t, first.Check(ctx), credential.ErrCoordinationUnavailable)
	first.Release(ctx)
	require.NoError(t, second.Check(ctx))
	second.Release(ctx)
	mr.SetError("Redis unavailable")
	_, err = coordinator.Acquire(ctx, 1)
	require.ErrorIs(t, err, credential.ErrCoordinationUnavailable)
	mr.SetError("")
	third, err := coordinator.Acquire(ctx, 1)
	require.NoError(t, err)
	third.Release(ctx)
}

func newCoordinatedClaude(t *testing.T, lookup credential.AccountLookup, rdb *redis.Client, client *http.Client) *credential.ClaudeTokenProvider {
	t.Helper()
	p := credential.NewClaudeTokenProviderWithHTTPClient(lookup, client)
	require.NoError(t, p.SetRefreshCoordinator(NewCredentialRefreshCoordinator(rdb)))
	return p
}

func TestCredentialReplicasFenceExpiredOwnerAndReauthorization(t *testing.T) {
	ctx := context.Background()
	mr, rdb := credentialRedis(t)
	lookup := credential.NewNoopAccountLookup()
	started, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-finish
		_, _ = w.Write([]byte(`{"access_token":"late-access","refresh_token":"late-refresh","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { once.Do(func() { close(finish) }) })
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "one-use", RefreshURL: srv.URL})
	first := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	second := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	done := make(chan error, 1)
	go func() { done <- first.Refresh(ctx, 1) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("OAuth request did not start")
	}
	require.ErrorIs(t, second.Refresh(ctx, 1), credential.ErrRefreshBusy)
	mr.FastForward(credentialRefreshLeaseTTL)
	// A stopped/crashed owner is never replaced using the old refresh token.
	require.ErrorIs(t, second.Refresh(ctx, 1), credential.ErrRefreshUncertain)
	require.EqualValues(t, 1, calls.Load())
	old, err := lookup.Lookup(ctx, 1)
	require.NoError(t, err)
	require.True(t, old.RefreshPending)
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{Revision: old.Revision + 1, AccessToken: "manual-access", RefreshToken: "manual-refresh", ExpiresAt: time.Now().Add(time.Hour)})
	once.Do(func() { close(finish) })
	require.ErrorIs(t, <-done, credential.ErrCredentialConflict)
	for _, p := range []*credential.ClaudeTokenProvider{first, second} {
		token, err := p.GetAccessToken(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, "manual-access", token)
	}
	require.EqualValues(t, 1, calls.Load())
}

type failingCredentialStore struct {
	*credential.NoopAccountLookup
	failStore atomic.Bool
	loseClaim bool
	loseStore atomic.Bool
}

func (s *failingCredentialStore) Store(ctx context.Context, id int64, c *credential.AccountCredentials) error {
	if s.failStore.Load() {
		return errors.New("storage unavailable; sensitive detail must not escape")
	}
	if s.loseStore.Load() {
		cp := *c
		if err := s.NoopAccountLookup.Store(ctx, id, &cp); err != nil {
			return err
		}
		return errors.New("store response lost")
	}
	return s.NoopAccountLookup.Store(ctx, id, c)
}

func (s *failingCredentialStore) ClaimRefresh(ctx context.Context, id int64, c *credential.AccountCredentials) error {
	if err := s.NoopAccountLookup.ClaimRefresh(ctx, id, c); err != nil {
		return err
	}
	if s.loseClaim {
		return errors.New("claim response lost")
	}
	return nil
}

func TestCredentialPendingRotationRecoveredWithoutAnotherOAuthCall(t *testing.T) {
	ctx := context.Background()
	_, rdb := credentialRedis(t)
	lookup := &failingCredentialStore{NoopAccountLookup: credential.NewNoopAccountLookup()}
	lookup.failStore.Store(true)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "one-use", RefreshURL: srv.URL})
	owner := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	peer := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	token, err := owner.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "rotated-access", token)
	require.ErrorIs(t, peer.Refresh(ctx, 1), credential.ErrRefreshUncertain)
	require.ErrorIs(t, owner.Refresh(ctx, 1), credential.ErrCredentialPersistencePending)
	require.EqualValues(t, 1, calls.Load())
	lookup.failStore.Store(false)
	require.Equal(t, []int64{1}, owner.PersistPending(ctx, time.Minute))
	token, err = peer.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "rotated-access", token)
	stored, err := lookup.Lookup(ctx, 1)
	require.NoError(t, err)
	require.False(t, stored.RefreshPending)
	require.EqualValues(t, 2, stored.Revision)
	require.EqualValues(t, 1, calls.Load())
	// A warm provider must observe a manual authorization on its next read.
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{Revision: 3, AccessToken: "new-authorization", ExpiresAt: time.Now().Add(time.Hour)})
	token, err = owner.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "new-authorization", token)
}

func TestCredentialLostClaimAndOAuthFailureAreNotReplayed(t *testing.T) {
	for _, loseClaim := range []bool{true, false} {
		t.Run(map[bool]string{true: "lost_claim_response", false: "lost_oauth_response"}[loseClaim], func(t *testing.T) {
			ctx := context.Background()
			_, rdb := credentialRedis(t)
			lookup := &failingCredentialStore{NoopAccountLookup: credential.NewNoopAccountLookup(), loseClaim: loseClaim}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer srv.Close()
			lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "one-use", RefreshURL: srv.URL})
			owner := newCoordinatedClaude(t, lookup, rdb, srv.Client())
			require.Error(t, owner.Refresh(ctx, 1))
			// A new provider models losing all process-local state on restart.
			restarted := newCoordinatedClaude(t, lookup, rdb, srv.Client())
			require.ErrorIs(t, restarted.Refresh(ctx, 1), credential.ErrRefreshUncertain)
			if loseClaim {
				require.Zero(t, calls.Load())
			} else {
				require.EqualValues(t, 1, calls.Load())
			}
		})
	}
}

func TestCredentialRedisFailureDoesNotRefreshAndRecovers(t *testing.T) {
	ctx := context.Background()
	mr, rdb := credentialRedis(t)
	lookup := credential.NewNoopAccountLookup()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "one-use", RefreshURL: srv.URL})
	p := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	mr.SetError("Redis unavailable")
	require.ErrorIs(t, p.Refresh(ctx, 1), credential.ErrCoordinationUnavailable)
	require.Zero(t, calls.Load())
	stored, err := lookup.Lookup(ctx, 1)
	require.NoError(t, err)
	require.False(t, stored.RefreshPending)
	mr.SetError("")
	require.NoError(t, p.Refresh(ctx, 1))
	require.EqualValues(t, 1, calls.Load())
	// Shared, already-valid credentials remain readable during Redis failure.
	mr.SetError("Redis unavailable")
	token, err := p.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "access", token)
}

func TestCredentialConcurrentReadersShareRotationAndCancelWait(t *testing.T) {
	ctx := context.Background()
	_, rdb := credentialRedis(t)
	lookup := credential.NewNoopAccountLookup()
	started, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-finish
		_, _ = w.Write([]byte(`{"access_token":"shared-access","refresh_token":"shared-refresh","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { once.Do(func() { close(finish) }) })
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "one-use", RefreshURL: srv.URL})
	owner := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	peer := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- owner.Refresh(ctx, 1) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("OAuth request did not start")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := peer.GetAccessToken(waitCtx, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	localCtx, localCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer localCancel()
	_, err = owner.GetAccessToken(localCtx, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	type result struct {
		token string
		err   error
	}
	peerDone := make(chan result, 1)
	go func() { token, err := peer.GetAccessToken(ctx, 1); peerDone <- result{token, err} }()
	once.Do(func() { close(finish) })
	require.NoError(t, <-ownerDone)
	got := <-peerDone
	require.NoError(t, got.err)
	require.Equal(t, "shared-access", got.token)
	require.EqualValues(t, 1, calls.Load())
}

func TestCredentialStoreResponseLossDoesNotReplayOAuth(t *testing.T) {
	ctx := context.Background()
	_, rdb := credentialRedis(t)
	lookup := &failingCredentialStore{NoopAccountLookup: credential.NewNoopAccountLookup()}
	lookup.loseStore.Store(true)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"committed-access","refresh_token":"committed-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "one-use", RefreshURL: srv.URL})
	owner := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	require.ErrorIs(t, owner.Refresh(ctx, 1), credential.ErrCredentialPersistencePending)
	peer := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	token, err := peer.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "committed-access", token)
	lookup.loseStore.Store(false)
	token, err = owner.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "committed-access", token)
	require.Empty(t, owner.PersistPending(ctx, time.Minute))
	require.EqualValues(t, 1, calls.Load())
}

func TestCredentialRecoveredStaleRotationRefreshesOnlyAfterPersistence(t *testing.T) {
	ctx := context.Background()
	_, rdb := credentialRedis(t)
	lookup := &failingCredentialStore{NoopAccountLookup: credential.NewNoopAccountLookup()}
	lookup.failStore.Store(true)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"access_token":"short-lived","refresh_token":"rotated-refresh","expires_in":1}`))
		} else {
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "rotated-refresh" {
				w.WriteHeader(400)
				return
			}
			stored, _ := lookup.Lookup(ctx, 1)
			if stored.Revision != 3 || !stored.RefreshPending {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`))
		}
	}))
	defer srv.Close()
	lookup.Seed(1, credential.PlatformClaude, &credential.AccountCredentials{RefreshToken: "original", RefreshURL: srv.URL})
	owner := newCoordinatedClaude(t, lookup, rdb, srv.Client())
	require.ErrorIs(t, owner.Refresh(ctx, 1), credential.ErrCredentialPersistencePending)
	lookup.failStore.Store(false)
	token, err := owner.GetAccessToken(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "fresh-access", token)
	require.EqualValues(t, 2, calls.Load())
}
