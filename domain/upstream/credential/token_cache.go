package credential

import (
	"sync"
	"time"
)

// tokenCache is an in-process cache of access tokens keyed by account ID. It
// exists so the hot path (GetAccessToken) does not hit the AccountLookup on
// every request. In a multi-instance deployment the Redis-backed provider
// should be used instead; this cache is the single-process fallback.
type tokenCache struct {
	mu  sync.RWMutex
	m   map[int64]cacheEntry
	now func() time.Time
}

type cacheEntry struct {
	accessToken string
	expiresAt   time.Time
	// creds holds the full credential set (including a rotated refresh token)
	// when set. It lets resolve reuse a refreshed refresh token in-process even
	// if persistence (Store) failed, so the account does not brick on the next
	// refresh (domain-M1). Nil for cache entries seeded from an access-token-only
	// lookup.
	creds *AccountCredentials
}

func newTokenCache() *tokenCache {
	return &tokenCache{m: make(map[int64]cacheEntry), now: time.Now}
}

func (c *tokenCache) get(accountID int64) (string, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[accountID]
	if !ok {
		return "", time.Time{}, false
	}
	return e.accessToken, e.expiresAt, true
}

func (c *tokenCache) set(accountID int64, token string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[accountID] = cacheEntry{accessToken: token, expiresAt: expiresAt}
}

// setCreds stores the full credential set for an account, including the
// rotated refresh token. Used after a refresh so a subsequent resolve can fall
// back to the in-process refresh token when the persistent Store failed
// (domain-M1).
func (c *tokenCache) setCreds(accountID int64, creds *AccountCredentials) {
	if creds == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[accountID] = cacheEntry{accessToken: creds.AccessToken, expiresAt: creds.ExpiresAt, creds: creds}
}

// getCreds returns the full cached credential set if present.
func (c *tokenCache) getCreds(accountID int64) (*AccountCredentials, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[accountID]
	if !ok {
		return nil, false
	}
	return e.creds, e.creds != nil
}

func (c *tokenCache) delete(accountID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, accountID)
}

// stale reports whether the cached token is within RefreshSkew of expiry (or
// already expired).
func (c *tokenCache) stale(accountID int64) bool {
	_, exp, ok := c.get(accountID)
	if !ok {
		return true
	}
	return !c.now().Add(RefreshSkew).Before(exp)
}

// staleExpiry reports whether a token with the given absolute expiry is within
// RefreshSkew of expiring (or already expired). It uses time.Now rather than
// the cache's injectable clock because it operates on freshly-loaded
// credentials, not cached entries.
func staleExpiry(expiresAt time.Time) bool {
	return !time.Now().Add(RefreshSkew).Before(expiresAt)
}
