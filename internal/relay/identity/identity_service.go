package identity

import (
	"errors"
	"sync"
	"time"
)

// IdentityService is the entry point used by the subscription adaptors to
// obtain a fingerprint for an account and to apply mimicry. It owns an
// in-process cache of fingerprints keyed by account ID; in a full deployment
// this cache is backed by Redis (key fp:{accountID}) with TTL renewal, but the
// MVP keeps it in-memory so it can be exercised without infrastructure.
type IdentityService struct {
	mu    sync.Mutex
	cache map[int64]fingerprintCacheEntry
	ttl   time.Duration
}

// fingerprintCacheEntry pairs a fingerprint with the time it was cached, so
// entries can be expired lazily on access according to the configured TTL.
type fingerprintCacheEntry struct {
	fp       Fingerprint
	cachedAt time.Time
}

// ErrNoSnapshot is returned by GetOrCreateFingerprint when the account carries
// no usable fingerprint seed (neither a cached snapshot nor a platform
// default).
var ErrNoSnapshot = errors.New("identity: no fingerprint snapshot for account")

// NewIdentityService builds an IdentityService with the given cache TTL. A
// zero TTL keeps entries cached indefinitely.
func NewIdentityService(ttl time.Duration) *IdentityService {
	return &IdentityService{
		cache: make(map[int64]fingerprintCacheEntry),
		ttl:   ttl,
	}
}

// AccountKey identifies the account a fingerprint is resolved for. It mirrors
// the fields the adaptor layer has access to from the selected subscription
// account.
type AccountKey struct {
	ID       int64
	Platform Platform
	Snapshot FingerprintSnapshot // cached snapshot, may be empty
	IsOAuth  bool
}

// GetOrCreateFingerprint returns the cached fingerprint for the account, or
// creates one from the cached snapshot / platform default when none exists or
// the cached entry has exceeded the TTL. The returned fingerprint is stable
// until it expires, so the upstream sees a consistent client identity across
// requests within a TTL window.
func (s *IdentityService) GetOrCreateFingerprint(key AccountKey) (Fingerprint, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.cache[key.ID]; ok {
		// Lazy TTL expiry: if the entry is still fresh, return it; otherwise
		// fall through to regenerate it.
		if s.ttl <= 0 || now.Sub(entry.cachedAt) < s.ttl {
			return entry.fp, nil
		}
	}
	var fp Fingerprint
	if key.Snapshot != "" {
		fp = RestoreFromSnapshot(key.Snapshot, key.Platform)
	} else {
		fp = DefaultFingerprintForPlatform(key.Platform)
	}
	if fp.ClientID == "" {
		return Fingerprint{}, ErrNoSnapshot
	}
	s.cache[key.ID] = fingerprintCacheEntry{fp: fp, cachedAt: now}
	return fp, nil
}

// Invalidate drops the cached fingerprint for an account (e.g. after a token
// refresh or when an account is rotated).
func (s *IdentityService) Invalidate(accountID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, accountID)
}
