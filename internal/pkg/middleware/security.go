package middleware

import (
	"context"
	crypto_rand "crypto/rand"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// controlCharsRegex matches control characters (CRLF, newlines, tabs, etc.)
var controlCharsRegex = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// sanitizeLogField removes control characters from a string to prevent log injection
func sanitizeLogField(s string) string {
	return controlCharsRegex.ReplaceAllString(s, "")
}

// sanitizeRequestID validates and sanitizes a request ID, rejecting values
// containing control characters or exceeding 128 bytes.
func sanitizeRequestID(id string) string {
	if len(id) > 128 {
		return ""
	}
	sanitized := controlCharsRegex.ReplaceAllString(id, "")
	if sanitized != id {
		return ""
	}
	return sanitized
}

// SecurityHeaders adds security-related HTTP headers to responses
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")

		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Enable XSS protection
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Content Security Policy
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self'; "+
				"img-src 'self' data: https:; "+
				"font-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none';")

		// Enable HSTS (for direct TLS or behind TLS-terminating proxy)
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}

		// Referrer policy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions policy
		w.Header().Set("Permissions-Policy",
			"geolocation=(), "+
				"microphone=(), "+
				"camera=(), "+
				"payment=(), "+
				"usb=(), "+
				"magnetometer=(), "+
				"gyroscope=(), "+
				"accelerometer=()")

		// Remove server information
		w.Header().Set("Server", "")

		next.ServeHTTP(w, r)
	})
}

// RequestID adds a unique request ID to each request for tracing
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = generateRequestID()
		}

		w.Header().Set("X-Request-ID", requestID)

		// Add request ID to context for logging
		ctx := contextWithRequestID(r.Context(), requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LoggingMiddleware logs all HTTP requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		applogger.Log.Info("HTTP request started",
			zap.String("method", r.Method),
			zap.String("path", sanitizeLogField(r.URL.Path)),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("user_agent", sanitizeLogField(r.UserAgent())),
			zap.String("request_id", GetRequestID(r.Context())),
		)

		// Wrap response writer to capture status code
		wrapped := &responseWriter{w, http.StatusOK}
		next.ServeHTTP(wrapped, r)

		// Log response
		applogger.Log.Info("HTTP request completed",
			zap.String("method", r.Method),
			zap.String("path", sanitizeLogField(r.URL.Path)),
			zap.String("request_id", GetRequestID(r.Context())),
			zap.Int("status", wrapped.status),
			zap.Duration("duration", time.Since(startTime)),
		)
	})
}

// Response wrapper to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Request ID context key
type contextKey string

const requestIDKey contextKey = "requestID"

func contextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

func GetRequestID(ctx context.Context) string {
	if requestID, ok := ctx.Value(requestIDKey).(string); ok {
		return requestID
	}
	return ""
}

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}
