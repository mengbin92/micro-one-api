package credential

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// OpenAITokenProvider implements TokenProvider for ChatGPT / Codex
// subscription accounts. It refreshes against the ChatGPT OAuth token
// endpoint.
//
// The Codex CLI uses the same ChatGPT OAuth flow; the token endpoint and
// client_id are the published ChatGPT backend values.
type OpenAITokenProvider struct {
	lookup    AccountLookup
	cache     *tokenCache
	refresher *refresher
	refreshMu sync.Map
}

// CodexOAuthClientID is the published OAuth client_id used by codex_cli_rs.
const CodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// CodexTokenRefreshURL is the ChatGPT OAuth token endpoint used by codex_cli_rs.
const CodexTokenRefreshURL = "https://auth.openai.com/oauth/token"

// NewOpenAITokenProvider builds a Codex/ChatGPT token provider.
func NewOpenAITokenProvider(lookup AccountLookup) *OpenAITokenProvider {
	return NewOpenAITokenProviderWithHTTPClient(lookup, defaultRefreshHTTPClient())
}

// NewOpenAITokenProviderWithHTTPClient is the testable constructor.
func NewOpenAITokenProviderWithHTTPClient(lookup AccountLookup, hc *http.Client) *OpenAITokenProvider {
	return &OpenAITokenProvider{
		lookup: lookup,
		cache:  newTokenCache(),
		refresher: &refresher{
			httpClient: hc,
			clientID:   CodexOAuthClientID,
		},
	}
}

// GetAccessToken returns a valid Codex/ChatGPT OAuth access token. It checks
// the cache first, then the stored token, and only refreshes when necessary.
func (p *OpenAITokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	if p.lookup == nil {
		return "", ErrNotConfigured
	}
	if token, _, ok := p.cache.get(accountID); ok && !p.cache.stale(accountID) {
		return token, nil
	}
	mu := p.lockFor(accountID)
	mu.Lock()
	defer mu.Unlock()
	if token, _, ok := p.cache.get(accountID); ok && !p.cache.stale(accountID) {
		return token, nil
	}
	return p.resolve(ctx, accountID, false)
}

// Refresh forces a token refresh for the account.
func (p *OpenAITokenProvider) Refresh(ctx context.Context, accountID int64) error {
	mu := p.lockFor(accountID)
	mu.Lock()
	defer mu.Unlock()
	_, err := p.resolve(ctx, accountID, true)
	return err
}

func (p *OpenAITokenProvider) resolve(ctx context.Context, accountID int64, force bool) (string, error) {
	creds, err := p.lookup.Lookup(ctx, accountID)
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", ErrAccountNotFound
	}
	if !force && !staleExpiry(creds.ExpiresAt) && creds.AccessToken != "" {
		p.cache.set(accountID, creds.AccessToken, creds.ExpiresAt)
		return creds.AccessToken, nil
	}
	if creds.RefreshToken == "" {
		// No refresh token and the stored access token is stale: the account
		// cannot be used until it is re-authorized.
		return "", ErrNoRefreshToken
	}
	refreshURL := creds.RefreshURL
	if refreshURL == "" {
		refreshURL = CodexTokenRefreshURL
	}
	newCreds, err := p.refresher.refresh(ctx, refreshURL, creds.RefreshToken)
	if err != nil {
		return "", err
	}
	newCreds.AccountID = creds.AccountID
	newCreds.ClientID = creds.ClientID
	if newCreds.RefreshURL == "" {
		newCreds.RefreshURL = creds.RefreshURL
	}
	if storeErr := p.lookup.Store(ctx, accountID, newCreds); storeErr != nil {
		p.cache.set(accountID, newCreds.AccessToken, newCreds.ExpiresAt)
		return newCreds.AccessToken, fmt.Errorf("credential: token refreshed but persist failed: %w", storeErr)
	}
	p.cache.set(accountID, newCreds.AccessToken, newCreds.ExpiresAt)
	return newCreds.AccessToken, nil
}

func (p *OpenAITokenProvider) lockFor(accountID int64) *sync.Mutex {
	v, _ := p.refreshMu.LoadOrStore(accountID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// compile-time interface check.
var _ TokenProvider = (*OpenAITokenProvider)(nil)
