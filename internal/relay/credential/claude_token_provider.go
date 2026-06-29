package credential

import (
	"context"
	"net/http"
)

// ClaudeTokenProvider implements TokenProvider for Claude Code subscription
// accounts. It refreshes against the Anthropic OAuth token endpoint. All
// shared cache/refresh/serialization logic lives in baseTokenProvider; this
// type only contributes the platform-specific constants.
type ClaudeTokenProvider struct {
	baseTokenProvider
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
		baseTokenProvider: newBaseTokenProvider(lookup, hc, ClaudeOAuthClientID, ClaudeTokenRefreshURL),
	}
}

// GetAccessToken returns a valid Claude OAuth access token, refreshing
// transparently when the cached token is within RefreshSkew of expiry.
func (p *ClaudeTokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	return p.baseTokenProvider.GetAccessToken(ctx, accountID)
}

// Refresh forces a token refresh for the account.
func (p *ClaudeTokenProvider) Refresh(ctx context.Context, accountID int64) error {
	return p.baseTokenProvider.Refresh(ctx, accountID)
}

// compile-time interface check.
var _ TokenProvider = (*ClaudeTokenProvider)(nil)
