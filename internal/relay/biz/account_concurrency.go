package biz

import "sync"

// AccountConcurrencyLimiter caps the number of in-flight relay requests per
// subscription account inside a single relay-gateway process. It is the
// enforcement side of SubscriptionAccount.Concurrency: channel-service owns the
// configured limit, the gateway holds a slot for the lifetime of each upstream
// call (including the full duration of a streamed response) so a single account
// is never saturated into upstream 429/529s.
//
// This is intentionally in-process only. A multi-replica deployment would need a
// Redis-backed counter to share the limit across replicas (mirrored on the
// runtime blocker); that is deferred. Until then each replica enforces its own
// share of the limit, which still bounds per-account fan-out.
type AccountConcurrencyLimiter struct {
	mu       sync.Mutex
	inflight map[int64]int32
}

// NewAccountConcurrencyLimiter builds an empty limiter.
func NewAccountConcurrencyLimiter() *AccountConcurrencyLimiter {
	return &AccountConcurrencyLimiter{inflight: make(map[int64]int32)}
}

// TryAcquire reserves a concurrency slot for accountID. It returns a release
// function and true when a slot was granted, or (nil, false) when the account is
// already at its limit. The release function is idempotent and safe to call from
// any goroutine.
//
// A non-positive limit (or a nil limiter / non-positive accountID) means
// "unlimited": TryAcquire always succeeds and returns a no-op release so callers
// need not special-case the unlimited path.
func (l *AccountConcurrencyLimiter) TryAcquire(accountID int64, limit int32) (func(), bool) {
	if l == nil || limit <= 0 || accountID <= 0 {
		return func() {}, true
	}
	l.mu.Lock()
	if l.inflight == nil {
		l.inflight = make(map[int64]int32)
	}
	if l.inflight[accountID] >= limit {
		l.mu.Unlock()
		return nil, false
	}
	l.inflight[accountID]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.inflight[accountID] <= 1 {
				delete(l.inflight, accountID)
			} else {
				l.inflight[accountID]--
			}
			l.mu.Unlock()
		})
	}, true
}

// Inflight returns the current in-flight count for accountID. Intended for
// tests and observability, not the hot path.
func (l *AccountConcurrencyLimiter) Inflight(accountID int64) int32 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight[accountID]
}
