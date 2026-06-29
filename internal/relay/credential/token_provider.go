// Package credential provides the OAuth token-management layer for
// subscription accounts (Codex / Claude), ported conceptually from sub2api's
// token_refresh_service.go and new-api's codex_credential_refresh_task.go.
//
// MVP scope (plan §十): a TokenProvider returns a valid access token for an
// account, refreshing on demand when the cached token is about to expire.
// Background refresh is provided by RefreshTask. Caching lives in Redis in a
// full deployment (key token:{platform}:{accountID}); the MVP ships an
// in-process implementation and a Redis-backed implementation so the server
// can run with or without Redis.
package credential

import (
	"context"
	"errors"
	"time"
)

// Platform identifies a subscription-account platform. It mirrors
// identity.Platform but is duplicated here to keep the credential package free
// of an identity dependency (the credential layer only needs the string tag).
type Platform string

const (
	PlatformCodex  Platform = "codex"
	PlatformClaude Platform = "claude"
)

// AccountCredentials holds the OAuth credentials for a subscription account.
// It is the in-memory view of the encrypted `credentials` blob stored in the
// SubscriptionAccount record.
type AccountCredentials struct {
	AccountID    string // upstream account id (e.g. chatgpt-account-id)
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time // access-token expiry
	ClientID     string    // OAuth client_id (for refresh)
	RefreshURL   string    // token endpoint URL
}

// TokenProvider returns a valid access token for an account, refreshing when
// necessary.
type TokenProvider interface {
	// GetAccessToken returns a non-expired access token for the account,
	// refreshing transparently when the cached token is within RefreshSkew of
	// expiry. Implementations MUST be safe for concurrent use.
	GetAccessToken(ctx context.Context, accountID int64) (string, error)
	// Refresh forces a token refresh for the account regardless of expiry.
	Refresh(ctx context.Context, accountID int64) error
}

// AccountLookup resolves the credentials for an account. The credential layer
// does not own account storage; it queries the channel/identity service via
// this interface. Implementations translate the gRPC reply into
// AccountCredentials.
type AccountLookup interface {
	// Lookup returns the credentials for the account. The returned
	// AccountCredentials is a snapshot; mutations (e.g. a refreshed token) are
	// persisted via Store.
	Lookup(ctx context.Context, accountID int64) (*AccountCredentials, error)
	// Store persists updated credentials (new access/refresh token + expiry).
	Store(ctx context.Context, accountID int64, creds *AccountCredentials) error
}

// Sentinel errors.
var (
	// ErrAccountNotFound is returned when no credentials exist for the account.
	ErrAccountNotFound = errors.New("credential: account not found")
	// ErrNoRefreshToken is returned when a refresh is required but the account
	// has no refresh_token.
	ErrNoRefreshToken = errors.New("credential: no refresh_token available")
	// ErrRefreshFailed is returned when the upstream token endpoint rejected
	// the refresh attempt.
	ErrRefreshFailed = errors.New("credential: token refresh failed")
	// ErrNotConfigured is returned when no AccountLookup is wired.
	ErrNotConfigured = errors.New("credential: account lookup is not configured")
)

// RefreshSkew is how long before expiry a token is considered stale and
// proactively refreshed. Matches sub2api's 3-minute skew.
const RefreshSkew = 3 * time.Minute
