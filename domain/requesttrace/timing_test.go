package requesttrace

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func validSummary() *TimingSummary {
	return &TimingSummary{Version: TimingVersion, ObservationID: uuid.NewString(),
		ObservationKind: "http", Protocol: "sse", Endpoint: "/v1/responses", ExecutionPath: "legacy",
		Outcome: "rejected", StartedAtMS: 1000, CollectionState: "not_recorded"}
}

func TestTimingSummaryPresenceAndClone(t *testing.T) {
	s := validSummary()
	finish := s.StartedAtMS
	s.HandlerFinishedAtMS = &finish
	s.HandlerDurationMS = new(float64) // a measured zero must survive JSON
	reservation := "reservation"
	s.Attempts = []TimingAttempt{{Number: 1, RequestID: "request", Outcome: "failed", ReservationID: &reservation}}
	require.NoError(t, s.Validate())
	data, err := jsonx.Marshal(s)
	require.NoError(t, err)
	var decoded TimingSummary
	require.NoError(t, jsonx.Unmarshal(data, &decoded))
	require.Nil(t, decoded.FirstContentMS)
	require.NotNil(t, decoded.HandlerDurationMS)
	require.Zero(t, *decoded.HandlerDurationMS)
	copy := s.Clone()
	*copy.HandlerDurationMS = 20
	*copy.Attempts[0].ReservationID = "other"
	require.Zero(t, *s.HandlerDurationMS)
	require.Equal(t, "reservation", *s.Attempts[0].ReservationID)
}

func TestTimingSummaryRejectsInvalidMetadata(t *testing.T) {
	for name, mutate := range map[string]func(*TimingSummary){
		"external_id":            func(s *TimingSummary) { s.ObservationID = "supplied-request-id" },
		"version":                func(s *TimingSummary) { s.Version = 2 },
		"negative_user":          func(s *TimingSummary) { s.UserID = -1 },
		"outcome":                func(s *TimingSummary) { s.Outcome = "arbitrary-error" },
		"route_query":            func(s *TimingSummary) { s.Endpoint += "?api_key=secret" },
		"nan":                    func(s *TimingSummary) { v := math.NaN(); s.FirstContentMS = &v },
		"negative":               func(s *TimingSummary) { v := -1.0; s.RPCDurationMS = &v },
		"complete_without_write": func(s *TimingSummary) { s.OutputComplete = true },
		"span_without_trace":     func(s *TimingSummary) { s.SpanID = "1111111111111111" },
		"invalid_trace":          func(s *TimingSummary) { s.OTelTraceID = "00000000000000000000000000000000" },
		"raw_missing_reason":     func(s *TimingSummary) { s.MissingReasons = []string{"upstream error with private body"} },
		"mismatched_kind":        func(s *TimingSummary) { s.ObservationKind = "ws_turn" },
		"unpaired_finish":        func(s *TimingSummary) { s.HandlerDurationMS = new(float64) },
		"unary_output": func(s *TimingSummary) {
			s.Protocol, s.ObservationKind = "grpc", "grpc"
			finish, duration := int64(1001), 1.0
			s.OutputFinishedAtMS, s.OutputDurationMS = &finish, &duration
		},
		"nonstream_ttft": func(s *TimingSummary) { s.Protocol = "http"; s.FirstContentMS = new(float64) },
		"duplicate_attempt": func(s *TimingSummary) {
			s.Attempts = []TimingAttempt{{Number: 1, RequestID: "a", Outcome: "failed"}, {Number: 1, RequestID: "b", Outcome: "success"}}
		},
		"invalid_phase": func(s *TimingSummary) { s.Stages = []TimingStage{{Phase: "request-id-123", Count: 1}} },
		"duplicate_phase": func(s *TimingSummary) {
			s.Stages = []TimingStage{{Phase: "quota.commit", Count: 1}, {Phase: "quota.commit", Count: 1}}
		},
		"invalid_settlement": func(s *TimingSummary) {
			s.SettlementSnapshot = &TimingSettlement{Status: "success", ObservedAtMS: 1001}
		},
	} {
		t.Run(name, func(t *testing.T) { s := validSummary(); mutate(s); require.Error(t, s.Validate()) })
	}
}
