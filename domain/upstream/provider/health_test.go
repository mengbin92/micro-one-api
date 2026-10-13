package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHealthDisposition(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want HealthOutcome
	}{
		{"success", nil, HealthSuccess},
		{"rate limit", &UpstreamHTTPError{StatusCode: 429}, HealthNeutral},
		{"locked", &UpstreamHTTPError{StatusCode: 423}, HealthNeutral},
		{"overloaded", &UpstreamHTTPError{StatusCode: 529}, HealthNeutral},
		{"bad credential", &UpstreamHTTPError{StatusCode: 401}, HealthNeutral},
		{"bad request", &UpstreamHTTPError{StatusCode: 400}, HealthNeutral},
		{"service failure", &UpstreamHTTPError{StatusCode: 503}, HealthFailure},
		{"upstream deadline", context.DeadlineExceeded, HealthFailure},
		{"cancelled", context.Canceled, HealthNeutral},
		{"transport", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, HealthFailure},
		{"truncated response", fmt.Errorf("read: %w", io.ErrUnexpectedEOF), HealthFailure},
		{"local dependency timeout", BeforeForward(context.DeadlineExceeded), HealthNeutral},
		{"local dependency transport", BeforeForward(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("billing unavailable")}), HealthNeutral},
		{"unattributed error", errors.New("dial tcp billing: timeout"), HealthNeutral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HealthDisposition(context.Background(), tc.err); got != tc.want {
				t.Fatalf("outcome = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCallerDeadlineIsNotUpstreamTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	for _, err := range []error{context.DeadlineExceeded, &UpstreamHTTPError{StatusCode: 502}} {
		if got := HealthDisposition(ctx, err); got != HealthNeutral {
			t.Fatalf("caller deadline outcome = %v, want neutral", got)
		}
	}
}

func TestBeforeForwardPreservesCause(t *testing.T) {
	cause := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("billing unavailable")}
	err := BeforeForward(cause)
	if !errors.Is(err, cause) || BeforeForward(err) != err {
		t.Fatal("stage marker must preserve the cause and be idempotent")
	}
}

func TestInvalidConfigurationDistinguishesDependencyFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cause         error
		configuration bool
	}{
		{"invalid URL", errors.New("unsupported URL scheme"), true},
		{"credential resolver dial", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("channel unavailable")}, false},
		{"credential resolver timeout", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := InvalidConfiguration(tc.cause)
			var configuration *ConfigurationError
			if !errors.Is(err, tc.cause) || !IsPreForwardError(err) || errors.As(err, &configuration) != tc.configuration {
				t.Fatalf("configuration=%v beforeforward=%v cause=%v", configuration, IsPreForwardError(err), err)
			}
			if HealthDisposition(context.Background(), err) != HealthNeutral {
				t.Fatal("local configuration/dependency failure became availability evidence")
			}
		})
	}
}

func TestConfigurationRevisionIsUnambiguousAndChangesWithCredentials(t *testing.T) {
	first := ConfigurationRevision("https://upstream/v1", "credential-a")
	if first != ConfigurationRevision("https://upstream/v1", "credential-a") || first == ConfigurationRevision("https://upstream/v1", "credential-b") || ConfigurationRevision("ab", "c") == ConfigurationRevision("a", "bc") {
		t.Fatal("configuration identity must be stable, credential-sensitive and preserve field boundaries")
	}
}

func TestParseRetryAfterBoundsUntrustedUpstreamHeader(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 0},
		{"invalid", 0},
		{"-5", 0},
		{"0", 0},
		{"30", 30 * time.Second},
		{"99999999999999999999999999999", 0},
		{"9223372036854775807", 5 * time.Minute},
		{"600", 5 * time.Minute},
		{now.Add(45 * time.Second).Format(http.TimeFormat), 45 * time.Second},
		{now.Add(10 * time.Minute).Format(http.TimeFormat), 5 * time.Minute},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := ParseRetryAfter(tc.value, now); got != tc.want {
				t.Fatalf("ParseRetryAfter(%q)=%v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
