package service

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
)

type fakeLogClient struct {
	logv1.LogServiceClient
	request  *logv1.RecordUsageObservationRequest
	response *logv1.RecordUsageObservationResponse
	err      error
}

func (c *fakeLogClient) RecordUsageObservation(_ context.Context, req *logv1.RecordUsageObservationRequest, _ ...grpc.CallOption) (*logv1.RecordUsageObservationResponse, error) {
	c.request = req
	return c.response, c.err
}

func observationSendEvent() evidence.SendEvent {
	send := evidence.SendRef{ObservationID: "obs-1", SendID: "obs-1/1/send", RequestID: "req-1", RootRequestID: "root-1",
		AttemptNumber: 1, UserID: 7, Purpose: evidence.PurposeUser, SourceKind: "channel", SourceID: 3, Model: "exact",
		Protocol: "openai_chat", Endpoint: "/v1/chat/completions"}
	record := &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, Status: "missing", Reason: "usage_absent"}, HTTPStatus: 200, Complete: true}
	return evidence.NewSendEvent(send, "terminal", 1, record, evidence.CaptureComplete, "usage_absent", time.Unix(1700000000, 0))
}

func TestUsageObservationSenderSendsCanonicalPayload(t *testing.T) {
	event := observationSendEvent()
	_, digest, err := event.Encode()
	require.NoError(t, err)
	client := &fakeLogClient{response: &logv1.RecordUsageObservationResponse{Accepted: true, Status: "stored", PayloadSha256: digest}}
	sender := NewUsageObservationSender(client)

	receipt, err := sender.Send(context.Background(), event)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.Equal(t, "stored", receipt.Status)
	require.Equal(t, event.Send.SendID, client.request.SendId)
	require.Equal(t, event.Send.ObservationID, client.request.ObservationId)
	require.Equal(t, event.Category, client.request.Category)
	require.Equal(t, digest, client.request.PayloadSha256)
	require.Equal(t, digest, receipt.Digest, "the receiver must echo the digest it recomputed")
}

func TestUsageObservationSenderMapsConflict(t *testing.T) {
	client := &fakeLogClient{err: errors.Conflict("USAGE_OBSERVATION_CONFLICT", "same key, different payload")}
	sender := NewUsageObservationSender(client)
	_, err := sender.Send(context.Background(), observationSendEvent())
	require.ErrorIs(t, err, relaybiz.ErrObservationConflict)
}

func TestUsageObservationSenderMapsNonDurableStorage(t *testing.T) {
	storageErr := errors.InternalServer(logv1.UsageObservationErrorReason_USAGE_OBSERVATION_UNAVAILABLE.String(), "storage is not durable")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"typed", storageErr}, {"grpc", storageErr.GRPCStatus().Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeLogClient{err: tc.err}
			_, err := NewUsageObservationSender(client).Send(context.Background(), observationSendEvent())
			require.ErrorIs(t, err, relaybiz.ErrObservationStorageUnavailable)
		})
	}
	otherErr := errors.InternalServer("OTHER_FAILURE", "temporary failure").GRPCStatus().Err()
	_, err := NewUsageObservationSender(&fakeLogClient{err: otherErr}).Send(context.Background(), observationSendEvent())
	require.ErrorIs(t, err, otherErr)
	require.NotErrorIs(t, err, relaybiz.ErrObservationStorageUnavailable)
}
