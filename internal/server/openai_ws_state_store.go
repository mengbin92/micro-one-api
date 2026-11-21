package server

import (
	"context"
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
// the prior turn. With a Redis client, a failed lookup is a routing miss.
// Single-process deployments without Redis keep bindings in memory.
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
	if rdb != nil {
		return &openAIWSStickyStore{rdb: rdb}
	}
	return &openAIWSStickyStore{
		hot:       make(map[string]openAIWSStickyBinding, 256),
		lastSweep: time.Now(),
	}
}

// BindResponseChannel records the channel that served the response.
func (s *openAIWSStickyStore) BindResponseChannel(ctx context.Context, group, responseID string, channelID int64, ttl time.Duration) {
	s.bindSource(ctx, openAIWSStickyKeyPrefix, group, responseID, openAIWSStickySource{kind: relaybiz.UpstreamRouteChannel, id: channelID}, ttl)
}

func (s *openAIWSStickyStore) BindResponseRoute(ctx context.Context, group, responseID string, channel *relaybiz.Channel, ttl time.Duration) {
	s.bindSource(ctx, openAIWSStickyKeyPrefix, group, responseID, stickySourceForChannel(channel), ttl)
}

func (s *openAIWSStickyStore) bindSource(ctx context.Context, prefix, group, value string, source openAIWSStickySource, ttl time.Duration) {
	id := normalizeStickyResponseID(value)
	if id == "" || source.id <= 0 || source.kind == 0 {
		return
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	key := stickyKey(prefix, group, id)
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = s.rdb.Set(ctx, key, encodeStickySource(source), ttl).Err()
		return
	}
	s.hotMu.Lock()
	defer s.hotMu.Unlock()
	s.hot[key] = openAIWSStickyBinding{source: source, expiresAt: time.Now().Add(ttl)}
	s.maybeSweepLocked()
}

func (s *openAIWSStickyStore) LookupResponseChannel(ctx context.Context, group, responseID string) int64 {
	source := s.LookupResponseRoute(ctx, group, responseID)
	if source.kind != relaybiz.UpstreamRouteChannel {
		return 0
	}
	return source.id
}

func (s *openAIWSStickyStore) LookupResponseRoute(ctx context.Context, group, responseID string) openAIWSStickySource {
	return s.lookupSource(ctx, openAIWSStickyKeyPrefix, group, responseID)
}

func (s *openAIWSStickyStore) lookupSource(ctx context.Context, prefix, group, value string) openAIWSStickySource {
	id := normalizeStickyResponseID(value)
	if id == "" {
		return openAIWSStickySource{}
	}
	key := stickyKey(prefix, group, id)
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		value, err := s.rdb.Get(ctx, key).Result()
		if err != nil {
			return openAIWSStickySource{}
		}
		// Bare numeric values written by legacy gateways are channel IDs.
		source, _ := decodeStickySource(value, relaybiz.UpstreamRouteChannel)
		return source
	}
	s.hotMu.RLock()
	defer s.hotMu.RUnlock()
	if binding, ok := s.hot[key]; ok && time.Now().Before(binding.expiresAt) {
		return binding.source
	}
	return openAIWSStickySource{}
}

func (s *openAIWSStickyStore) BindSessionChannel(ctx context.Context, group, sessionHash string, channelID int64, ttl time.Duration) {
	// SessionAccountStore uses this method exclusively for subscription-account
	// stickiness, so its ID belongs to the subscription namespace.
	s.bindSource(ctx, openAIWSSessionStickyKeyPrefix, group, sessionHash, openAIWSStickySource{kind: relaybiz.UpstreamRouteSubscription, id: channelID}, ttl)
}

func (s *openAIWSStickyStore) BindSessionRoute(ctx context.Context, group, sessionHash string, channel *relaybiz.Channel, ttl time.Duration) {
	s.bindSource(ctx, openAIWSSessionStickyKeyPrefix, group, sessionHash, stickySourceForChannel(channel), ttl)
}

func (s *openAIWSStickyStore) LookupSessionChannel(ctx context.Context, group, sessionHash string) int64 {
	source := s.LookupSessionRoute(ctx, group, sessionHash)
	if source.kind != relaybiz.UpstreamRouteSubscription {
		return 0
	}
	return source.id
}

func (s *openAIWSStickyStore) LookupSessionRoute(ctx context.Context, group, sessionHash string) openAIWSStickySource {
	return s.lookupSource(ctx, openAIWSSessionStickyKeyPrefix, group, sessionHash)
}

func (s *openAIWSStickyStore) RefreshSessionTTL(ctx context.Context, group, sessionHash string, ttl time.Duration) bool {
	id := normalizeStickyResponseID(sessionHash)
	if id == "" {
		return false
	}
	if ttl <= 0 {
		ttl = openAIWSStickyTTL
	}
	key := stickyKey(openAIWSSessionStickyKeyPrefix, group, id)
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		ok, err := s.rdb.PExpire(ctx, key, ttl).Result()
		return err == nil && ok
	}
	s.hotMu.Lock()
	defer s.hotMu.Unlock()
	now := time.Now()
	binding, ok := s.hot[key]
	if !ok || !now.Before(binding.expiresAt) {
		return false
	}
	binding.expiresAt = now.Add(ttl)
	s.hot[key] = binding
	return true
}

func (s *openAIWSStickyStore) DeleteSession(ctx context.Context, group, sessionHash string) {
	id := normalizeStickyResponseID(sessionHash)
	if id == "" {
		return
	}
	key := stickyKey(openAIWSSessionStickyKeyPrefix, group, id)
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = s.rdb.Del(ctx, key).Err()
		return
	}
	s.hotMu.Lock()
	delete(s.hot, key)
	s.hotMu.Unlock()
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

func normalizeStickyResponseID(value string) string { return strings.TrimSpace(value) }

func stickyKey(prefix, group, id string) string {
	return prefix + group + ":" + id
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
