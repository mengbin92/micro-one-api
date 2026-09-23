package credential

import (
	"sync"
	"time"
)

// tokenCache retains full credentials, including pending rotations, in process.
type tokenCache struct {
	mu  sync.RWMutex
	m   map[int64]cacheEntry
	now func() time.Time
}

type cacheEntry struct {
	dirtySince  time.Time
	accessToken string
	expiresAt   time.Time
	// creds holds the full credential set (including a rotated refresh token)
	// when set. It lets resolve reuse a refreshed refresh token in-process even
	// if persistence (Store) failed, so the account does not brick on the next
	// refresh (domain-M1). Production lookups always retain full credentials.
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
	cp := *creds
	c.m[accountID] = cacheEntry{accessToken: cp.AccessToken, expiresAt: cp.ExpiresAt, creds: &cp}
}

// getCreds returns the full cached credential set if present.
func (c *tokenCache) getCreds(accountID int64) (*AccountCredentials, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[accountID]
	if !ok {
		return nil, false
	}
	if e.creds == nil {
		return nil, false
	}
	cp := *e.creds
	return &cp, true
}

func (c *tokenCache) delete(accountID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m[accountID].dirtySince.IsZero() {
		delete(c.m, accountID)
	}
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

// markDirty is called under the provider's per-account lock before Store.
func (c *tokenCache) markDirty(id int64, creds *AccountCredentials) {
	c.mu.Lock()
	defer c.mu.Unlock()
	since := c.m[id].dirtySince
	if since.IsZero() {
		since = c.now()
	}
	cp := *creds
	c.m[id] = cacheEntry{accessToken: cp.AccessToken, expiresAt: cp.ExpiresAt, creds: &cp, dirtySince: since}
}
func (c *tokenCache) pending() map[int64]time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]time.Time)
	for id, e := range c.m {
		if !e.dirtySince.IsZero() {
			out[id] = e.dirtySince
		}
	}
	return out
}
func (c *tokenCache) discard(id int64) { c.mu.Lock(); defer c.mu.Unlock(); delete(c.m, id) }
