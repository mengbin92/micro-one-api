package errors

import (
	"errors"
	"testing"
)

// TestMapChannelError_RouteDeadEnd (🟡#7): the routed/circuit/saturated
// dead-end messages surface as ROUTE_DEAD_END (503), distinct from a plain
// CHANNEL_NOT_FOUND (also 503 but a different reason string) and from a
// MODEL_NOT_FOUND (404). This is the observability contract: operators can
// tell "all upstreams tripped their circuit" from "nobody serves this model".
func TestMapChannelError_RouteDeadEnd(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
		wantCode   int
	}{
		{"circuit-opened", errors.New("all subscription accounts serving \"gpt-5\" are circuit-opened or saturated; try again after the circuit window"), ReasonRouteDeadEnd, 503},
		{"routed-none-schedulable", errors.New("model routing matched 2 account(s) for \"gpt-5\" but none are schedulable"), ReasonRouteDeadEnd, 503},
		{"plain channel not found", errors.New("channel not found"), ReasonChannelNotFound, 503},
		{"subscription account not found", errors.New("subscription account not found"), ReasonChannelNotFound, 503},
		{"plain model not found", errors.New("model not found"), ReasonModelNotFound, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := MapChannelError(tc.err)
			var e *Error
			if !errors.As(mapped, &e) {
				t.Fatalf("mapped is not *Error: %T", mapped)
			}
			if e.Reason != tc.wantReason {
				t.Fatalf("reason = %s, want %s", e.Reason, tc.wantReason)
			}
			if code := GetHTTPStatusCode(e.Reason); code != tc.wantCode {
				t.Fatalf("http code = %d, want %d", code, tc.wantCode)
			}
		})
	}
}

// TestIsServiceUnavailable_IncludesRouteDeadEnd (🟡#7): the routed dead-end
// is a transient unavailability, so IsServiceUnavailable must report true so
// callers can retry instead of failing permanently.
func TestIsServiceUnavailable_IncludesRouteDeadEnd(t *testing.T) {
	mapped := MapChannelError(errors.New("all subscription accounts serving \"gpt-5\" are circuit-opened or saturated; try again after the circuit window"))
	if !IsServiceUnavailable(mapped) {
		t.Fatal("ROUTE_DEAD_END should be treated as service-unavailable (retryable)")
	}
}
