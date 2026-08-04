package server

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const subscriptionSessionWindowKeyPrefix = "subscription_account:session_window:"
const subscriptionSessionWindowDedupePrefix = "subscription_account:session_window_dedupe:"

// subscriptionSessionWindowStore enforces a per-session spend ceiling against
// a shared Redis key. The in-memory maps are the fail-open fallback (code-
// review M8): when Redis is unreachable the window is enforced per-replica, so
// a multi-replica deployment can temporarily overspend the session window
// (N × the single-replica view) until Redis recovers. Availability over strict
// enforcement; the same trade-off as the account concurrency limiter (H11).
type subscriptionSessionWindowStore struct {
	rdb       *redis.Client
	mu        sync.Mutex
	usage     map[string]sessionWindowUsage
	seen      map[string]time.Time
	lastSweep time.Time
}

type sessionWindowUsage struct {
	used      float64
	expiresAt time.Time
}

func newSubscriptionSessionWindowStore(rdb *redis.Client) *subscriptionSessionWindowStore {
	return &subscriptionSessionWindowStore{
		rdb:       rdb,
		usage:     make(map[string]sessionWindowUsage),
		seen:      make(map[string]time.Time),
		lastSweep: time.Now(),
	}
}

func (s *subscriptionSessionWindowStore) Exceeded(ctx context.Context, group, sessionHash string, accountID int64, limitUSD float64) bool {
	if s == nil || accountID <= 0 || limitUSD <= 0 || strings.TrimSpace(sessionHash) == "" {
		return false
	}
	key := sessionWindowKey(group, sessionHash, accountID)
	if s.rdb != nil {
		rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		used, err := s.rdb.Get(rCtx, key).Float64()
		if err == nil {
			return used >= limitUSD
		}
		if err != redis.Nil {
			return s.memoryExceeded(key, limitUSD)
		}
	}
	return s.memoryExceeded(key, limitUSD)
}

func (s *subscriptionSessionWindowStore) RecordUsage(ctx context.Context, group, sessionHash string, accountID int64, reservationID string, costUSD float64, ttl time.Duration) {
	if s == nil || accountID <= 0 || costUSD <= 0 || strings.TrimSpace(sessionHash) == "" {
		return
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	key := sessionWindowKey(group, sessionHash, accountID)
	dedupeKey := sessionWindowDedupeKey(group, sessionHash, accountID, reservationID)
	if s.rdb != nil {
		rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		dedupeOK := true
		if dedupeKey != "" {
			set, err := s.rdb.SetNX(rCtx, dedupeKey, "1", ttl).Result()
			if err == nil && !set {
				cancel()
				return
			}
			dedupeOK = err == nil
		}
		if dedupeOK {
			if _, incrErr := s.rdb.IncrByFloat(rCtx, key, costUSD).Result(); incrErr == nil {
				_ = s.rdb.Expire(rCtx, key, ttl).Err()
				cancel()
				return
			}
		}
		cancel()
	}
	s.recordMemory(key, dedupeKey, costUSD, ttl)
}

func (s *subscriptionSessionWindowStore) memoryExceeded(key string, limitUSD float64) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	usage, ok := s.usage[key]
	if !ok || !usage.expiresAt.After(now) {
		delete(s.usage, key)
		return false
	}
	return usage.used >= limitUSD
}

func (s *subscriptionSessionWindowStore) recordMemory(key, dedupeKey string, costUSD float64, ttl time.Duration) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	if dedupeKey != "" {
		if seenUntil, ok := s.seen[dedupeKey]; ok && seenUntil.After(now) {
			return
		}
		s.seen[dedupeKey] = expiresAt
	}
	usage := s.usage[key]
	if !usage.expiresAt.After(now) {
		usage = sessionWindowUsage{}
	}
	usage.used += costUSD
	usage.expiresAt = expiresAt
	s.usage[key] = usage
	s.sweepLocked(now)
}

func (s *subscriptionSessionWindowStore) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	for key, usage := range s.usage {
		if !usage.expiresAt.After(now) {
			delete(s.usage, key)
		}
	}
	for key, expiresAt := range s.seen {
		if !expiresAt.After(now) {
			delete(s.seen, key)
		}
	}
}

func sessionWindowKey(group, sessionHash string, accountID int64) string {
	return subscriptionSessionWindowKeyPrefix + strings.TrimSpace(group) + ":" + normalizeStickyResponseID(sessionHash) + ":" + strconv.FormatInt(accountID, 10)
}

func sessionWindowDedupeKey(group, sessionHash string, accountID int64, reservationID string) string {
	reservationID = strings.TrimSpace(reservationID)
	if reservationID == "" {
		return ""
	}
	return subscriptionSessionWindowDedupePrefix + strings.TrimSpace(group) + ":" + normalizeStickyResponseID(sessionHash) + ":" + strconv.FormatInt(accountID, 10) + ":" + reservationID
}
