package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/requesttrace"
	"testing"
	"time"
)

func TestSettlementTelemetrySurvivesClaimWithoutChangingDigestOrReplayParent(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	store := NewSettlementTaskRepo(d)
	task := intakeTask()
	digest, err := task.PayloadDigest()
	require.NoError(t, err)
	task.Telemetry = requesttrace.Carrier{ObservationID: "first-observation", TraceParent: "00-11111111111111111111111111111111-1111111111111111-01", AcceptedAtMS: 123, QueuedAtMS: 124}
	withTelemetry, err := task.PayloadDigest()
	require.NoError(t, err)
	require.Equal(t, digest, withTelemetry)
	require.NoError(t, store.SaveTask(context.Background(), task))
	replay := *task
	replay.Telemetry = requesttrace.Carrier{ObservationID: "second-observation", TraceParent: "broken", AcceptedAtMS: 999, QueuedAtMS: 999}
	require.NoError(t, store.SaveTask(context.Background(), &replay))
	claimed, err := store.ListPending(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, task.Telemetry, claimed[0].Telemetry)
	require.NoError(t, store.MarkFailed(context.Background(), task.ReservationID, "retry", time.Now().Add(-time.Second)))
	claimed, err = store.ListPending(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.EqualValues(t, 123, claimed[0].Telemetry.AcceptedAtMS)
	require.Greater(t, claimed[0].Telemetry.QueuedAtMS, int64(124))
	require.Equal(t, task.Telemetry.TraceParent, claimed[0].Telemetry.TraceParent)
}
