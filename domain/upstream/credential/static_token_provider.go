package credential

import (
	"context"
	"fmt"
)

// StaticTokenProvider implements TokenProvider for subscription accounts that
// authenticate with a long-lived API key (no refresh token, no expiry). It is
// the credential shape used by the domestic "Coding Plan" vendors that
// publish Anthropic-compatible endpoints but do not run an OAuth refresh
// flow: 智谱 GLM Coding Plan and MiniMax Coding Plan.
//
// See docs/design/cn-subscription-accounts-roadmap.md (P1/P2).
//
// Semantics:
//   - GetAccessToken returns the access_token stored on the account (the
//     vendor-issued Coding Plan key). The stored key is assumed non-expiring,
//     so the provider neither caches nor refreshes; it reads from the
//     AccountLookup on every call (the lookup is cheap and goes through the
//     same gRPC/Redis path the other providers use).
//   - Refresh is a no-op: there is no refresh token to exchange. It returns
//     nil so callers that call Refresh defensively (e.g. after a transient
//     401) do not mark the account broken for a refresh failure.
//   - A persistent 401/403 from the upstream is the signal that the static
//     key is actually invalid; that path is handled by the relay's
//     runtime-blocker / SetSubscriptionAccountError chain keyed on the HTTP
//     status, not by this provider.
type StaticTokenProvider struct {
	lookup AccountLookup
}

// NewStaticTokenProvider builds a static (API-key) token provider backed by
// the given account lookup. One shared instance serves all static platforms
// (zhipu / minimax); the platform tag is carried on the account record, not
// on the provider.
func NewStaticTokenProvider(lookup AccountLookup) *StaticTokenProvider {
	return &StaticTokenProvider{lookup: lookup}
}

// GetAccessToken returns the stored access token for the account. The token
// is treated as non-expiring; if the account has no access token the provider
// surfaces ErrNoRefreshToken (the account cannot serve requests until it is
// re-provisioned, mirroring the OAuth provider's "no refresh token" branch).
func (p *StaticTokenProvider) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	if p == nil || p.lookup == nil {
		return "", ErrNotConfigured
	}
	creds, err := p.lookup.Lookup(ctx, accountID)
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", ErrAccountNotFound
	}
	if creds.AccessToken == "" {
		return "", fmt.Errorf("%w: static account has no access_token", ErrNoRefreshToken)
	}
	return creds.AccessToken, nil
}

// Refresh is a no-op for static-key accounts: there is no refresh token and
// the key does not expire. Returning nil keeps the account schedulable; a
// genuinely invalid key is surfaced via the upstream 401/403 runtime-block
// path rather than the refresh path.
func (p *StaticTokenProvider) Refresh(_ context.Context, _ int64) error {
	return nil
}

// Invalidate is a no-op: there is no in-process token cache to evict. The
// provider re-reads the stored key on every GetAccessToken, so an operator
// rotating the key via the channel-service admin RPC takes effect on the
// next request without any cache invalidation here.
func (p *StaticTokenProvider) Invalidate(_ int64) {}

// compile-time interface checks.
var (
	_ TokenProvider    = (*StaticTokenProvider)(nil)
	_ TokenInvalidator = (*StaticTokenProvider)(nil)
)
