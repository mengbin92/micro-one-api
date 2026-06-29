// Package middleware provides HTTP middleware components.
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// IdempotencyConfig holds configuration for idempotency middleware.
type IdempotencyConfig struct {
	// Header is the name of the header containing the idempotency key.
	Header string
	// TTL is how long idempotency keys are stored.
	TTL time.Duration
	// CacheKeys determines whether to cache response keys.
	CacheKeys bool
}

// DefaultIdempotencyConfig returns default idempotency configuration.
func DefaultIdempotencyConfig() *IdempotencyConfig {
	return &IdempotencyConfig{
		Header:    "Idempotency-Key",
		TTL:       24 * time.Hour,
		CacheKeys: true,
	}
}

// IdempotencyMiddleware provides idempotency support for HTTP requests.
//
// It ensures that requests with the same idempotency key return the same response,
// preventing duplicate operations. This is critical for:
// - Payment processing
// - Resource creation
// - State-changing operations
//
// The middleware stores response data in Redis with the idempotency key.
// Subsequent requests with the same key return the cached response.
type IdempotencyMiddleware struct {
	redis      *redis.Client
	config     *IdempotencyConfig
	localCache *idempotencyCache
}

// IdempotencyResponse represents a cached response.
type IdempotencyResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
	Replay     bool              `json:"replay"`
}

// idempotencyCache provides local in-memory caching for recent idempotency
// keys with bounded size and TTL-based eviction.
type idempotencyCache struct {
	mu        sync.RWMutex
	keys      map[string]*idempotencyEntry
	max       int
	ttl       time.Duration
	lastSweep time.Time
}

// idempotencyEntry pairs a cached response with its insertion time for TTL.
type idempotencyEntry struct {
	resp    *IdempotencyResponse
	addedAt time.Time
}

func newIdempotencyCache(max int, ttl time.Duration) *idempotencyCache {
	if max <= 0 {
		max = 1000
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &idempotencyCache{
		keys:      make(map[string]*idempotencyEntry),
		max:       max,
		ttl:       ttl,
		lastSweep: time.Now(),
	}
}

// get returns a cached response if present and not expired. Sweep of expired
// entries runs opportunistically at most once per ttl.
func (c *idempotencyCache) get(key string) (*IdempotencyResponse, bool) {
	c.mu.RLock()
	e, ok := c.keys[key]
	c.mu.RUnlock()
	if !ok || e == nil {
		return nil, false
	}
	if time.Since(e.addedAt) > c.ttl {
		c.mu.Lock()
		delete(c.keys, key)
		c.mu.Unlock()
		return nil, false
	}
	e.resp.Replay = true
	return e.resp, true
}

// set stores a response, evicting the oldest entry if at capacity.
func (c *idempotencyCache) set(key string, resp *IdempotencyResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	// Opportunistic sweep of expired entries.
	if now.Sub(c.lastSweep) >= c.ttl {
		for k, ent := range c.keys {
			if now.Sub(ent.addedAt) > c.ttl {
				delete(c.keys, k)
			}
		}
		c.lastSweep = now
	}

	if len(c.keys) >= c.max {
		// Evict the oldest entry (approximate LRU by insertion time).
		var oldestKey string
		var oldestAt = now
		for k, ent := range c.keys {
			if ent.addedAt.Before(oldestAt) {
				oldestAt = ent.addedAt
				oldestKey = k
			}
		}
		if oldestKey != "" {
			delete(c.keys, oldestKey)
		}
	}

	c.keys[key] = &idempotencyEntry{resp: resp, addedAt: now}
}

// NewIdempotencyMiddleware creates a new idempotency middleware.
func NewIdempotencyMiddleware(redisClient *redis.Client, cfg *IdempotencyConfig) *IdempotencyMiddleware {
	if cfg == nil {
		cfg = DefaultIdempotencyConfig()
	}

	return &IdempotencyMiddleware{
		redis:      redisClient,
		config:     cfg,
		localCache: newIdempotencyCache(1000, 5*time.Minute),
	}
}

// Handler returns the middleware handler.
func (im *IdempotencyMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only apply to POST, PATCH, PUT, DELETE requests
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// Extract idempotency key from header
		key := r.Header.Get(im.config.Header)
		if key == "" {
			// No idempotency key, proceed with request
			next.ServeHTTP(w, r)
			return
		}

		// Normalize the key (trim and hash for consistency)
		normalizedKey := normalizeIdempotencyKey(key)

		// Check if we have a cached response
		if cachedResp := im.getCachedResponse(r.Context(), normalizedKey); cachedResp != nil {
			im.writeCachedResponse(w, r, cachedResp)
			applogger.Log.Info("Idempotency replay",
				zap.String("key", normalizedKey),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
			)
			return
		}

		// Wrap the response writer to capture the response
		wrapped := &idempotentResponseWriter{
			ResponseWriter: w,
			request:        r,
			key:            normalizedKey,
			middleware:     im,
		}

		// Process the request
		next.ServeHTTP(wrapped, r)
	})
}

// getCachedResponse retrieves a cached response if available.
func (im *IdempotencyMiddleware) getCachedResponse(ctx context.Context, key string) *IdempotencyResponse {
	// Check local cache first (TTL-aware).
	if im.localCache != nil {
		if resp, ok := im.localCache.get(key); ok {
			return resp
		}
	}

	// Check Redis
	if im.redis != nil {
		redisKey := im.redisKey(key)
		data, err := im.redis.Get(ctx, redisKey).Bytes()
		if err == nil && len(data) > 0 {
			var resp IdempotencyResponse
			if err := sonic.Unmarshal(data, &resp); err == nil {
				// Populate local cache for future replays.
				if im.localCache != nil {
					im.localCache.set(key, &resp)
				}
				resp.Replay = true
				return &resp
			}
			if applogger.Log != nil {
				applogger.Log.Debug("failed to unmarshal idempotency response from Redis",
					zap.String("key", key), zap.Error(err))
			}
		}
	}

	return nil
}

// cacheResponse stores a response for future replay.
func (im *IdempotencyMiddleware) cacheResponse(ctx context.Context, key string, resp *IdempotencyResponse) {
	// Store in local cache
	if im.localCache != nil && im.config.CacheKeys {
		im.localCache.set(key, resp)
	}

	// Store in Redis
	if im.redis != nil {
		redisKey := im.redisKey(key)
		if data, err := sonic.Marshal(resp); err == nil {
			ttl := im.config.TTL
			if ttl <= 0 {
				ttl = 24 * time.Hour
			}
			if err := im.redis.Set(ctx, redisKey, data, ttl).Err(); err != nil && applogger.Log != nil {
				applogger.Log.Debug("failed to store idempotency response in Redis",
					zap.String("key", key), zap.Error(err))
			}
		}
	}
}

// writeCachedResponse writes a cached response to the client.
func (im *IdempotencyMiddleware) writeCachedResponse(w http.ResponseWriter, _ *http.Request, resp *IdempotencyResponse) {
	// Copy headers
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	// Set idempotency replay header
	w.Header().Set("Idempotency-Replayed", "true")

	// Write status code and body
	w.WriteHeader(resp.StatusCode)
	if len(resp.Body) > 0 {
		w.Write(resp.Body)
	}
}

// redisKey generates a Redis key for the idempotency key.
func (im *IdempotencyMiddleware) redisKey(key string) string {
	return fmt.Sprintf("idempotency:%s", key)
}

// idempotentResponseWriter wraps http.ResponseWriter to capture responses.
type idempotentResponseWriter struct {
	http.ResponseWriter
	request    *http.Request
	key        string
	middleware *IdempotencyMiddleware
	statusCode int
	written    bool
	headers    map[string]string
}

// WriteHeader captures the status code and writes it.
func (iw *idempotentResponseWriter) WriteHeader(statusCode int) {
	if !iw.written {
		iw.statusCode = statusCode
		iw.written = true
	}
	iw.ResponseWriter.WriteHeader(statusCode)
}

// Write captures the body and writes it. The response is cached on the first
// write (after finalizing status code and headers) so subsequent replays
// return the identical response.
func (iw *idempotentResponseWriter) Write(data []byte) (int, error) {
	if !iw.written {
		iw.statusCode = http.StatusOK
		iw.written = true
	}

	// Cache the response on first write
	if iw.key != "" {
		// Snapshot headers now (Header() may have been mutated between
		// WriteHeader and Write).
		iw.captureHeaders()
		resp := &IdempotencyResponse{
			StatusCode: iw.statusCode,
			Headers:    iw.headers,
			Body:       data,
			Replay:     false,
		}
		iw.middleware.cacheResponse(iw.request.Context(), iw.key, resp)
		// Clear key so we don't cache again
		iw.key = ""
	}

	return iw.ResponseWriter.Write(data)
}

// Header returns the header map.
func (iw *idempotentResponseWriter) Header() http.Header {
	return iw.ResponseWriter.Header()
}

// captureHeaders snapshots the current response headers (first value of each)
// into iw.headers for later caching.
func (iw *idempotentResponseWriter) captureHeaders() {
	h := iw.ResponseWriter.Header()
	iw.headers = make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			iw.headers[k] = v[0]
		}
	}
}

// normalizeIdempotencyKey normalizes an idempotency key for consistent hashing.
func normalizeIdempotencyKey(key string) string {
	// Trim whitespace
	key = trimSpace(key)

	// If key is already a hash format, return as-is
	if looksLikeHash(key) {
		return key
	}

	// Hash the key for consistency and security
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// trimSpace removes leading and trailing whitespace.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// looksLikeHash checks if a string looks like a hex hash.
func looksLikeHash(s string) bool {
	if len(s) < 32 {
		return false
	}
	for _, c := range s {
		if !isHexByte(c) {
			return false
		}
	}
	return true
}

// isHexByte checks if a rune is a valid hex character.
func isHexByte(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// ValidateIdempotencyKey validates an idempotency key format.
func ValidateIdempotencyKey(key string) error {
	if key == "" {
		return errors.New("idempotency key cannot be empty")
	}
	if len(key) > 256 {
		return errors.New("idempotency key too long (max 256 characters)")
	}
	// Check if it looks like a hash or is a reasonable string
	if !looksLikeHash(key) && len(key) < 8 {
		return errors.New("idempotency key too short (min 8 characters unless using hash format)")
	}
	return nil
}

// GenerateIdempotencyKey generates a new idempotency key from request parameters.
func GenerateIdempotencyKey(method, path, userID, resourceID string) string {
	data := fmt.Sprintf("%s:%s:%s:%s:%d", method, path, userID, resourceID, time.Now().Unix()/60)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
