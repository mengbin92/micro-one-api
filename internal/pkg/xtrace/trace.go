package xtrace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type contextKey string

const traceIDKey contextKey = "traceID"

// GenerateTraceID creates a new random 16-byte hex trace ID.
func GenerateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WithTraceID stores a trace ID in the context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// ExtractTraceID retrieves the trace ID from context. Returns empty string if not present.
func ExtractTraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// TraceIDHeader is the HTTP header used to propagate trace IDs.
const TraceIDHeader = "X-Trace-ID"

// Middleware returns an HTTP middleware that extracts or generates a trace ID.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(TraceIDHeader)
		if traceID == "" {
			traceID = GenerateTraceID()
		}
		ctx := WithTraceID(r.Context(), traceID)
		w.Header().Set(TraceIDHeader, traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
