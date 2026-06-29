package credential

import (
	"context"
	"sync"
	"time"
)

// NoopAccountLookup is an AccountLookup that returns the credentials it was
// seeded with. It exists so the gateway can boot and exercise the adaptor
// path end-to-end before the channel-service RPC for subscription accounts is
// implemented. In a full deployment this is replaced by a gRPC-backed lookup.
//
// It is safe for concurrent use.
type NoopAccountLookup struct {
	mu     sync.RWMutex
	byID   map[int64]*AccountCredentials
	plat   map[int64]Platform
	expiry map[int64]time.Time
}

// NewNoopAccountLookup creates an empty in-memory account lookup.
func NewNoopAccountLookup() *NoopAccountLookup {
	return &NoopAccountLookup{
		byID:   make(map[int64]*AccountCredentials),
		plat:   make(map[int64]Platform),
		expiry: make(map[int64]time.Time),
	}
}

// Seed inserts (or replaces) the credentials for an account. Primarily for
// tests and initial bootstrapping.
func (n *NoopAccountLookup) Seed(id int64, platform Platform, creds *AccountCredentials) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.byID[id] = creds
	n.plat[id] = platform
	if creds != nil {
		n.expiry[id] = creds.ExpiresAt
	}
}

// Lookup implements AccountLookup.
func (n *NoopAccountLookup) Lookup(_ context.Context, id int64) (*AccountCredentials, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	c, ok := n.byID[id]
	if !ok || c == nil {
		return nil, ErrAccountNotFound
	}
	cp := *c
	return &cp, nil
}

// Store implements AccountLookup.
func (n *NoopAccountLookup) Store(_ context.Context, id int64, creds *AccountCredentials) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := *creds
	n.byID[id] = &cp
	if creds != nil {
		n.expiry[id] = creds.ExpiresAt
	}
	return nil
}

// PlatformOf returns the platform tag for an account, or "" when unknown.
func (n *NoopAccountLookup) PlatformOf(id int64) Platform {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.plat[id]
}

// ExpiringSoon implements ExpiringScanner: it returns the IDs of seeded
// accounts whose token expires within `within`.
func (n *NoopAccountLookup) ExpiringSoon(_ context.Context, within time.Duration) ([]int64, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	threshold := time.Now().Add(within)
	var ids []int64
	for id, exp := range n.expiry {
		if !exp.After(threshold) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// compile-time interface checks.
var (
	_ AccountLookup   = (*NoopAccountLookup)(nil)
	_ ExpiringScanner = (*NoopAccountLookup)(nil)
)
