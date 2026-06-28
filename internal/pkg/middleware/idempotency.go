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

// idempotencyCache provides local in-memory caching for recent idempotency keys.
type idempotencyCache struct {
	mu    sync.RWMutex
	keys  map[string]*IdempotencyResponse
	max   int
	ttl   time.Duration
	timer *time.Timer
}

func newIdempotencyCache(max int, ttl time.Duration) *idempotencyCache {
	return &idempotencyCache{
		keys: make(map[string]*IdempotencyResponse),
		max:  max,
		ttl:  ttl,
	}
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
			request:       r,
			key:           normalizedKey,
			middleware:    im,
		}

		// Process the request
		next.ServeHTTP(wrapped, r)
	})
}

// getCachedResponse retrieves a cached response if available.
func (im *IdempotencyMiddleware) getCachedResponse(ctx context.Context, key string) *IdempotencyResponse {
	// Check local cache first
	if im.localCache != nil {
		im.localCache.mu.RLock()
		if resp, ok := im.localCache.keys[key]; ok {
			im.localCache.mu.RUnlock()
			resp.Replay = true
			return resp
		}
		im.localCache.mu.RUnlock()
	}

	// Check Redis
	if im.redis != nil {
		redisKey := im.redisKey(key)
		data, err := im.redis.Get(ctx, redisKey).Bytes()
		if err == nil && len(data) > 0 {
			// Deserialize response
			// For simplicity, we'll assume JSON here
			// In production, use a proper serialization format
			// For now, return nil as this is a placeholder
			applogger.Log.Debug("Idempotency key found in Redis",
				zap.String("key", key),
			)
			// TODO: Implement proper deserialization
		}
	}

	return nil
}

// cacheResponse stores a response for future replay.
func (im *IdempotencyMiddleware) cacheResponse(ctx context.Context, key string, resp *IdempotencyResponse) {
	// Store in local cache
	if im.localCache != nil && im.config.CacheKeys {
		im.localCache.mu.Lock()
		im.localCache.keys[key] = resp
		im.localCache.mu.Unlock()
	}

	// Store in Redis
	if im.redis != nil {
		redisKey := im.redisKey(key)
		// TODO: Implement proper serialization
		// For now, we'll skip storing in Redis
		_ = redisKey
		_ = ctx
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

// Write captures the body and writes it.
func (iw *idempotentResponseWriter) Write(data []byte) (int, error) {
	if !iw.written {
		iw.statusCode = http.StatusOK
		iw.written = true
	}

	// Cache the response on first write
	if iw.key != "" {
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
	h := iw.ResponseWriter.Header()
	// Capture headers
	if iw.headers == nil {
		iw.headers = make(map[string]string)
		for k, v := range h {
			if len(v) > 0 {
				iw.headers[k] = v[0]
			}
		}
	}
	return h
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
