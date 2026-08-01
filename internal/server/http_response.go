package server

import (
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"micro-one-api/pkg/errors"
)

// gatewayErrorMessage returns a generic, client-safe message for a status code,
// hiding internal/upstream error detail (relay-H2). The full error is still
// logged/metered by callers; only the HTTP response body is sanitized.
//
//   - 5xx and 502/503/504: a neutral "upstream service unavailable" so provider
//     names, file paths, and upstream response-body fragments are not echoed.
//   - 4xx: returned as-is (these are genuinely user-facing — auth, validation,
//     malformed request). Callers that pass a 4xx derived from an *upstream*
//     body should sanitize the message themselves before calling writeError.
func gatewayErrorMessage(statusCode int) string {
	if statusCode >= 500 {
		return "upstream service unavailable"
	}
	return ""
}

// sanitizeUpstreamError maps an upstream/internal error to a client-safe
// message for the given status code (relay-H2). For 5xx it returns the neutral
// gateway message. For 4xx it keeps a short reason derived from the status code
// (e.g. "upstream returned 429") rather than echoing the raw upstream error
// body, which may contain provider names, internal paths, or rate-limit detail.
func sanitizeUpstreamError(statusCode int, err error) string {
	if statusCode >= 500 {
		return gatewayErrorMessage(statusCode)
	}
	// 4xx from upstream: preserve the category but not the raw body.
	switch statusCode {
	case http.StatusTooManyRequests:
		return "upstream rate limited"
	case http.StatusUnauthorized:
		return "upstream authentication failed"
	case http.StatusForbidden:
		return "upstream denied request"
	default:
		return "upstream request error"
	}
}

func (s *HTTPServer) handleRelayPlanError(w http.ResponseWriter, err error) {
	// Check for structured errors
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, "no available channel")
		return
	}

	// Handle gRPC errors from downstream services
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		case codes.Unavailable:
			s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		default:
			if isChannelUnavailableMessage(st.Message()) {
				s.writeError(w, http.StatusServiceUnavailable, "no available channel")
				return
			}
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	if isChannelUnavailableMessage(err.Error()) {
		s.writeError(w, http.StatusServiceUnavailable, "no available channel")
		return
	}

	// Model not allowed (string match from biz layer)
	if strings.Contains(err.Error(), "not allowed") {
		s.writeError(w, http.StatusForbidden, "model not allowed")
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func isChannelUnavailableMessage(message string) bool {
	return strings.Contains(message, "no available channel") ||
		strings.Contains(message, "channel not found") ||
		strings.Contains(message, "subscription account not found")
}

func (s *HTTPServer) handleIdentityError(w http.ResponseWriter, err error) {
	// Check for structured errors first
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Handle gRPC errors
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		default:
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) handleChannelError(w http.ResponseWriter, err error) {
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = encodeJSON(w, map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
		},
	})
}

func (s *HTTPServer) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = encodeJSON(w, data)
}
