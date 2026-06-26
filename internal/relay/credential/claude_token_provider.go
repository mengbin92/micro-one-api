package credential

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// ClaudeTokenProvider implements TokenProvider for Claude Code subscription
// accounts. It refreshes against the Anthropic OAuth token endpoint.
//
// In the MVP the token endpoint and client_id are hardcoded to the published
// Claude Code OAuth values; a full deployment reads them from the
// SubscriptionAccount record (RefreshURL/ClientID fields).
type ClaudeTokenProvider struct {
	lookup    AccountLookup
	cache     *tokenCache
	refresher *refresher

	// refreshMu serializes concurrent refreshes for the same account so that
	// a thundering herd of requests does not all hit the token endpoint at
	// once. One refresh per account wins; the rest read the updated cache.
	refreshMu sync.Map // accountID int64 -> *sync.Mutex
}

// ClaudeOAuthClientID is the published OAuth client_id for Claude Code.
const ClaudeOAuthClientID = "9d1c250a-e61b-44d4-8bcb-9604d4e4c824"

// ClaudeTokenRefreshURL is the Anthropic OAuth token endpoint used by the
// Claude Code CLI.
const ClaudeTokenRefreshURL = "https://console.anthropic.com/v1/oauth/token"

// NewClaudeTokenProvider builds a Claude token provider backed by the given
// account lookup. The refresh HTTP client defaults to 30s; pass a custom
// client via NewClaudeTokenProviderWithHTTPClient for tests.
func NewClaudeTokenProvider(lookup AccountLookup) *ClaudeTokenProvider {
	return NewClaudeTokenProviderWithHTTPClient(lookup, defaultRefreshHTTPClient())
}

// NewClaudeTokenProviderWithHTTPClient is the testable constructor.
func NewClaudeTokenProviderWithHTTPClient(lookup AccountLookup, hc *http.Client) *ClaudeTokenProvider {
	return &ClaudeTokenProvider{
		lookup: lookup,
		cache:  newTokenCache(),
		refresher: &refresher{
			httpClient: hc,
			clientID:   ClaudeOAuthClientID,
		},
	}
}

// GetAccessToken returns a valid Claude OAuth access token. It checks the
// in-process cache first; on a miss it consults the AccountLookup, and only
// refreshes when the stored token is within RefreshSkew of expiry.
func (p *ClaudeTokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	if p.lookup == nil {
		return "", ErrNotConfigured
	}
	if token, _, ok := p.cache.get(accountID); ok && !p.cache.stale(accountID) {
		return token, nil
	}
	// Cache miss (or stale): serialize per-account resolution.
	mu := p.lockFor(accountID)
	mu.Lock()
	defer mu.Unlock()
	// Re-check after acquiring the lock: another goroutine may have refreshed.
	if token, _, ok := p.cache.get(accountID); ok && !p.cache.stale(accountID) {
		return token, nil
	}
	return p.resolve(ctx, accountID, false)
}

// Refresh forces a token refresh for the account regardless of expiry.
func (p *ClaudeTokenProvider) Refresh(ctx context.Context, accountID int64) error {
	mu := p.lockFor(accountID)
	mu.Lock()
	defer mu.Unlock()
	_, err := p.resolve(ctx, accountID, true)
	return err
}

// resolve either seeds the cache from a still-valid stored token or performs a
// refresh. force=true always refreshes.
func (p *ClaudeTokenProvider) resolve(ctx context.Context, accountID int64, force bool) (string, error) {
	creds, err := p.lookup.Lookup(ctx, accountID)
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
		p.cache.set(accountID, creds.AccessToken, creds.ExpiresAt)
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
		refreshURL = ClaudeTokenRefreshURL
	}
	newCreds, err := p.refresher.refresh(ctx, refreshURL, creds.RefreshToken)
	if err != nil {
		return "", err
	}
	// Preserve the account id and client id from the stored record.
	newCreds.AccountID = creds.AccountID
	newCreds.ClientID = creds.ClientID
	if newCreds.RefreshURL == "" {
		newCreds.RefreshURL = creds.RefreshURL
	}
	if storeErr := p.lookup.Store(ctx, accountID, newCreds); storeErr != nil {
		// We have a valid token even if persistence failed; cache it locally
		// so the current request can still proceed, but surface the store
		// error so it can be retried / logged.
		p.cache.set(accountID, newCreds.AccessToken, newCreds.ExpiresAt)
		return newCreds.AccessToken, fmt.Errorf("credential: token refreshed but persist failed: %w", storeErr)
	}
	p.cache.set(accountID, newCreds.AccessToken, newCreds.ExpiresAt)
	return newCreds.AccessToken, nil
}

func (p *ClaudeTokenProvider) lockFor(accountID int64) *sync.Mutex {
	v, _ := p.refreshMu.LoadOrStore(accountID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// compile-time interface check.
var _ TokenProvider = (*ClaudeTokenProvider)(nil)
