// Package middleware provides HTTP middleware components.
package middleware

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
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
// MaxCachedBodyBytes caps the response body that the idempotency middleware
// will buffer for replay. Responses larger than this are streamed to the
// client but NOT cached (the Idempotency-Key header has no replay effect),
// preventing unbounded memory growth from large responses (review Medium #6).
const MaxCachedBodyBytes = 1 << 20 // 1 MiB

type IdempotencyMiddleware struct {
	redis      *redis.Client
	config     *IdempotencyConfig
	localCache *idempotencyCache
	// inflightMu guards inflight, a map of in-flight idempotency keys. When
	// two concurrent requests share the same key, the second waits on the
	// key's *sync.WaitGroup until the first finishes and caches its response.
	// This closes the concurrent-duplicate-execution gap (review Medium #6).
	inflightMu sync.Mutex
	inflight   map[string]*sync.WaitGroup
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

const idempotencyLeaseTTL = 30 * time.Second

var releaseIdempotencyLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var renewIdempotencyLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

type idempotencyLease struct {
	middleware *IdempotencyMiddleware
	key        string
	token      string
	stop       chan struct{}
	done       chan struct{}
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
	if !ok || e == nil {
		c.mu.RUnlock()
		return nil, false
	}
	resp := cloneIdempotencyResponse(e.resp)
	addedAt := e.addedAt
	c.mu.RUnlock()
	if time.Since(addedAt) > c.ttl {
		c.mu.Lock()
		delete(c.keys, key)
		c.mu.Unlock()
		return nil, false
	}
	resp.Replay = true
	return resp, true
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

	c.keys[key] = &idempotencyEntry{resp: cloneIdempotencyResponse(resp), addedAt: now}
}

func cloneIdempotencyResponse(resp *IdempotencyResponse) *IdempotencyResponse {
	if resp == nil {
		return nil
	}
	clone := *resp
	clone.Body = append([]byte(nil), resp.Body...)
	clone.Headers = make(map[string]string, len(resp.Headers))
	for key, value := range resp.Headers {
		clone.Headers[key] = value
	}
	return &clone
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
		inflight:   make(map[string]*sync.WaitGroup),
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

		// M2: scope the idempotency key by caller identity + method + path so a
		// client that guesses/reuses another client's Idempotency-Key cannot
		// replay that client's cached response. Identity is the bearer token
		// hash (or "" when unauthenticated); both are folded into the hash, so
		// an attacker cannot enumerate other users' responses.
		normalizedKey := normalizeIdempotencyKey(key, r)

		// Check if we have a cached response (from a previous completed request)
	retry:
		if cachedResp := im.getCachedResponse(r.Context(), normalizedKey); cachedResp != nil {
			im.writeCachedResponse(w, r, cachedResp)
			applogger.Log.Info("Idempotency replay",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
			)
			return
		}

		// In-flight lock (review Medium #6): if another request with the same
		// key is already executing, wait for it to finish, then serve its
		// cached response. This prevents two concurrent identical requests
		// from both executing the real operation.
		for {
			wg := im.acquireInflight(normalizedKey)
			if wg == nil {
				break
			}
			// Another request is in-flight for this key; wait for it.
			// Use a goroutine + select so the wait is cancellable by the
			// request context.
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-r.Context().Done():
				return
			}
			// The first request should have cached the response by now.
			if cachedResp := im.getCachedResponse(r.Context(), normalizedKey); cachedResp != nil {
				im.writeCachedResponse(w, r, cachedResp)
				applogger.Log.Info("Idempotency replay (in-flight wait)",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
				)
				return
			}
			// The first request did not cache; only retry after the primary has
			// completed, so duplicate executions remain serialized.
		}

		lease, acquired, err := im.acquireDistributedInflight(r.Context(), normalizedKey)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				im.completeInflight(normalizedKey)
				return
			}
			applogger.Log.Warn("distributed idempotency lock unavailable; using local lock", zap.Error(err))
			acquired = true
		}
		if !acquired {
			im.completeInflight(normalizedKey)
			cachedResp, waitErr := im.waitDistributedInflight(r.Context(), normalizedKey)
			if waitErr != nil {
				return
			}
			if cachedResp != nil {
				im.writeCachedResponse(w, r, cachedResp)
				return
			}
			goto retry
		}

		// Wrap the response writer to capture the response
		wrapped := &idempotentResponseWriter{
			ResponseWriter: w,
			request:        r,
			key:            normalizedKey,
			middleware:     im,
		}

		// Process the request
		defer im.completeInflight(normalizedKey)
		if lease != nil {
			defer lease.release()
		}
		next.ServeHTTP(wrapped, r)
		// Cache the buffered response (2xx only) now that the handler is done.
		wrapped.finalize()
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
			if err := jsonx.Unmarshal(data, &resp); err == nil {
				// Populate local cache for future replays.
				if im.localCache != nil {
					im.localCache.set(key, &resp)
				}
				resp.Replay = true
				return &resp
			}
			applogger.Log.Debug("failed to unmarshal idempotency response from Redis",
				zap.Error(err))
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
		if data, err := jsonx.Marshal(resp); err == nil {
			ttl := im.config.TTL
			if ttl <= 0 {
				ttl = 24 * time.Hour
			}
			if err := im.redis.Set(ctx, redisKey, data, ttl).Err(); err != nil {
				applogger.Log.Debug("failed to store idempotency response in Redis",
					zap.Error(err))
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
		_, _ = w.Write(resp.Body) // #nosec G705 -- exact cached response replay, not template rendering.
	}
}

// acquireInflight attempts to register the current key as in-flight. It
// returns nil if this caller won the race (it is now the primary executor
// and must call completeInflight when done). It returns a non-nil WaitGroup
// if another request is already executing for this key — the caller should
// wait on the WaitGroup and then check for a cached response.
func (im *IdempotencyMiddleware) acquireInflight(key string) *sync.WaitGroup {
	im.inflightMu.Lock()
	defer im.inflightMu.Unlock()
	if wg, ok := im.inflight[key]; ok {
		return wg
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	im.inflight[key] = wg
	return nil
}

// completeInflight marks the in-flight execution for this key as done and
// removes it from the map. Called by the primary executor after finalize().
func (im *IdempotencyMiddleware) completeInflight(key string) {
	im.inflightMu.Lock()
	wg, ok := im.inflight[key]
	if ok {
		delete(im.inflight, key)
	}
	im.inflightMu.Unlock()
	if ok {
		wg.Done()
	}
}

// redisKey generates a Redis key for the idempotency key.
func (im *IdempotencyMiddleware) redisKey(key string) string {
	return fmt.Sprintf("idempotency:%s", key)
}

func (im *IdempotencyMiddleware) inflightRedisKey(key string) string {
	return im.redisKey(key) + ":inflight"
}

func (im *IdempotencyMiddleware) acquireDistributedInflight(ctx context.Context, key string) (*idempotencyLease, bool, error) {
	if im.redis == nil {
		return nil, true, nil
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return nil, false, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	lockKey := im.inflightRedisKey(key)
	acquired, err := im.redis.SetNX(ctx, lockKey, token, idempotencyLeaseTTL).Result()
	if err != nil || !acquired {
		return nil, acquired, err
	}
	lease := &idempotencyLease{
		middleware: im,
		key:        lockKey,
		token:      token,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go lease.renew(ctx)
	return lease, true, nil
}

func (im *IdempotencyMiddleware) waitDistributedInflight(ctx context.Context, key string) (*IdempotencyResponse, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if resp := im.getCachedResponse(ctx, key); resp != nil {
			return resp, nil
		}
		exists, err := im.redis.Exists(ctx, im.inflightRedisKey(key)).Result()
		if err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (lease *idempotencyLease) renew(ctx context.Context) {
	defer close(lease.done)
	ticker := time.NewTicker(idempotencyLeaseTTL / 3)
	defer ticker.Stop()
	for {
		select {
		case <-lease.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			rpcCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := renewIdempotencyLeaseScript.Run(
				rpcCtx,
				lease.middleware.redis,
				[]string{lease.key},
				lease.token,
				idempotencyLeaseTTL.Milliseconds(),
			).Result()
			cancel()
			if err != nil {
				applogger.Log.Warn("failed to renew idempotency lease", zap.Error(err))
			}
		}
	}
}

func (lease *idempotencyLease) release() {
	close(lease.stop)
	<-lease.done
	rpcCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := releaseIdempotencyLeaseScript.Run(
		rpcCtx,
		lease.middleware.redis,
		[]string{lease.key},
		lease.token,
	).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		applogger.Log.Warn("failed to release idempotency lease", zap.Error(err))
	}
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
	// body buffers the full response so multi-chunk / streaming responses are
	// cached in their entirety rather than only the first Write() (review M2).
	body bytes.Buffer
	// bufferOverflow is set when the response body exceeds
	// MaxCachedBodyBytes. The response is still streamed to the client but
	// is NOT cached for replay, preventing unbounded memory growth (review
	// Medium #6).
	bufferOverflow bool
}

// WriteHeader captures the status code and writes it.
func (iw *idempotentResponseWriter) WriteHeader(statusCode int) {
	if !iw.written {
		iw.statusCode = statusCode
		iw.written = true
	}
	iw.ResponseWriter.WriteHeader(statusCode)
}

// Write captures the body and writes it. The full response is buffered and
// cached only on the final write (after the handler returns) so that:
//   - multi-chunk / streaming responses are not truncated to the first chunk
//     (review M2);
//   - only 2xx responses are cached — 4xx/5xx errors are no longer replayed
//     for the full TTL (review M2).
func (iw *idempotentResponseWriter) Write(data []byte) (int, error) {
	if !iw.written {
		iw.statusCode = http.StatusOK
		iw.written = true
	}
	// Buffer the body for later caching, but only up to MaxCachedBodyBytes.
	// Once the buffer overflows the response is streamed through to the
	// client but not cached for replay (review Medium #6 — previously the
	// buffer grew without bound, a memory-exhaustion vector for large
	// responses).
	if !iw.bufferOverflow {
		if iw.body.Len()+len(data) > MaxCachedBodyBytes {
			iw.bufferOverflow = true
			iw.body.Reset() // free the partial buffer
		} else {
			iw.body.Write(data)
		}
	}
	return iw.ResponseWriter.Write(data)
}

// finalize caches the buffered response once the handler completes, but only
// for successful (2xx) responses. Called by the middleware after
// next.ServeHTTP returns. Errors are deliberately not cached: replaying a 500
// or 429 for the full TTL would amplify transient failures (review M2).
func (iw *idempotentResponseWriter) finalize() {
	if iw.key == "" {
		return
	}
	if iw.statusCode < 200 || iw.statusCode >= 300 {
		return
	}
	// Skip caching when the response body exceeded the size limit (review
	// Medium #6). The response was already streamed to the client; we just
	// don't buffer a replay copy.
	if iw.bufferOverflow {
		return
	}
	iw.captureHeaders()
	resp := &IdempotencyResponse{
		StatusCode: iw.statusCode,
		Headers:    iw.headers,
		Body:       append([]byte(nil), iw.body.Bytes()...),
		Replay:     false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	iw.middleware.cacheResponse(ctx, iw.key, resp)
}

// Header returns the header map.
func (iw *idempotentResponseWriter) Header() http.Header {
	return iw.ResponseWriter.Header()
}

// Flush delegates to the underlying ResponseWriter when it implements
// http.Flusher. This is required for streaming/SSE responses: without it,
// handlers that call w.(http.Flusher).Flush() would fail the type assertion
// and the stream would stall (review Medium #6). When the underlying writer
// does not implement Flusher, Flush is a no-op.
func (iw *idempotentResponseWriter) Flush() {
	if f, ok := iw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
// The key is scoped by the caller identity (bearer token hash), HTTP method and
// request path (review M2): an idempotency key is only meaningful within a
// single caller's single operation, so "order-123" issued by user A on
// POST /v1/chat/completions must not collide with user B using the same key.
func normalizeIdempotencyKey(key string, r *http.Request) string {
	// Trim whitespace
	key = trimSpace(key)

	// Fold in caller identity + method + path. Even when the raw key already
	// looks like a hash, we re-hash the composite so the scope is enforced.
	identity := ""
	method := ""
	path := ""
	if r != nil {
		identity = extractIdempotencyIdentity(r)
		method = r.Method
		path = r.URL.Path
	}
	composite := identity + "" + method + "" + path + "" + key
	hash := sha256.Sum256([]byte(composite))
	return hex.EncodeToString(hash[:])
}

// extractIdempotencyIdentity returns a privacy-preserving caller identity for
// idempotency key scoping: the SHA-256 of the bearer token, or "" when no
// Authorization header is present. It never returns the raw token.
func extractIdempotencyIdentity(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(authHeader) > len(prefix) && strings.EqualFold(authHeader[:len(prefix)], prefix) {
		sum := sha256.Sum256([]byte(authHeader[len(prefix):]))
		return hex.EncodeToString(sum[:])
	}
	// Non-bearer auth (e.g. an API key scheme): hash the whole header value.
	sum := sha256.Sum256([]byte(authHeader))
	return hex.EncodeToString(sum[:])
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
