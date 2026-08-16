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
// Every branch returns a non-empty message: the previous "4xx returns empty"
// behavior leaked an empty {"error":{"message":""}} body to clients on
// billing/quota failures (HTTP 402), which surfaced as an opaque
// "unexpected status 402 Payment Required" in SDK clients.
//
//   - 5xx: a neutral "upstream service unavailable" so provider names, file
//     paths, and upstream response-body fragments are not echoed.
//   - 4xx: a short, client-safe reason for the status category (never empty).
func gatewayErrorMessage(statusCode int) string {
	if statusCode >= 500 {
		return "upstream service unavailable"
	}
	switch statusCode {
	case http.StatusPaymentRequired:
		return "insufficient quota"
	case http.StatusTooManyRequests:
		return "rate limited"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not found"
	case http.StatusBadRequest:
		return "bad request"
	case http.StatusServiceUnavailable:
		return "service unavailable"
	default:
		if statusCode >= 400 {
			return "request error"
		}
		return "ok"
	}
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
		// Channel-selection dead-ends now arrive as NotFound/FailedPrecondition
		// (pkg/errors GRPCStatus); check the message BEFORE the code switch so
		// they stay 503 instead of falling into the generic NotFound→401 below.
		if isChannelUnavailableMessage(st.Message()) {
			s.writeError(w, http.StatusServiceUnavailable, "no available channel")
			return
		}
		switch st.Code() {
		case codes.Unauthenticated, codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		case codes.FailedPrecondition:
			// ROUTE_DEAD_END crosses gRPC as FailedPrecondition (pkg/errors
			// GRPCStatus). Its message ("circuit-opened...", "none are
			// schedulable") does NOT match isChannelUnavailableMessage, so it
			// must be handled by code, not message: the request is valid but
			// no upstream is schedulable right now — a transient 503, not an
			// internal error. Keep it 503.
			s.writeError(w, http.StatusServiceUnavailable, "no available channel")
		case codes.Unavailable:
			s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		default:
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
		case codes.Unauthenticated, codes.NotFound:
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
