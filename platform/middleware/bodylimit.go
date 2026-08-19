package middleware

// Keep encoding/json for ValidateJSONBody: it needs Decoder.DisallowUnknownFields()
// and custom error type checking (json.SyntaxError, json.UnmarshalTypeError) that sonic
// doesn't expose the same way. Body parsing is not the hot path for serialization.
//
// NOTE (dual-parser boundary): this middleware validates the body with stdlib
// encoding/json, while downstream handlers parse the same body via pkg/jsonx
// (sonic.ConfigStd). Both parsers are encoding/json-compatible, but accept/reject
// behavior may differ on edge inputs (unknown fields, number precision, escapes).
// That is intentional: this layer fails closed (rejects) before the handler sees
// the body, so a divergence would surface as a rejected request, never as a
// silently different parse downstream. If strict parity ever becomes required,
// migrate this file to jsonx and drop the stdlib type assertions.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
)

const (
	// DefaultMaxBodySize is the fallback maximum request body size (10MB).
	DefaultMaxBodySize = 10 * 1024 * 1024
	// JSONRequestBodyLimit bounds chat-like JSON requests. These requests are
	// buffered for model extraction and should fail before allocation grows.
	JSONRequestBodyLimit = 8 * 1024 * 1024
	// LargeRequestBodyLimit is reserved for raw proxy and multipart audio/image
	// payloads that can legitimately be larger than JSON while still having a hard cap.
	LargeRequestBodyLimit = 64 * 1024 * 1024
)

// RequestBodyLimitForPath returns the cap for an inbound relay endpoint.
// Keep the policy here so every transport registration applies the same
// limits, including the legacy and orchestrator paths.
func RequestBodyLimitForPath(path string) int64 {
	switch {
	case path == "/v1/chat/completions", path == "/v1/completions",
		path == "/v1/embeddings", path == "/v1/moderations",
		path == "/v1/images/generations", path == "/v1/audio/speech",
		path == "/v1/messages", path == "/v1/responses",
		strings.HasPrefix(path, "/v1/responses/"):
		return JSONRequestBodyLimit
	case path == "/v1/audio/transcriptions", path == "/v1/audio/translations",
		path == "/v1/images/edits", path == "/v1/images/variations",
		strings.HasPrefix(path, "/v1/oneapi/proxy/"):
		return LargeRequestBodyLimit
	default:
		return DefaultMaxBodySize
	}
}

// RequestBodyLimitByPath applies the endpoint-specific cap to the request.
// Content-Length is rejected eagerly; chunked requests are rejected by
// http.MaxBytesReader when the downstream handler reads past the cap.
func RequestBodyLimitByPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxSize := RequestBodyLimitForPath(r.URL.Path)
		if r.ContentLength > maxSize {
			WriteRequestBodyTooLarge(w, r.URL.Path)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxSize)
		next.ServeHTTP(w, r)
	})
}

// WriteRequestBodyTooLarge writes the protocol-compatible 413 envelope used
// by both middleware preflight and downstream streaming reads.
func WriteRequestBodyTooLarge(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	if path == "/v1/messages" {
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"request body too large"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"error":{"message":"request body too large","code":413}}`))
}

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
			if r.ContentLength > maxSize {
				WriteRequestBodyTooLarge(w, r.URL.Path)
				return
			}
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

			// Read one extra byte so a body that exactly fills the cap is valid,
			// while any larger body gets a deterministic 413.
			data, readErr := io.ReadAll(io.LimitReader(r.Body, maxSize+1))
			if readErr != nil {
				var maxErr *http.MaxBytesError
				if errors.As(readErr, &maxErr) {
					WriteRequestBodyTooLarge(w, r.URL.Path)
				} else {
					http.Error(w, `{"error":{"message":"invalid request body","code":400}}`, http.StatusBadRequest)
				}
				return
			}
			if int64(len(data)) > maxSize {
				applogger.Log.Warn("Request body too large",
					zap.Int64("max_size", maxSize),
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method),
				)
				WriteRequestBodyTooLarge(w, r.URL.Path)
				return
			}

			// Decode with size limit
			decoder := json.NewDecoder(bytes.NewReader(data))
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
					WriteRequestBodyTooLarge(w, r.URL.Path)

				default:
					applogger.Log.Error("Error decoding JSON",
						zap.Error(err),
						zap.String("path", r.URL.Path),
					)
					http.Error(w, `{"error":{"message":"Invalid request","code":400}}`, http.StatusBadRequest)
				}
				return
			}

			// Reset body for next handlers
			// Note: In production, you might want to cache the decoded body
			next.ServeHTTP(w, r)
		})
	}
}
