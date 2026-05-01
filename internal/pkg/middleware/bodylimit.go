package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

const (
	// DefaultMaxBodySize is the default maximum request body size (10MB)
	DefaultMaxBodySize = 10 * 1024 * 1024
)

// MaxBodySize creates middleware that limits the size of request bodies
func MaxBodySize(maxSize int64) func(http.Handler) http.Handler {
	if maxSize <= 0 {
		maxSize = DefaultMaxBodySize

		// Try to get from environment
		if sizeStr := os.Getenv("MAX_BODY_SIZE"); sizeStr != "" {
			if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
				maxSize = size
			}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Limit the request body size
			r.Body = http.MaxBytesReader(w, r.Body, maxSize)

			// Call next handler
			next.ServeHTTP(w, r)
		})
	}
}

// SimpleMaxBodySize creates a simple body size limit middleware with default settings
func SimpleMaxBodySize() func(http.Handler) http.Handler {
	return MaxBodySize(DefaultMaxBodySize)
}

// ValidateJSONBody validates and limits JSON request body size
func ValidateJSONBody(v interface{}, maxSize int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Limit body size
			limitedReader := io.LimitReader(r.Body, maxSize)

			// Decode with size limit
			decoder := json.NewDecoder(limitedReader)
			decoder.DisallowUnknownFields()

			if err := decoder.Decode(v); err != nil {
				var syntaxError *json.SyntaxError
				var unmarshalTypeError *json.UnmarshalTypeError

				switch {
				case errors.As(err, &syntaxError):
					applogger.Log.Warn("Invalid JSON syntax",
						zap.Error(err),
						zap.String("path", r.URL.Path),
					)
					http.Error(w, `{"error":{"message":"Invalid JSON syntax","code":400}}`, http.StatusBadRequest)

				case errors.As(err, &unmarshalTypeError):
					applogger.Log.Warn("Invalid JSON type",
						zap.Error(err),
						zap.String("path", r.URL.Path),
						zap.String("field", unmarshalTypeError.Field),
					)
					http.Error(w, `{"error":{"message":"Invalid JSON type","code":400}}`, http.StatusBadRequest)

				case errors.Is(err, io.ErrUnexpectedEOF):
					applogger.Log.Warn("Invalid JSON structure",
						zap.Error(err),
						zap.String("path", r.URL.Path),
					)
					http.Error(w, `{"error":{"message":"Invalid JSON structure","code":400}}`, http.StatusBadRequest)

				case err.Error() == "http: request body too large":
					applogger.Log.Warn("Request body too large",
						zap.Int64("max_size", maxSize),
						zap.String("path", r.URL.Path),
						zap.String("method", r.Method),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					w.Write([]byte(`{"error":{"message":"request body too large","code":413}}`))

				default:
					applogger.Log.Error("Error decoding JSON",
						zap.Error(err),
						zap.String("path", r.URL.Path),
					)
					http.Error(w, `{"error":{"message":"Invalid request","code":400}}`, http.StatusBadRequest)
				}
				return
			}

			// Check for additional data
			if _, err := io.Copy(io.Discard, limitedReader); err != nil {
				applogger.Log.Warn("Request body too large after decode",
					zap.Error(err),
					zap.Int64("max_size", maxSize),
					zap.String("path", r.URL.Path),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				w.Write([]byte(`{"error":{"message":"request body too large","code":413}}`))
				return
			}

			// Reset body for next handlers
			// Note: In production, you might want to cache the decoded body
			next.ServeHTTP(w, r)
		})
	}
}
