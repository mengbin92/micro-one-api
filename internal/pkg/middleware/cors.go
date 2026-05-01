package middleware

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// CORSConfig holds CORS configuration
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig returns default CORS configuration
func DefaultCORSConfig() *CORSConfig {
	allowedOrigins := []string{"https://yourdomain.com", "https://app.yourdomain.com"}
	if origins := os.Getenv("CORS_ALLOWED_ORIGINS"); origins != "" {
		allowedOrigins = strings.Split(origins, ",")
		for i, origin := range allowedOrigins {
			allowedOrigins[i] = strings.TrimSpace(origin)
		}
	}

	return &CORSConfig{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS", "PUT", "DELETE", "PATCH"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"Content-Length", "Content-Type", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           86400, // 24 hours
	}
}

// CORS creates a CORS middleware with the given configuration
func CORS(config *CORSConfig) func(http.Handler) http.Handler {
	if config == nil {
		config = DefaultCORSConfig()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			allowedOrigin := isAllowedOrigin(origin, config.AllowedOrigins)
			if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(config.AllowedMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(config.AllowedHeaders, ", "))
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(config.ExposedHeaders, ", "))
				w.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", config.MaxAge))

				if config.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}

			// Handle preflight requests
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Log CORS details
			if origin != "" {
				applogger.Log.Debug("CORS request",
					zap.String("origin", origin),
					zap.Bool("allowed", allowedOrigin != ""),
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
				)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isAllowedOrigin checks if the origin is in the allowed list
func isAllowedOrigin(origin string, allowedOrigins []string) string {
	if origin == "" {
		return ""
	}

	for _, allowed := range allowedOrigins {
		if allowed == "*" {
			return "*"
		}
		if strings.EqualFold(allowed, origin) {
			return origin
		}
	}

	return ""
}

// SimpleCORS creates a simple CORS middleware with default settings
func SimpleCORS() func(http.Handler) http.Handler {
	return CORS(DefaultCORSConfig())
}
