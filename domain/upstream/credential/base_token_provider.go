package credential

import (
	"context"
	"log"
	"net/http"
	"sync"
)

// baseTokenProvider holds the shared state and logic common to all platform
// token providers (Claude, Codex). Each concrete provider embeds it and only
// supplies the platform-specific refresh URL + client ID. This eliminates the
// ~90 lines of duplication between the Claude and OpenAI providers.
//
// Concurrency: GetAccessToken/Refresh serialize per-account via refreshMu so
// a thundering herd of requests does not all hit the token endpoint at once.
// One refresh per account wins; the rest read the updated cache.
type baseTokenProvider struct {
	lookup    AccountLookup
	cache     *tokenCache
	refresher *refresher
	refreshMu sync.Map // accountID int64 -> *sync.Mutex

	// defaultRefreshURL is used when the account's stored RefreshURL is empty.
	defaultRefreshURL string
}

// GetAccessToken returns a valid access token, refreshing transparently when
// the cached token is within RefreshSkew of expiry.
func (b *baseTokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	if b.lookup == nil {
		return "", ErrNotConfigured
	}
	if token, _, ok := b.cache.get(accountID); ok && !b.cache.stale(accountID) {
		return token, nil
	}
	mu := b.lockFor(accountID)
	mu.Lock()
	defer mu.Unlock()
	// Re-check after acquiring the lock: another goroutine may have refreshed.
	if token, _, ok := b.cache.get(accountID); ok && !b.cache.stale(accountID) {
		return token, nil
	}
	return b.resolve(ctx, accountID, false)
}

// Refresh forces a token refresh for the account regardless of expiry.
func (b *baseTokenProvider) Refresh(ctx context.Context, accountID int64) error {
	mu := b.lockFor(accountID)
	mu.Lock()
	defer mu.Unlock()
	_, err := b.resolve(ctx, accountID, true)
	return err
}

func (b *baseTokenProvider) Invalidate(accountID int64) {
	b.cache.delete(accountID)
}

// resolve either seeds the cache from a still-valid stored token or performs a
// refresh. force=true always refreshes.
func (b *baseTokenProvider) resolve(ctx context.Context, accountID int64, force bool) (string, error) {
	// domain-M1: prefer the in-process full credential cache when the access
	// token is still valid. A previous refresh may have rotated the refresh
	// token and failed to persist it (Store error); the persistent Lookup would
	// then return the stale (now-invalid) refresh token and the account would
	// brick on the next refresh. The cached creds carry the rotated token.
	if cached, ok := b.cache.getCreds(accountID); ok && cached.AccessToken != "" && !staleExpiry(cached.ExpiresAt) && !force {
		b.cache.set(accountID, cached.AccessToken, cached.ExpiresAt)
		return cached.AccessToken, nil
	}
	creds, err := b.lookup.Lookup(ctx, accountID)
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", ErrAccountNotFound
	}
	// If the stored token is still valid and we are not forcing, seed the cache
	// from it and return. This avoids a redundant refresh when the provider's
	// in-process cache was cold (e.g. after a process restart) but the stored
	// token is still good.
	if !force && !staleExpiry(creds.ExpiresAt) && creds.AccessToken != "" {
		b.cache.set(accountID, creds.AccessToken, creds.ExpiresAt)
		return creds.AccessToken, nil
	}
	if creds.RefreshToken == "" {
		// No refresh token and the stored access token is stale: the account
		// cannot be used until it is re-authorized. Surface the sentinel so the
		// caller can mark the account temporarily unschedulable.
		return "", ErrNoRefreshToken
	}
	refreshURL := creds.RefreshURL
	if refreshURL == "" {
		refreshURL = b.defaultRefreshURL
	}
	newCreds, err := b.refresher.refresh(ctx, refreshURL, creds.RefreshToken)
	if err != nil {
		return "", err
	}
	// Preserve the account id and client id from the stored record.
	newCreds.AccountID = creds.AccountID
	newCreds.ClientID = creds.ClientID
	if newCreds.RefreshURL == "" {
		newCreds.RefreshURL = creds.RefreshURL
	}
	if storeErr := b.lookup.Store(ctx, accountID, newCreds); storeErr != nil {
		// domain-M1: persistence failed but we hold a fully-valid refreshed
		// credential set, including the ROTATED refresh token. Cache the whole
		// set in-process so (a) the current request succeeds with the new access
		// token and (b) the next resolve reuses the rotated refresh token instead
		// of the stale one the persistent lookup would return (which would brick
		// the account once the access token expires). Do NOT return the store
		// error: a typical caller does `if err != nil { return err }`, which would
		// fail a request that has a perfectly good token. Report the persist
		// failure via log/meter only.
		b.cache.setCreds(accountID, newCreds)
		logPersistFailure(accountID, storeErr)
		return newCreds.AccessToken, nil
	}
	b.cache.setCreds(accountID, newCreds)
	return newCreds.AccessToken, nil
}

func (b *baseTokenProvider) lockFor(accountID int64) *sync.Mutex {
	v, _ := b.refreshMu.LoadOrStore(accountID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// logPersistFailure reports a credential persistence failure without failing
// the request. A refreshed access token is still valid; only its durable store
// failed. We log so operators can investigate storage health without breaking
// authentication (domain-M1).
func logPersistFailure(accountID int64, err error) {
	log.Printf("credential: account %d token refreshed but persist failed (will retry on next resolve): %v", accountID, err)
}

// newBaseTokenProvider builds the shared state for a platform provider.
func newBaseTokenProvider(lookup AccountLookup, hc *http.Client, clientID, defaultRefreshURL string) baseTokenProvider {
	if hc == nil {
		hc = defaultRefreshHTTPClient()
	}
	return baseTokenProvider{
		lookup:            lookup,
		cache:             newTokenCache(),
		refresher:         &refresher{httpClient: hc, clientID: clientID},
		defaultRefreshURL: defaultRefreshURL,
	}
}
