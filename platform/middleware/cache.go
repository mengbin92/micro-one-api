package middleware

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// cacheEntry represents a cached HTTP response
type cacheEntry struct {
	statusCode int
	header     http.Header
	body       []byte
	expiresAt  time.Time
}

// ResponseCache is an in-memory HTTP response cache
type ResponseCache struct {
	entries sync.Map
	maxSize int
	count   int
	mu      sync.Mutex
}

// CacheConfig holds cache configuration
type CacheConfig struct {
	TTL     time.Duration
	MaxSize int
	Methods []string
	Paths   []string
}

// DefaultCacheConfig returns a default cache configuration
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		TTL:     30 * time.Second,
		MaxSize: 1024,
		Methods: []string{"GET"},
		Paths:   []string{"/v1/models"},
	}
}

// NewResponseCache creates a new response cache
func NewResponseCache(maxSize int) *ResponseCache {
	if maxSize <= 0 {
		maxSize = 1024
	}
	return &ResponseCache{
		maxSize: maxSize,
	}
}

// cacheResponseWriter wraps http.ResponseWriter to capture the response
type cacheResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
	header     http.Header
}

func newCacheResponseWriter(w http.ResponseWriter) *cacheResponseWriter {
	return &cacheResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           &bytes.Buffer{},
		header:         make(http.Header),
	}
}

func (crw *cacheResponseWriter) WriteHeader(code int) {
	crw.statusCode = code
}

func (crw *cacheResponseWriter) Write(b []byte) (int, error) {
	return crw.body.Write(b)
}

func (crw *cacheResponseWriter) Header() http.Header {
	return crw.header
}

// cacheKey generates a cache key from the request
func cacheKey(r *http.Request) string {
	key := fmt.Sprintf("%s:%s", r.Method, r.URL.String())
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", hash[:8])
}

// ResponseCacheMiddleware creates a middleware that caches HTTP responses
func ResponseCacheMiddleware(config *CacheConfig) func(http.Handler) http.Handler {
	if config == nil {
		config = DefaultCacheConfig()
	}
	cache := NewResponseCache(config.MaxSize)

	// Pre-check if path should be cached
	shouldCache := func(path string, method string) bool {
		for _, m := range config.Methods {
			if m == method {
				for _, p := range config.Paths {
					if p == path {
						return true
					}
				}
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldCache(r.URL.Path, r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			key := cacheKey(r)

			// Check cache
			if entry, ok := cache.entries.Load(key); ok {
				ce := entry.(*cacheEntry)
				if time.Now().Before(ce.expiresAt) {
					// Cache hit
					for k, v := range ce.header {
						for _, vv := range v {
							w.Header().Add(k, vv)
						}
					}
					w.Header().Set("X-Cache", "HIT")
					w.WriteHeader(ce.statusCode)
					w.Write(ce.body)
					return
				}
				// Expired, delete
				cache.entries.Delete(key)
			}

			// Cache miss - capture response
			crw := newCacheResponseWriter(w)
			next.ServeHTTP(crw, r)

			// Write to actual response
			for k, v := range crw.header {
				for _, vv := range v {
					w.Header().Add(k, vv)
				}
			}
			w.Header().Set("X-Cache", "MISS")
			w.WriteHeader(crw.statusCode)
			w.Write(crw.body.Bytes())

			// Store in cache if successful
			if crw.statusCode >= 200 && crw.statusCode < 300 {
				cache.mu.Lock()
				if cache.count < cache.maxSize {
					cache.entries.Store(key, &cacheEntry{
						statusCode: crw.statusCode,
						header:     crw.header.Clone(),
						body:       crw.body.Bytes(),
						expiresAt:  time.Now().Add(config.TTL),
					})
					cache.count++
				}
				cache.mu.Unlock()
			}
		})
	}
}

// InvalidateCache removes a cached entry by key pattern
func (rc *ResponseCache) Invalidate(keyPattern string) {
	rc.entries.Range(func(key, value interface{}) bool {
		if k, ok := key.(string); ok && k == keyPattern {
			rc.entries.Delete(key)
			rc.mu.Lock()
			rc.count--
			rc.mu.Unlock()
		}
		return true
	})
}
