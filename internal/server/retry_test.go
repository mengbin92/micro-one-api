package server

import (
	"fmt"
	"testing"
	"time"
)

func TestParseRetryConfig_Defaults(t *testing.T) {
	cfg := parseRetryConfig(0, "", "", 0, nil)

	if cfg.maxAttempts != 3 {
		t.Errorf("maxAttempts = %d, want 3", cfg.maxAttempts)
	}
	if cfg.initialInterval != 500*time.Millisecond {
		t.Errorf("initialInterval = %v, want 500ms", cfg.initialInterval)
	}
	if cfg.maxInterval != 5*time.Second {
		t.Errorf("maxInterval = %v, want 5s", cfg.maxInterval)
	}
	if cfg.multiplier != 2.0 {
		t.Errorf("multiplier = %f, want 2.0", cfg.multiplier)
	}
	if !cfg.retryableStatus[429] || !cfg.retryableStatus[500] || !cfg.retryableStatus[502] || !cfg.retryableStatus[503] {
		t.Errorf("default retryable statuses missing, got %v", cfg.retryableStatus)
	}
}

func TestParseRetryConfig_Custom(t *testing.T) {
	cfg := parseRetryConfig(5, "1s", "10s", 3.0, []int{500, 503})

	if cfg.maxAttempts != 5 {
		t.Errorf("maxAttempts = %d, want 5", cfg.maxAttempts)
	}
	if cfg.initialInterval != time.Second {
		t.Errorf("initialInterval = %v, want 1s", cfg.initialInterval)
	}
	if cfg.maxInterval != 10*time.Second {
		t.Errorf("maxInterval = %v, want 10s", cfg.maxInterval)
	}
	if cfg.multiplier != 3.0 {
		t.Errorf("multiplier = %f, want 3.0", cfg.multiplier)
	}
	if !cfg.retryableStatus[500] || !cfg.retryableStatus[503] {
		t.Errorf("custom retryable statuses missing, got %v", cfg.retryableStatus)
	}
	if cfg.retryableStatus[429] {
		t.Errorf("429 should not be in custom retryable statuses")
	}
}

func TestIsRetryableError_UpstreamStatus(t *testing.T) {
	retryable := map[int]bool{429: true, 500: true, 502: true, 503: true}

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil error", nil, false},
		{"429 error", fmt.Errorf("upstream error: status=429, body=rate limited"), true},
		{"500 error", fmt.Errorf("upstream error: status=500, body=internal error"), true},
		{"502 error", fmt.Errorf("upstream error: status=502, body=bad gateway"), true},
		{"400 error", fmt.Errorf("upstream error: status=400, body=bad request"), false},
		{"401 error", fmt.Errorf("upstream error: status=401, body=unauthorized"), false},
		{"connection refused", fmt.Errorf("dial tcp 127.0.0.1:8080: connection refused"), true},
		{"timeout", fmt.Errorf("context deadline exceeded (timeout)"), true},
		{"EOF", fmt.Errorf("unexpected EOF"), true},
		{"dial tcp", fmt.Errorf("dial tcp 10.0.0.1:9001: connect: no route to host"), true},
		{"other error", fmt.Errorf("some random error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err, retryable)
			if got != tt.retryable {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestBackoffDuration(t *testing.T) {
	initial := 500 * time.Millisecond
	max := 5 * time.Second
	multiplier := 2.0

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 500 * time.Millisecond}, // 500ms * 2^0 = 500ms
		{1, 1 * time.Second},        // 500ms * 2^1 = 1s
		{2, 2 * time.Second},        // 500ms * 2^2 = 2s
		{3, 4 * time.Second},        // 500ms * 2^3 = 4s
		{4, 5 * time.Second},        // 500ms * 2^4 = 8s, capped at 5s
		{5, 5 * time.Second},        // capped
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			got := backoffDuration(tt.attempt, initial, max, multiplier)
			if got != tt.expected {
				t.Errorf("backoffDuration(%d) = %v, want %v", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestUpstreamStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"nil", nil, 0},
		{"429", fmt.Errorf("upstream error: status=429, body=..."), 429},
		{"500", fmt.Errorf("upstream error: status=500, body=..."), 500},
		{"no status", fmt.Errorf("some other error"), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upstreamStatus(tt.err)
			if got != tt.status {
				t.Errorf("upstreamStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}

func TestMapUpstreamError(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{429, 429},
		{502, 502},
		{503, 503},
		{504, 504},
		{400, 502}, // default -> BadGateway
		{500, 502}, // default -> BadGateway
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.input), func(t *testing.T) {
			got := mapUpstreamError(tt.input)
			if got != tt.expected {
				t.Errorf("mapUpstreamError(%d) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}
