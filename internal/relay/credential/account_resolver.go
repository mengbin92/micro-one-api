package credential

import (
	"context"
)

// SubscriptionAccountMetadata is the account-level metadata the relay path
// needs to correctly address a subscription account. It is distinct from
// Channel (which describes an API-key endpoint): a subscription account has
// its own identity, credential record id, upstream account id and cached
// fingerprint, none of which fit on the current Channel struct.
//
// The MVP resolves this via a SubscriptionAccountResolver keyed by the
// channel id that selected the account (the channel-service SelectChannel
// returns a Channel record whose Type is a subscription type); a full
// deployment uses the dedicated SelectSubscriptionAccount RPC (plan §4.6.3).
type SubscriptionAccountMetadata struct {
	// ID is the subscription account's own identifier (NOT the channel id).
	// It keys the credential/identity caches.
	ID int64
	// AccessToken is an optional in-memory fallback token used by the MVP when
	// a dedicated credential store is not yet wired.
	AccessToken string
	// AccountID is the upstream account id (e.g. chatgpt-account-id for Codex,
	// or the UUID used in Claude metadata.user_id).
	AccountID string
	// Platform is "codex" | "claude".
	Platform Platform
	// AccountType is the subscription credential type ("oauth" |
	// "setup_token"). It controls whether identity mimicry is applied.
	AccountType string
	// Fingerprint is the cached fingerprint snapshot (opaque blob).
	Fingerprint string
	// GroupID is the quota/billing group the account belongs to.
	GroupID string
}

// SubscriptionAccountResolver resolves the subscription-account metadata for a
// channel that was selected as a subscription type. Implementations query the
// channel/identity service (gRPC in production, NoopAccountResolver for tests).
//
// The channel id passed in is the id returned by SelectChannel; the resolver
// maps it to the underlying subscription account.
type SubscriptionAccountResolver interface {
	// Resolve returns the subscription account metadata for the given channel
	// id, or ErrAccountNotFound if the channel is not backed by a subscription
	// account.
	Resolve(ctx context.Context, channelID int64) (*SubscriptionAccountMetadata, error)
}

// NoopAccountResolver is an in-memory SubscriptionAccountResolver for tests
// and initial bootstrapping. It maps channel ids to account metadata entries
// seeded via SeedByChannel.
type NoopAccountResolver struct {
	byChannel map[int64]*SubscriptionAccountMetadata
}

// NewNoopAccountResolver creates an empty in-memory resolver.
func NewNoopAccountResolver() *NoopAccountResolver {
	return &NoopAccountResolver{byChannel: make(map[int64]*SubscriptionAccountMetadata)}
}

// SeedByChannel registers subscription-account metadata for a channel id.
func (n *NoopAccountResolver) SeedByChannel(channelID int64, meta *SubscriptionAccountMetadata) {
	n.byChannel[channelID] = meta
}

// Resolve implements SubscriptionAccountResolver.
func (n *NoopAccountResolver) Resolve(_ context.Context, channelID int64) (*SubscriptionAccountMetadata, error) {
	meta, ok := n.byChannel[channelID]
	if !ok || meta == nil {
		return nil, ErrAccountNotFound
	}
	cp := *meta
	return &cp, nil
}

// compile-time interface check.
var _ SubscriptionAccountResolver = (*NoopAccountResolver)(nil)
