package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

var ErrStreamHeaderTimeout = errors.New("upstream response header timeout")
var ErrRequestBudget = errors.New("relay total request budget exhausted")

// StreamTimeouts separates first response and sliding network activity limits.
// Total duration belongs to the caller context, shared across all attempts.
type StreamTimeouts struct{ Header, Idle time.Duration }

func TimeoutFromEnv(name string, fallback time.Duration, allowZero bool) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 || (!allowZero && value == 0) {
		return 0, fmt.Errorf("%s must be %s duration", name, map[bool]string{true: "a non-negative", false: "a positive"}[allowZero])
	}
	return value, nil
}
func StreamTimeoutsFromEnv(fallback time.Duration) (StreamTimeouts, error) {
	if fallback <= 0 {
		fallback = time.Minute
	}
	header, err := TimeoutFromEnv("RELAY_STREAM_HEADER_TIMEOUT", fallback, false)
	if err != nil {
		return StreamTimeouts{}, err
	}
	idle, err := TimeoutFromEnv("RELAY_STREAM_IDLE_TIMEOUT", fallback, false)
	if err != nil {
		return StreamTimeouts{}, err
	}
	return StreamTimeouts{Header: header, Idle: idle}, nil
}
func ValidateTimeoutEnvironment() error {
	if _, err := StreamTimeoutsFromEnv(time.Minute); err != nil {
		return err
	}
	for _, item := range []struct {
		name string
		zero bool
	}{{"RELAY_STREAM_TOTAL_TIMEOUT", true}, {"RELAY_REQUEST_TIMEOUT", false}, {"RELAY_CONNECT_TIMEOUT", false}} {
		if _, err := TimeoutFromEnv(item.name, time.Minute, item.zero); err != nil {
			return err
		}
	}
	return nil
}

// StreamHTTPClient preserves the configured proxy/redirect/transport policy,
// removes Client.Timeout and applies the same byte-idle guard to adaptor paths.
func StreamHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return NewStreamHTTPClient(time.Minute)
	}
	if _, ok := client.Transport.(*streamTimeoutRoundTripper); ok && client.Timeout == 0 {
		return client
	}
	cfg, err := StreamTimeoutsFromEnv(client.Timeout)
	if err != nil {
		panic(err)
	}
	return StreamHTTPClientWithTimeouts(client, cfg)
}
func StreamHTTPClientWithTimeouts(client *http.Client, cfg StreamTimeouts) *http.Client {
	if cfg.Header <= 0 || cfg.Idle <= 0 {
		panic("stream header and idle timeouts must be positive")
	}
	cp := *client
	cp.Timeout = 0
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if previous, ok := base.(*streamTimeoutRoundTripper); ok {
		base = previous.base
	}
	cp.Transport = &streamTimeoutRoundTripper{base: base, idleTimeout: cfg.Idle, headerTimeout: cfg.Header}
	return &cp
}

// StreamFailureReason uses a finite vocabulary and never labels URLs/accounts.
func StreamFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrStreamIdleTimeout):
		return "idle_timeout"
	case errors.Is(err, ErrStreamHeaderTimeout):
		return "header_timeout"
	case errors.Is(err, ErrRequestBudget):
		return "total_budget"
	case errors.Is(err, context.Canceled):
		return "client_cancel"
	case errors.Is(err, context.DeadlineExceeded):
		return "caller_deadline"
	default:
		return "transport"
	}
}
