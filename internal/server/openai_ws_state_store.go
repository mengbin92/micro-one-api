package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	relaybiz "micro-one-api/internal/biz"
)

// openAIWSStickyKeyPrefix is the Redis key prefix for cross-process response->channel
// sticky routing. Mirrors sub2api's sticky_session: prefix semantics.
const openAIWSStickyKeyPrefix = "openai_ws_resp:"
const openAIWSSessionStickyKeyPrefix = "openai_ws_session:"

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
	source    openAIWSStickySource
	expiresAt time.Time
}

type openAIWSStickySource struct {
	kind relaybiz.UpstreamRouteKind
	id   int64
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
	s.bindResponseSource(ctx, group, responseID, openAIWSStickySource{kind: relaybiz.UpstreamRouteChannel, id: channelID}, ttl)
}

func (s *openAIWSStickyStore) BindResponseRoute(ctx context.Context, group, responseID string, channel *relaybiz.Channel, ttl time.Duration) {
	s.bindResponseSource(ctx, group, responseID, stickySourceForChannel(channel), ttl)
}

func (s *openAIWSStickyStore) bindResponseSource(ctx context.Context, group, responseID string, source openAIWSStickySource, ttl time.Duration) {
	id := normalizeStickyResponseID(responseID)
	if id == "" || source.id <= 0 || source.kind == 0 {
		return
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	expiresAt := time.Now().Add(ttl)
	key := stickyHotKey(openAIWSStickyKeyPrefix, group, id)
	s.hotMu.Lock()
	s.hot[key] = openAIWSStickyBinding{source: source, expiresAt: expiresAt}
	s.maybeSweepLocked()
	s.hotMu.Unlock()

	if s.rdb == nil {
		return
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = s.rdb.Set(rCtx, stickyRedisKey(group, id), encodeStickySource(source), ttl).Err()
}

// LookupResponseChannel returns the channel bound to responseID. Hot cache is
// checked first; on miss it falls back to Redis and populates the hot cache.
// Returns 0 if not found.
func (s *openAIWSStickyStore) LookupResponseChannel(ctx context.Context, group, responseID string) int64 {
	source := s.LookupResponseRoute(ctx, group, responseID)
	if source.kind != relaybiz.UpstreamRouteChannel {
		return 0
	}
	return source.id
}

func (s *openAIWSStickyStore) LookupResponseRoute(ctx context.Context, group, responseID string) openAIWSStickySource {
	id := normalizeStickyResponseID(responseID)
	if id == "" {
		return openAIWSStickySource{}
	}
	key := stickyHotKey(openAIWSStickyKeyPrefix, group, id)
	now := time.Now()
	s.hotMu.RLock()
	if b, ok := s.hot[key]; ok && now.Before(b.expiresAt) {
		source := b.source
		s.hotMu.RUnlock()
		return source
	}
	s.hotMu.RUnlock()

	if s.rdb == nil {
		return openAIWSStickySource{}
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	val, err := s.rdb.Get(rCtx, stickyRedisKey(group, id)).Result()
	source, ok := decodeStickySource(val, relaybiz.UpstreamRouteChannel)
	if err != nil || !ok {
		return openAIWSStickySource{}
	}
	// Populate hot cache with a shorter local TTL so repeated lookups are fast.
	s.hotMu.Lock()
	s.hot[key] = openAIWSStickyBinding{source: source, expiresAt: now.Add(5 * time.Minute)}
	s.maybeSweepLocked()
	s.hotMu.Unlock()
	return source
}

func (s *openAIWSStickyStore) BindSessionChannel(ctx context.Context, group, sessionHash string, channelID int64, ttl time.Duration) {
	// SessionAccountStore uses this method exclusively for subscription-account
	// stickiness, so its ID belongs to the subscription namespace.
	s.bindSessionSource(ctx, group, sessionHash, openAIWSStickySource{kind: relaybiz.UpstreamRouteSubscription, id: channelID}, ttl)
}

func (s *openAIWSStickyStore) BindSessionRoute(ctx context.Context, group, sessionHash string, channel *relaybiz.Channel, ttl time.Duration) {
	s.bindSessionSource(ctx, group, sessionHash, stickySourceForChannel(channel), ttl)
}

func (s *openAIWSStickyStore) bindSessionSource(ctx context.Context, group, sessionHash string, source openAIWSStickySource, ttl time.Duration) {
	id := normalizeStickyResponseID(sessionHash)
	if id == "" || source.id <= 0 || source.kind == 0 {
		return
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	expiresAt := time.Now().Add(ttl)
	key := stickyHotKey(openAIWSSessionStickyKeyPrefix, group, id)
	s.hotMu.Lock()
	s.hot[key] = openAIWSStickyBinding{source: source, expiresAt: expiresAt}
	s.maybeSweepLocked()
	s.hotMu.Unlock()

	if s.rdb == nil {
		return
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = s.rdb.Set(rCtx, stickySessionRedisKey(group, id), encodeStickySource(source), ttl).Err()
}

func (s *openAIWSStickyStore) LookupSessionChannel(ctx context.Context, group, sessionHash string) int64 {
	source := s.LookupSessionRoute(ctx, group, sessionHash)
	if source.kind != relaybiz.UpstreamRouteSubscription {
		return 0
	}
	return source.id
}

func (s *openAIWSStickyStore) LookupSessionRoute(ctx context.Context, group, sessionHash string) openAIWSStickySource {
	id := normalizeStickyResponseID(sessionHash)
	if id == "" {
		return openAIWSStickySource{}
	}
	key := stickyHotKey(openAIWSSessionStickyKeyPrefix, group, id)
	now := time.Now()
	s.hotMu.RLock()
	if b, ok := s.hot[key]; ok && now.Before(b.expiresAt) {
		source := b.source
		s.hotMu.RUnlock()
		return source
	}
	s.hotMu.RUnlock()

	if s.rdb == nil {
		return openAIWSStickySource{}
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	val, err := s.rdb.Get(rCtx, stickySessionRedisKey(group, id)).Result()
	// v0.11.0 review L7: pre-v0.11.0 session bindings stored bare channel IDs.
	// Default to channel when decoding legacy bare-integer values so rolling
	// upgrades do not misroute to subscription accounts.
	source, ok := decodeStickySource(val, relaybiz.UpstreamRouteChannel)
	if err != nil || !ok {
		return openAIWSStickySource{}
	}
	s.hotMu.Lock()
	s.hot[key] = openAIWSStickyBinding{source: source, expiresAt: now.Add(5 * time.Minute)}
	s.maybeSweepLocked()
	s.hotMu.Unlock()
	return source
}

func (s *openAIWSStickyStore) RefreshSessionTTL(ctx context.Context, group, sessionHash string, ttl time.Duration) bool {
	id := normalizeStickyResponseID(sessionHash)
	if id == "" {
		return false
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	key := stickyHotKey(openAIWSSessionStickyKeyPrefix, group, id)
	now := time.Now()
	refreshed := false
	s.hotMu.Lock()
	if b, ok := s.hot[key]; ok && now.Before(b.expiresAt) {
		b.expiresAt = now.Add(ttl)
		s.hot[key] = b
		refreshed = true
	}
	s.hotMu.Unlock()

	if s.rdb == nil {
		return refreshed
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if ok, err := s.rdb.Expire(rCtx, stickySessionRedisKey(group, id), ttl).Result(); err == nil && ok {
		return true
	}
	return refreshed
}

func (s *openAIWSStickyStore) DeleteSession(ctx context.Context, group, sessionHash string) {
	id := normalizeStickyResponseID(sessionHash)
	if id == "" {
		return
	}
	key := stickyHotKey(openAIWSSessionStickyKeyPrefix, group, id)
	s.hotMu.Lock()
	delete(s.hot, key)
	s.hotMu.Unlock()

	if s.rdb == nil {
		return
	}
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = s.rdb.Del(rCtx, stickySessionRedisKey(group, id)).Err()
}

func (s *openAIWSStickyStore) maybeSweepLocked() {
	now := time.Now()
	if now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	// Full sweep of expired entries. Throttled to once per minute, so an O(n)
	// pass under the lock is cheap; unlike a bounded scan it guarantees every
	// expired entry is removed regardless of Go's randomized map iteration order
	// (a 512-entry cap could inspect only live keys and never reach the expired
	// ones, letting the map grow unbounded).
	for k, b := range s.hot {
		if now.After(b.expiresAt) {
			delete(s.hot, k)
		}
	}
}

func normalizeStickyResponseID(responseID string) string {
	return strings.TrimSpace(responseID)
}

func stickyHotKey(prefix, group, id string) string {
	return fmt.Sprintf("%s%s:%s", prefix, group, id)
}

func stickyRedisKey(group, responseID string) string {
	return openAIWSStickyKeyPrefix + group + ":" + responseID
}

func stickySessionRedisKey(group, sessionHash string) string {
	return openAIWSSessionStickyKeyPrefix + group + ":" + sessionHash
}

func stickySourceForChannel(channel *relaybiz.Channel) openAIWSStickySource {
	identity := relaybiz.RoutingSourceIdentityForChannel(channel)
	return openAIWSStickySource{kind: identity.Kind, id: identity.ID}
}

func encodeStickySource(source openAIWSStickySource) string {
	return source.kind.String() + ":" + strconv.FormatInt(source.id, 10)
}

func decodeStickySource(value string, legacyKind relaybiz.UpstreamRouteKind) (openAIWSStickySource, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return openAIWSStickySource{}, false
	}
	kind := legacyKind
	idText := value
	if prefix, rest, found := strings.Cut(value, ":"); found {
		idText = rest
		switch prefix {
		case relaybiz.UpstreamRouteChannel.String():
			kind = relaybiz.UpstreamRouteChannel
		case relaybiz.UpstreamRouteSubscription.String():
			kind = relaybiz.UpstreamRouteSubscription
		default:
			return openAIWSStickySource{}, false
		}
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || kind == 0 {
		return openAIWSStickySource{}, false
	}
	return openAIWSStickySource{kind: kind, id: id}, true
}
