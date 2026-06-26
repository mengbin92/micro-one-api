package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// openAIWSStickyKeyPrefix is the Redis key prefix for cross-process response->channel
// sticky routing. Mirrors sub2api's sticky_session: prefix semantics.
const openAIWSStickyKeyPrefix = "openai_ws_resp:"

// openAIWSStickyTTL is the default binding TTL. A single Codex turn chain rarely
// exceeds an hour, so this bounds memory across replicas.
const openAIWSStickyTTL = time.Hour

// openAIWSStickyStore resolves a previous_response_id to the channel that served
// the prior turn, across processes. The in-memory map is a hot cache; Redis is
// the authoritative cross-replica store. When Redis is unavailable (nil client),
// the store degrades to in-memory only — single-replica deployments still work.
type openAIWSStickyStore struct {
	rdb       *redis.Client
	hotMu     sync.RWMutex
	hot       map[string]openAIWSStickyBinding
	lastSweep time.Time
}

type openAIWSStickyBinding struct {
	channelID int64
	expiresAt time.Time
}

func newOpenAIWSStickyStore(rdb *redis.Client) *openAIWSStickyStore {
	return &openAIWSStickyStore{
		rdb:       rdb,
		hot:       make(map[string]openAIWSStickyBinding, 256),
		lastSweep: time.Now(),
	}
}

// BindResponseChannel stores responseID -> channelID both locally and in Redis.
func (s *openAIWSStickyStore) BindResponseChannel(ctx context.Context, group, responseID string, channelID int64, ttl time.Duration) {
	id := normalizeStickyResponseID(responseID)
	if id == "" || channelID <= 0 {
		return
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	expiresAt := time.Now().Add(ttl)
	key := stickyHotKey(group, id)
	s.hotMu.Lock()
	s.hot[key] = openAIWSStickyBinding{channelID: channelID, expiresAt: expiresAt}
	s.maybeSweepLocked()
	s.hotMu.Unlock()

	if s.rdb == nil {
		return
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = s.rdb.Set(rCtx, stickyRedisKey(group, id), channelID, ttl).Err()
}

// LookupResponseChannel returns the channel bound to responseID. Hot cache is
// checked first; on miss it falls back to Redis and populates the hot cache.
// Returns 0 if not found.
func (s *openAIWSStickyStore) LookupResponseChannel(ctx context.Context, group, responseID string) int64 {
	id := normalizeStickyResponseID(responseID)
	if id == "" {
		return 0
	}
	key := stickyHotKey(group, id)
	now := time.Now()
	s.hotMu.RLock()
	if b, ok := s.hot[key]; ok && now.Before(b.expiresAt) {
		ch := b.channelID
		s.hotMu.RUnlock()
		return ch
	}
	s.hotMu.RUnlock()

	if s.rdb == nil {
		return 0
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	val, err := s.rdb.Get(rCtx, stickyRedisKey(group, id)).Int64()
	if err != nil || val <= 0 {
		return 0
	}
	// Populate hot cache with a shorter local TTL so repeated lookups are fast.
	s.hotMu.Lock()
	s.hot[key] = openAIWSStickyBinding{channelID: val, expiresAt: now.Add(5 * time.Minute)}
	s.maybeSweepLocked()
	s.hotMu.Unlock()
	return val
}

func (s *openAIWSStickyStore) maybeSweepLocked() {
	now := time.Now()
	if now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	scanned := 0
	for k, b := range s.hot {
		if now.After(b.expiresAt) {
			delete(s.hot, k)
		}
		scanned++
		if scanned >= 512 {
			break
		}
	}
}

func normalizeStickyResponseID(responseID string) string {
	return strings.TrimSpace(responseID)
}

func stickyHotKey(group, responseID string) string {
	return fmt.Sprintf("%s:%s", group, responseID)
}

func stickyRedisKey(group, responseID string) string {
	return openAIWSStickyKeyPrefix + group + ":" + responseID
}
