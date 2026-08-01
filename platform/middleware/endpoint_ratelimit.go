package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// EndpointRateLimiter provides per-endpoint rate limiting
type EndpointRateLimiter struct {
	limiters map[string]*RateLimiter
	configs  map[string]*RateLimitConfig
	mu       sync.RWMutex
}

// NewEndpointRateLimiter creates a new per-endpoint rate limiter
func NewEndpointRateLimiter() *EndpointRateLimiter {
	return &EndpointRateLimiter{
		limiters: make(map[string]*RateLimiter),
		configs:  make(map[string]*RateLimitConfig),
	}
}

// RegisterEndpoint registers a rate limit config for a specific endpoint
func (erl *EndpointRateLimiter) RegisterEndpoint(path string, config *RateLimitConfig) {
	erl.mu.Lock()
	defer erl.mu.Unlock()
	erl.configs[path] = config
	erl.limiters[path] = NewRateLimiter(config)
}

// EndpointRateLimitMiddleware creates a middleware with per-endpoint rate limiting
func EndpointRateLimitMiddleware(endpointConfigs map[string]*RateLimitConfig) func(http.Handler) http.Handler {
	erl := NewEndpointRateLimiter()
	for path, config := range endpointConfigs {
		erl.RegisterEndpoint(path, config)
	}

	// Start cleanup goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			erl.mu.RLock()
			for _, limiter := range erl.limiters {
				limiter.Cleanup()
			}
			erl.mu.RUnlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			erl.mu.RLock()
			limiter, exists := erl.limiters[r.URL.Path]
			config := erl.configs[r.URL.Path]
			erl.mu.RUnlock()

			if !exists {
				next.ServeHTTP(w, r)
				return
			}

			key := extractRateLimitKey(r)
			if allowed, remaining := limiter.Allow(key); !allowed {
				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", config.RequestsPerSecond))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded for this endpoint","code":429}}`))
				return
			} else {
				w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// DefaultEndpointConfigs returns sensible default rate limit configs for common endpoints
func DefaultEndpointConfigs() map[string]*RateLimitConfig {
	return map[string]*RateLimitConfig{
		"/v1/chat/completions": {
			RequestsPerSecond: 60,
			Burst:             120,
			Window:            time.Minute,
		},
		"/v1/models": {
			RequestsPerSecond: 200,
			Burst:             400,
			Window:            time.Minute,
		},
	}
}
