package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	logv1 "micro-one-api/api/log/v1"
)

func TestRequestTimingProtoRoundTripPreservesPresence(t *testing.T) {
	finish, duration, zero := int64(1001), 1.25, 0.0
	reservation := "reservation"
	p := &logv1.RequestTimingSummary{
		Version: 1, ObservationId: uuid.NewString(), ObservationKind: "ws_turn", UserId: 7,
		RootRequestId: "root", Endpoint: "/v1/responses", Protocol: "websocket", ExecutionPath: "legacy",
		Outcome: "incomplete", StartedAtMs: 1000, OutputFinishedAtMs: &finish, OutputDurationMs: &duration,
		FirstContentMs: &zero, CollectionState: "partial", MissingReasons: []string{"client_write_failed"},
		Attempts: []*logv1.RequestTimingAttempt{
			{Number: 1, RequestId: "request-1", Outcome: "rejected"},
			{Number: 2, RequestId: "request-2", ReservationId: &reservation, Outcome: "incomplete"},
		},
		Stages:             []*logv1.RequestTimingStage{{Phase: "quota.commit", Count: 1, DurationMs: 0}},
		SettlementSnapshot: &logv1.RequestTimingSettlement{Status: "enqueued", ObservedAtMs: 1001},
	}
	// Serialize through protobuf first to exercise actual optional presence.
	encoded, err := proto.Marshal(p)
	require.NoError(t, err)
	var transported logv1.RequestTimingSummary
	require.NoError(t, proto.Unmarshal(encoded, &transported))
	do, err := convertRequestTiming(&transported)
	require.NoError(t, err)
	output := requestTimingToProto(do)
	require.True(t, proto.Equal(p, output))
	require.NotNil(t, output.FirstContentMs)
	require.Nil(t, output.HandlerDurationMs)
	require.Nil(t, output.Attempts[0].ReservationId)
	*output.FirstContentMs = 20
	*output.Attempts[1].ReservationId = "changed"
	require.Zero(t, *do.FirstContentMS)
	require.Equal(t, "reservation", *do.Attempts[1].ReservationID)
}

func TestRequestTimingProtoRejectsInvalidNestedValues(t *testing.T) {
	for _, p := range []*logv1.RequestTimingSummary{
		{},
		{Attempts: []*logv1.RequestTimingAttempt{nil}},
		{Stages: []*logv1.RequestTimingStage{nil}},
	} {
		_, err := convertRequestTiming(p)
		require.Error(t, err)
	}
	do, err := convertRequestTiming(nil)
	require.NoError(t, err)
	require.Nil(t, do)
	require.Nil(t, requestTimingToProto(nil))
}
