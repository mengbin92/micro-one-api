package middleware

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// RateLimiter implements a simple in-memory rate limiter
type RateLimiter struct {
	clients    map[string]*ClientLimiter
	mutex      sync.RWMutex
	rate       int
	burst      int
	maxClients int
}

// ClientLimiter tracks rate limiting for a single client
type ClientLimiter struct {
	tokens    int
	lastSeen  time.Time
	requests  []time.Time
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	RequestsPerSecond int
	Burst             int
	Window            time.Duration
	MaxClients        int
}

// DefaultRateLimitConfig returns default rate limiting configuration
func DefaultRateLimitConfig() *RateLimitConfig {
	rps := 100
	if rpsStr := os.Getenv("RATE_LIMIT_REQUESTS_PER_SECOND"); rpsStr != "" {
		if val, err := strconv.Atoi(rpsStr); err == nil && val > 0 {
			rps = val
		}
	}

	burst := 200
	if burstStr := os.Getenv("RATE_LIMIT_BURST"); burstStr != "" {
		if val, err := strconv.Atoi(burstStr); err == nil && val > 0 {
			burst = val
		}
	}

	maxClients := 100000
	if maxStr := os.Getenv("RATE_LIMIT_MAX_CLIENTS"); maxStr != "" {
		if val, err := strconv.Atoi(maxStr); err == nil && val > 0 {
			maxClients = val
		}
	}

	return &RateLimitConfig{
		RequestsPerSecond: rps,
		Burst:             burst,
		Window:            time.Minute,
		MaxClients:        maxClients,
	}
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config *RateLimitConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	return &RateLimiter{
		clients:    make(map[string]*ClientLimiter),
		rate:       config.RequestsPerSecond,
		burst:      config.Burst,
		maxClients: config.MaxClients,
	}
}

// Allow checks if a request from the given key should be allowed
func (rl *RateLimiter) Allow(key string) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	client, exists := rl.clients[key]

	if !exists {
		// Enforce max clients limit to prevent memory exhaustion
		if len(rl.clients) >= rl.maxClients {
			applogger.Log.Warn("Rate limiter max clients reached",
				zap.Int("max_clients", rl.maxClients),
			)
			return false
		}
		client = &ClientLimiter{
			tokens:   rl.burst - 1,
			lastSeen: now,
			requests: []time.Time{now},
		}
		rl.clients[key] = client
		return true
	}

	// Clean up old requests
	cutoff := now.Add(-time.Minute)
	validRequests := make([]time.Time, 0, len(client.requests))
	for _, reqTime := range client.requests {
		if reqTime.After(cutoff) {
			validRequests = append(validRequests, reqTime)
		}
	}
	client.requests = validRequests

	// Check if rate limit exceeded
	if len(client.requests) >= rl.rate {
		applogger.Log.Warn("Rate limit exceeded",
			zap.String("key", key),
			zap.Int("requests", len(client.requests)),
			zap.Int("limit", rl.rate),
		)
		return false
	}

	// Add current request
	client.requests = append(client.requests, now)
	client.lastSeen = now

	return true
}

// Cleanup removes stale entries from the rate limiter
func (rl *RateLimiter) Cleanup() {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	for key, client := range rl.clients {
		if now.Sub(client.lastSeen) > 5*time.Minute {
			delete(rl.clients, key)
		}
	}
}

// RateLimit creates a rate limiting middleware
func RateLimit(config *RateLimitConfig) func(http.Handler) http.Handler {
	limiter := NewRateLimiter(config)

	// Start cleanup goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			limiter.Cleanup()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract rate limit key (IP or token)
			key := extractRateLimitKey(r)

			// Check rate limit
			if !limiter.Allow(key) {
				applogger.Log.Warn("Request rate limited",
					zap.String("key", key),
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method),
					zap.String("remote_addr", r.RemoteAddr),
				)

				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.rate))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", "60")

				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":{"message":"rate limit exceeded","code":429}}`))
				return
			}

			// Add rate limit headers
			remaining := limiter.rate - len(limiter.clients[key].requests)
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.rate))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

			next.ServeHTTP(w, r)
		})
	}
}

// SimpleRateLimit creates a simple rate limiting middleware with default settings
func SimpleRateLimit() func(http.Handler) http.Handler {
	return RateLimit(DefaultRateLimitConfig())
}

// extractRateLimitKey extracts a rate limit key from the request
func extractRateLimitKey(r *http.Request) string {
	// Try to use token for rate limiting (more accurate)
	if token := extractToken(r); token != "" {
		return "token:" + token
	}

	// Fall back to IP address
	ip := getClientIP(r)
	return "ip:" + ip
}

// extractToken extracts the Bearer token from the request
func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		// Return a hash of the token for privacy
		return simpleHash(authHeader[7:])
	}

	return ""
}

// getClientIP extracts the client IP address from the request.
// Only trusts RemoteAddr to prevent X-Forwarded-For spoofing.
// Behind a trusted reverse proxy, the proxy should set X-Forwarded-For
// and this function should be updated to trust the proxy's IP range.
func getClientIP(r *http.Request) string {
	// Use RemoteAddr only - do not trust client-supplied headers
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}

	return r.RemoteAddr
}

// simpleHash creates a SHA-256 hash of a string, truncated to hex
func simpleHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8]) // first 8 bytes = 16 hex chars
}

