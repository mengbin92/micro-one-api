// Package credential provides the OAuth token-management layer for
// subscription accounts (Codex / Claude), ported conceptually from sub2api's
// token_refresh_service.go and new-api's codex_credential_refresh_task.go.
//
// MVP scope (plan §十): a TokenProvider returns a valid access token for an
// account, refreshing on demand when the cached token is about to expire.
// Background refresh and pending persistence are provided by RefreshTask.
// Distributed mode uses Redis admission and a durable refresh claim. A forced
// restart during OAuth or while writes are pending can require reauthorization;
// an uncertain rotation is never automatically replayed by another replica.
package credential

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Platform identifies a subscription-account platform. It mirrors
// identity.Platform but is duplicated here to keep the credential package free
// of an identity dependency (the credential layer only needs the string tag).
type Platform string

const (
	PlatformCodex   Platform = "codex"
	PlatformClaude  Platform = "claude"
	PlatformZhipu   Platform = "zhipu"   // GLM Coding Plan (static key)
	PlatformMinimax Platform = "minimax" // MiniMax Coding Plan (static key)
	PlatformKimi    Platform = "kimi"    // Kimi For Coding (OAuth refresh)
)

// AccountCredentials holds the OAuth credentials for a subscription account.
// It is the in-memory view of the encrypted `credentials` blob stored in the
// SubscriptionAccount record.
type AccountCredentials struct {
	Revision       int64  // durable revision used by Store for compare-and-swap
	RefreshPending bool   // durable claim; only its owner may complete this rotation
	AccountID      string // upstream account id (e.g. chatgpt-account-id)
	AccessToken    string
	RefreshToken   string
	ExpiresAt      time.Time // access-token expiry
	ClientID       string    // OAuth client_id (for refresh)
	RefreshURL     string    // token endpoint URL
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

// AuthoritativeTokenProvider requires OAuth adaptors to bypass tokens carried
// in selection snapshots and resolve shared credentials for each execution.
type AuthoritativeTokenProvider interface {
	RequiresAuthoritativeLookup() bool
}

// TokenInvalidator is optionally implemented by providers that cache tokens
// and can evict a single account after a background refresh completes.
type TokenInvalidator interface {
	Invalidate(accountID int64)
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
	// Store conditionally persists credentials at Revision, then updates Revision
	// on success. Conflicts return ErrCredentialConflict; retries are idempotent.
	Store(ctx context.Context, accountID int64, creds *AccountCredentials) error
}

type PendingPersister interface {
	PersistPending(context.Context, time.Duration) []int64
}

// RefreshClaimer advances Revision and marks a rotation pending atomically.
// A lost claim response is not replayable: callers must not contact OAuth.
type RefreshClaimer interface {
	ClaimRefresh(context.Context, int64, *AccountCredentials) error
}

// RefreshCoordinator provides short-lived admission only. RefreshClaimer is
// still required because lease expiry cannot fence an external OAuth server.
type RefreshCoordinator interface {
	Acquire(context.Context, int64) (RefreshLease, error)
}

type RefreshLease interface {
	Check(context.Context) error
	Release(context.Context)
}

// Sentinel errors.
var (
	ErrRefreshBusy                  = errors.New("credential: refresh owned by another replica")
	ErrRefreshUncertain             = errors.New("credential: rotation pending; wait for owner persistence or reauthorize with a new refresh token")
	ErrCoordinationUnavailable      = errors.New("credential: refresh coordination unavailable")
	ErrCredentialPersistencePending = errors.New("credential: rotation awaiting persistence")
	ErrCredentialConflict           = errors.New("credential: revision conflict; reload authorization")
	// ErrAccountNotFound is returned when no credentials exist for the account.
	ErrAccountNotFound = errors.New("credential: account not found")
	// ErrNoRefreshToken is returned when a refresh is required but the account
	// has no refresh_token.
	ErrNoRefreshToken = errors.New("credential: no refresh_token available")
	// ErrRefreshFailed is returned when the upstream token endpoint rejected
	// the refresh attempt.
	ErrRefreshFailed = errors.New("credential: token refresh failed")
	// ErrInvalidGrant is returned when the upstream token endpoint reports an
	// invalid_grant error, meaning the refresh token is permanently revoked or
	// expired and no amount of retrying will recover it. It wraps ErrRefreshFailed
	// so callers checking for a generic refresh failure still match. Callers
	// should use errors.Is(err, ErrInvalidGrant) to stop retrying the account
	// (code-review 2026-07-30 domain-H2).
	ErrInvalidGrant = fmt.Errorf("credential: %w: invalid grant", ErrRefreshFailed)
	// ErrNotConfigured is returned when no AccountLookup is wired.
	ErrNotConfigured = errors.New("credential: account lookup is not configured")
)

// RefreshSkew is how long before expiry a token is considered stale and
// proactively refreshed. Matches sub2api's 3-minute skew.
const RefreshSkew = 3 * time.Minute
