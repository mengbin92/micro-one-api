package credential

import (
	"context"
	"net/http"
)

// OpenAITokenProvider implements TokenProvider for ChatGPT / Codex
// subscription accounts. It refreshes against the ChatGPT OAuth token
// endpoint. All shared cache/refresh/serialization logic lives in
// baseTokenProvider; this type only contributes the platform-specific
// constants.
type OpenAITokenProvider struct {
	baseTokenProvider
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
		baseTokenProvider: newBaseTokenProvider(lookup, hc, CodexOAuthClientID, CodexTokenRefreshURL),
	}
}

// GetAccessToken returns a valid Codex/ChatGPT OAuth access token. It checks
// the cache first, then the stored token, and only refreshes when necessary.
func (p *OpenAITokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	return p.baseTokenProvider.GetAccessToken(ctx, accountID)
}

// Refresh forces a token refresh for the account.
func (p *OpenAITokenProvider) Refresh(ctx context.Context, accountID int64) error {
	return p.baseTokenProvider.Refresh(ctx, accountID)
}

// compile-time interface check.
var _ TokenProvider = (*OpenAITokenProvider)(nil)
