// Package testutil supplies boundary fixtures for timing storage and transport tests.
package testutil

import (
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"micro-one-api/domain/requesttrace"
)

// MaximumTiming fills the current field/collection limits with independent
// allocations. '<' also exercises jsonx's six-byte HTML escaping expansion.
func MaximumTiming() *requesttrace.TimingSummary {
	field := func(n int) string { return strings.Repeat("<", n) }
	s := &requesttrace.TimingSummary{
		Version: 1, ObservationID: uuid.NewString(), ConnectionID: uuid.NewString(),
		ObservationKind: "ws_turn", Protocol: "websocket", UserID: 7,
		OTelTraceID: strings.Repeat("f", 32), SpanID: strings.Repeat("f", 16),
		RootRequestID: field(128), CompatibilityTraceID: field(128),
		Endpoint: "/" + field(255), ExecutionPath: field(32), Outcome: "incomplete",
		StartedAtMS: 1000, OutputFinishedAtMS: new(int64(math.MaxInt64)), TurnFinishedAtMS: new(int64(math.MaxInt64)),
		OutputDurationMS: new(float64(math.MaxFloat64)), TurnDurationMS: new(float64(math.MaxFloat64)),
		FirstContentMS: new(float64(math.MaxFloat64)), OutputComplete: true, OutputOutcome: "write_failed",
		CollectionState: "not_recorded", SettlementSnapshot: &requesttrace.TimingSettlement{Status: "not_reserved", ObservedAtMS: math.MaxInt64},
	}
	for i := range 128 {
		s.Attempts = append(s.Attempts, requesttrace.TimingAttempt{
			Number: int32(i + 1), RequestID: field(124) + fmt.Sprintf("%04d", i),
			ReservationID: new(field(60) + fmt.Sprintf("%04d", i)), SourceKind: "subscription",
			SourceID: math.MaxInt64, UpstreamModelID: field(255), Outcome: "incomplete",
		})
	}
	for i := range 32 {
		s.MissingReasons = append(s.MissingReasons, strings.Repeat("r", 124)+fmt.Sprintf("%04d", i))
	}
	for _, phase := range []string{
		"request.parse", "relay.plan", "auth.snapshot", "relay.attempt", "admission.check", "credential.resolve",
		"request.transform", "quota.reserve", "quota.commit", "quota.release", "upstream.http", "response.transform",
		"response.stream", "retry.backoff", "route.reselect", "usage.log", "health.report", "ws.accept",
		"ws.first_frame", "ws.pool.acquire", "ws.upstream.dial", "cache.l1",
	} {
		s.Stages = append(s.Stages, requesttrace.TimingStage{Phase: phase, DurationMS: math.MaxFloat64, Count: math.MaxInt32})
	}
	return s
}
