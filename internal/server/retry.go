package server

import (
	"math"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
)

// retryConfig holds parsed retry parameters for use in retry loops.
type retryConfig struct {
	maxAttempts     int
	initialInterval time.Duration
	maxInterval     time.Duration
	multiplier      float64
	retryableStatus map[int]bool
}

// parseRetryConfig reads retry config with defaults applied.
func parseRetryConfig(maxAttempts int, initialInterval, maxInterval string, multiplier float64, retryableStatus []int) *retryConfig {
	cfg := &retryConfig{
		maxAttempts:     maxAttempts,
		multiplier:      multiplier,
		retryableStatus: make(map[int]bool),
	}

	if cfg.maxAttempts <= 0 {
		cfg.maxAttempts = 3
	}
	if cfg.multiplier <= 0 {
		cfg.multiplier = 2.0
	}

	d, err := time.ParseDuration(initialInterval)
	if err != nil {
		d = 500 * time.Millisecond
	}
	cfg.initialInterval = d

	d, err = time.ParseDuration(maxInterval)
	if err != nil {
		d = 5 * time.Second
	}
	cfg.maxInterval = d

	statuses := retryableStatus
	if len(statuses) == 0 {
		statuses = []int{429, 500, 502, 503}
	}
	for _, s := range statuses {
		cfg.retryableStatus[s] = true
	}

	return cfg
}

// isRetryableStatus checks if the given HTTP status code is retryable.
func isRetryableStatus(statusCode int, retryable map[int]bool) bool {
	return retryable[statusCode]
}

// isRetryableError checks if an error from the provider is retryable.
// It inspects the error message for upstream HTTP status codes.
func isRetryableError(err error, retryable map[int]bool) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Parse "upstream error: status=XXX, body=..." pattern
	if _, after, ok := strings.Cut(msg, "status="); ok {
		statusStr := after
		if end := strings.Index(statusStr, ","); end >= 0 {
			statusStr = statusStr[:end]
		}
		var status int
		for _, c := range statusStr {
			if c >= '0' && c <= '9' {
				status = status*10 + int(c-'0')
			} else {
				break
			}
		}
		if status > 0 {
			return isRetryableStatus(status, retryable)
		}
	}
	// Network errors (connection refused, timeout, etc.) are retryable
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "dial tcp") {
		return true
	}
	return false
}

// backoffDuration calculates the sleep duration for the given attempt (0-indexed).
func backoffDuration(attempt int, initial time.Duration, max time.Duration, multiplier float64) time.Duration {
	d := min(time.Duration(float64(initial)*math.Pow(multiplier, float64(attempt))), max)
	return d
}

// logRetry logs a retry attempt.
func logRetry(attempt int, maxAttempts int, wait time.Duration, err error) {
	applogger.Log.Warn("upstream call failed, retrying",
		zap.Int("attempt", attempt+1),
		zap.Int("max_attempts", maxAttempts),
		zap.Duration("wait", wait),
		zap.Error(err),
	)
}

// upstreamStatus extracts the HTTP status code from an upstream error message.
// Returns 0 if not an upstream HTTP error.
func upstreamStatus(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, "status="); ok {
		statusStr := after
		if end := strings.Index(statusStr, ","); end >= 0 {
			statusStr = statusStr[:end]
		}
		var status int
		for _, c := range statusStr {
			if c >= '0' && c <= '9' {
				status = status*10 + int(c-'0')
			} else {
				break
			}
		}
		return status
	}
	return 0
}

// retryableStatusCodes returns the set of retryable status codes from http status int.
// Used for the writeError path to map upstream errors to proper HTTP responses.
func mapUpstreamError(statusCode int) int {
	switch statusCode {
	case http.StatusBadRequest:
		return http.StatusBadRequest
	case http.StatusPaymentRequired:
		return http.StatusPaymentRequired
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusBadGateway:
		return http.StatusBadGateway
	case http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable
	case http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
