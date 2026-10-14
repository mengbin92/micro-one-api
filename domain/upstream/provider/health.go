package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HealthOutcome describes availability evidence, independently of retry policy.
type HealthOutcome uint8

const (
	HealthNeutral HealthOutcome = iota
	HealthSuccess
	HealthFailure
)

// PreForwardError preserves failures in local preparation or other dependencies.
// They must never be attributed to an upstream that has not been called.
type PreForwardError struct{ Err error }

type ConfigurationError struct{ Err error }

func (e *ConfigurationError) Error() string { return e.Err.Error() }
func (e *ConfigurationError) Unwrap() error { return e.Err }

func InvalidConfiguration(err error) error {
	if err == nil {
		return nil
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return BeforeForward(err)
	}
	return BeforeForward(&ConfigurationError{Err: err})
}

// ConfigurationRevision fingerprints execution configuration without exposing credentials.
func ConfigurationRevision(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Observation carries the selected configuration and half-open lease back to its owner.
type Observation struct {
	SourceKind, ConfigurationRevision, ProbeID, ID string
	SourceID                                       int64
	StartedAt                                      time.Time
	Outcome                                        HealthOutcome
	FailureKind, UpstreamModelID, Error            string
	ResponseTime                                   int64
	RetryAfter                                     time.Duration
}

func (e *PreForwardError) Error() string { return e.Err.Error() }
func (e *PreForwardError) Unwrap() error { return e.Err }

func BeforeForward(err error) error {
	if err == nil || IsPreForwardError(err) {
		return err
	}
	return &PreForwardError{Err: err}
}

func IsPreForwardError(err error) bool {
	var marker interface{ BeforeUpstream() bool }
	if errors.As(err, &marker) && marker.BeforeUpstream() {
		return true
	}
	var target *PreForwardError
	return errors.As(err, &target)
}

func (e *UpstreamHTTPError) UpstreamStatusCode() int { return e.StatusCode }

// ParseRetryAfter bounds untrusted upstream headers before converting seconds to duration.
func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(min(seconds, 300)) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return min(deadline.Sub(now), 5*time.Minute)
	}
	return 0
}

// HealthDisposition excludes caller deadlines, local failures, request errors
// and capacity rejection from both the failure count and the success denominator.
func HealthDisposition(ctx context.Context, err error) HealthOutcome {
	if err == nil {
		return HealthSuccess
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || IsPreForwardError(err) {
		return HealthNeutral
	}
	var httpError interface{ UpstreamStatusCode() int }
	if errors.As(err, &httpError) {
		code := httpError.UpstreamStatusCode()
		if code > 0 {
			if code < 500 || code == 529 {
				return HealthNeutral
			}
			return HealthFailure
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return HealthFailure
	}
	return HealthNeutral
}
