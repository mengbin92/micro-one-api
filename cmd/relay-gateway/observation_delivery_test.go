package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
)

type fakeLogObservationClient struct {
	logv1.LogServiceClient
	mu    sync.Mutex
	calls int
}

func (c *fakeLogObservationClient) RecordUsageObservation(_ context.Context, req *logv1.RecordUsageObservationRequest, _ ...grpc.CallOption) (*logv1.RecordUsageObservationResponse, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &logv1.RecordUsageObservationResponse{Accepted: true, Status: "stored", PayloadSha256: req.PayloadSha256}, nil
}

func (c *fakeLogObservationClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func retainedObservationEvent() evidence.SendEvent {
	send := evidence.SendRef{ObservationID: "obs-retained", SendID: "obs-retained/1/send-1", RequestID: "req-retained", RootRequestID: "root-retained",
		AttemptNumber: 1, UserID: 7, Purpose: evidence.PurposeUser, SourceKind: "channel", SourceID: 4, Model: "exact",
		Protocol: "openai_chat", Endpoint: "/v1/chat/completions"}
	buckets := [5]int64{10, 0, 0, 0, 2}
	record := &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, Status: "verified", Buckets: &buckets}, HTTPStatus: 200, Complete: true, Execution: "response_received"}
	return evidence.NewSendEvent(send, "terminal", 1, record, evidence.CaptureComplete, "", time.Unix(1700000000, 0))
}

// R10: turning the producer off must not strand evidence that is already on
// disk. The retained database is still opened and drained while
// WithUsageObservation refuses to create new events.
func TestObservationDeliveryDrainsRetainedQueueWhenProducerDisabled(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "false")
	path := filepath.Join(t.TempDir(), "observations.db")
	t.Setenv("RELAY_OBSERVATION_OUTBOX_PATH", path)

	d, closeDB, err := relaydata.NewObservationData(path, nil)
	require.NoError(t, err)
	repo, err := relaydata.NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	require.NoError(t, repo.Put(context.Background(), retainedObservationEvent()))
	closeDB()

	client := &fakeLogObservationClient{}
	delivery, cleanup, err := newObservationDelivery(client)
	require.NoError(t, err)
	require.NotNil(t, delivery, "a retained database must be drained even with the producer off")

	deadline := time.Now().Add(10 * time.Second)
	for client.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	require.Positive(t, client.callCount(), "the retained event must be delivered")
	cleanup()

	// The producer switch is off: no new sink may be attached to a request.
	ctx := relaybiz.WithUsageObservation(relaybiz.WithObservationDelivery(context.Background(), delivery), &relaybiz.Channel{ID: 4}, "req-new", "exact")
	require.Nil(t, evidence.ObservationSinkFrom(ctx))

	// The ACK tombstone proves the retained payload is gone, not merely
	// retried: reopening finds nothing pending.
	d, closeDB, err = relaydata.NewObservationData(path, nil)
	require.NoError(t, err)
	defer closeDB()
	reopened, err := relaydata.NewObservationOutboxRepo(d, path)
	require.NoError(t, err)
	stats, err := reopened.Stats(context.Background())
	require.NoError(t, err)
	require.Zero(t, stats.Pending)
}

func TestObservationDeliveryRejectsInvalidStorageLimits(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"invalid_headroom", "RELAY_OBSERVATION_MIN_FREE_BYTES", "invalid"},
		{"negative_headroom", "RELAY_OBSERVATION_MIN_FREE_BYTES", "-1"},
		{"zero_event_limit", "RELAY_OBSERVATION_MAX_PENDING_EVENTS", "0"},
		{"invalid_event_limit", "RELAY_OBSERVATION_MAX_PENDING_EVENTS", "oops"},
		{"negative_byte_limit", "RELAY_OBSERVATION_MAX_PENDING_BYTES", "-1"},
		{"overflow_byte_limit", "RELAY_OBSERVATION_MAX_PENDING_BYTES", "9223372036854775808"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
			t.Setenv("RELAY_OBSERVATION_OUTBOX_PATH", filepath.Join(t.TempDir(), "observations.db"))
			t.Setenv(tc.key, tc.value)
			_, cleanup, err := newObservationDelivery(&fakeLogObservationClient{})
			if cleanup != nil {
				cleanup()
			}
			require.Error(t, err, "invalid storage guard must not silently use defaults")
		})
	}
}
