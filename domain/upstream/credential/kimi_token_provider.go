package credential

import (
	"context"
	"net/http"
	"strings"
)

// KimiTokenProvider implements TokenProvider for Kimi For Coding
// subscription accounts. Kimi uses the OAuth refresh-token grant against
// its own token endpoint, so the provider is structurally identical to the
// Claude and OpenAI providers: it embeds baseTokenProvider and only
// contributes the platform-specific client ID and refresh URL.
//
// See docs/design/cn-subscription-accounts-roadmap.md (P3).
//
// The client ID and refresh URL are exported vars (not consts) so they can be
// overridden at process start from config — Kimi's CLI endpoint is not part of
// a stable public API and may change between CLI releases. The wire layer
// (cmd/relay-gateway/wire.go) applies config overrides via
// NewKimiTokenProviderWithHTTPClient + direct var assignment before the
// provider is enrolled in the RefreshTask.
type KimiTokenProvider struct {
	baseTokenProvider
}

const (
	KimiDeviceOAuthHost = "https://auth.kimi.com"
	KimiDeviceTokenURL  = KimiDeviceOAuthHost + "/api/oauth/token"
	KimiCodingBaseURL   = "https://api.kimi.com/coding"
)

// KimiOAuthClientID is the operator-configured OAuth client ID used for refresh.
var KimiOAuthClientID string

// KimiTokenRefreshURL is the OAuth token endpoint the Kimi CLI refreshes
// against. Overridable for the same reason as KimiOAuthClientID.
// #nosec G101 -- public OAuth endpoint pattern, not a credential.
var KimiTokenRefreshURL = KimiDeviceTokenURL

// RegisteredKimiDeviceClientID rejects an implicit or first-party registration.
func RegisteredKimiDeviceClientID(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if strings.EqualFold(clientID, "17e5f671-d194-4dfb-9706-5516cb48c098") || strings.EqualFold(clientID, "kimi-coding-cli") {
		return ""
	}
	return clientID
}

// NewKimiTokenProvider builds a Kimi token provider backed by the given
// account lookup. The refresh HTTP client defaults to 30s; pass a custom
// client via NewKimiTokenProviderWithHTTPClient for tests.
func NewKimiTokenProvider(lookup AccountLookup) *KimiTokenProvider {
	return NewKimiTokenProviderWithHTTPClient(lookup, defaultRefreshHTTPClient())
}

// NewKimiTokenProviderWithHTTPClient is the testable constructor. It snapshots
// the current KimiOAuthClientID / KimiTokenRefreshURL at construction time so
// later var overrides do not retroactively change a running provider (the
// wire layer constructs the provider once at boot).
func NewKimiTokenProviderWithHTTPClient(lookup AccountLookup, hc *http.Client) *KimiTokenProvider {
	return &KimiTokenProvider{
		baseTokenProvider: newPlatformTokenProvider(lookup, hc, KimiOAuthClientID, KimiTokenRefreshURL, "kimi"),
	}
}

// GetAccessToken returns a valid Kimi OAuth access token, refreshing
// transparently when the cached token is within RefreshSkew of expiry.
func (p *KimiTokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	return p.baseTokenProvider.GetAccessToken(ctx, accountID)
}

// Refresh forces a token refresh for the account.
func (p *KimiTokenProvider) Refresh(ctx context.Context, accountID int64) error {
	return p.baseTokenProvider.Refresh(ctx, accountID)
}

// compile-time interface check.
var _ TokenProvider = (*KimiTokenProvider)(nil)
