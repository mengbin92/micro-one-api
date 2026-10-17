package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/requesttrace"
	"path/filepath"
	"testing"
)

func TestExecutionTelemetryRestartsWithoutChangingFinancialEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	d, closeDB, err := NewExecutionData(path, nil)
	require.NoError(t, err)
	r := NewExecutionOutboxRepo(d, path)
	event := outboxIntent()
	_, digest, err := event.Encode()
	require.NoError(t, err)
	carrier := requesttrace.Carrier{ObservationID: "original-observation", TraceParent: "00-11111111111111111111111111111111-1111111111111111-01", AcceptedAtMS: 100, QueuedAtMS: 100}
	ctx, valid := requesttrace.Restore(context.Background(), carrier)
	require.True(t, valid)
	require.NoError(t, r.Admit(ctx, event))
	require.NoError(t, r.Put(context.Background(), event))
	closeDB()
	d, closeDB, err = NewExecutionData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	r = NewExecutionOutboxRepo(d, path)
	pending, err := r.Pending(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, event, pending[0].Event)
	require.Equal(t, digest, pending[0].Digest)
	require.Equal(t, carrier.ObservationID, pending[0].Telemetry.ObservationID)
	require.Equal(t, carrier.TraceParent, pending[0].Telemetry.TraceParent)
	require.EqualValues(t, 100, pending[0].Telemetry.AcceptedAtMS)
}
