package server

import (
	"context"
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
)

const responsesUpstreamClientErrorFallbackMessage = "upstream rejected the request"

// logResponsesUpstreamFailure records only bounded, low-cardinality failure
// metadata. In particular, it never logs request headers/body or the upstream
// response body carried by UpstreamHTTPError.
func logResponsesUpstreamFailure(ctx context.Context, endpoint string, stream bool, phase string, err error) {
	status := relaybiz.UpstreamStatus(err)
	executionPath := relayExecutionPathLegacy
	if observation, ok := ctx.Value(relayExecutionObservationKey{}).(*relayExecutionObservation); ok {
		observedEndpoint, observedStream, observedPath, _ := observation.snapshot()
		if observedEndpoint != "" {
			endpoint = observedEndpoint
		}
		if observedStream != relayStreamUnknown {
			stream = observedStream == strconv.FormatBool(true)
		}
		if observedPath != "" {
			executionPath = observedPath
		}
	}
	applogger.Log.Warn("responses upstream request failed",
		zap.Int("upstream_status", status),
		zap.String("endpoint", endpoint),
		zap.Bool("stream", stream),
		zap.String("execution_path", executionPath),
		zap.String("phase", phase),
		zap.String("error_category", responsesUpstreamErrorCategory(status)),
	)
}

func responsesUpstreamErrorCategory(status int) string {
	switch {
	case status == 0:
		return "transport"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "upstream_auth"
	case status == http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case status == http.StatusUnsupportedMediaType || status == http.StatusUnprocessableEntity:
		return "request_compatibility"
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		return "endpoint_capability"
	case status == http.StatusTooManyRequests:
		return "rate_limit"
	case status >= http.StatusInternalServerError:
		return "upstream_service"
	case status >= http.StatusBadRequest:
		return "client_request"
	default:
		return "unknown"
	}
}

// writeResponsesUpstreamError follows sub2api's deterministic-client-error
// behavior: preserve actionable OpenAI error metadata for request errors, but
// keep upstream credentials and raw response bodies private.
func (s *HTTPServer) writeResponsesUpstreamError(w http.ResponseWriter, err error) {
	upstreamStatus := relaybiz.UpstreamStatus(err)
	clientStatus := mapUpstreamError(upstreamStatus)
	if clientStatus == http.StatusRequestEntityTooLarge {
		s.writeError(w, clientStatus, "request payload is too large")
		return
	}
	if clientStatus != http.StatusBadRequest {
		s.writeError(w, clientStatus, "upstream service error")
		return
	}

	payload := map[string]any{
		"type":    "invalid_request_error",
		"message": responsesUpstreamClientErrorFallbackMessage,
	}
	var upstreamErr *relayprovider.UpstreamHTTPError
	if stderrors.As(err, &upstreamErr) {
		var body struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
				Param   string `json:"param"`
			} `json:"error"`
		}
		if jsonx.Unmarshal(upstreamErr.Body, &body) == nil {
			if value := sanitizeResponsesErrorField(body.Error.Type); value != "" {
				payload["type"] = value
			}
			if value := sanitizeResponsesErrorField(body.Error.Code); value != "" {
				payload["code"] = value
			}
			if value := sanitizeResponsesErrorField(body.Error.Param); value != "" {
				payload["param"] = value
			}
			if value := sanitizeResponsesErrorField(body.Error.Message); value != "" {
				payload["message"] = value
			}
		}
	}
	s.writeJSON(w, clientStatus, map[string]any{"error": payload})
}

func sanitizeResponsesErrorField(value string) string {
	return applogger.SanitizeAndTruncate(strings.TrimSpace(value), 2048)
}
