package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

type stubObservationRepo struct {
	rows      []*UsageObservationRow
	duplicate bool
	err       error
}

func (r *stubObservationRepo) Record(_ context.Context, row *UsageObservationRow) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	r.rows = append(r.rows, row)
	return r.duplicate, nil
}
func (r *stubObservationRepo) ListUsageObservations(context.Context, UsageObservationFilter) ([]*UsageObservationRow, string, error) {
	return r.rows, "", nil
}
func (r *stubObservationRepo) GetUsageObservation(context.Context, string) (*UsageObservationRow, error) {
	if len(r.rows) == 0 {
		return nil, ErrUsageObservationNotFound
	}
	return r.rows[0], nil
}
func (r *stubObservationRepo) DeleteObservationsBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// relayCapabilityContext runs the real service-identity interceptor so the
// writer capability is proven, not simulated.
func relayCapabilityContext(t *testing.T) context.Context {
	t.Helper()
	verifier, err := serviceidentity.NewVerifier(map[string]string{"relay": "relay-private"}, "legacy")
	require.NoError(t, err)
	interceptor := xgrpc.ServiceIdentityUnaryInterceptor(verifier)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer relay-private"))
	var out context.Context
	_, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: RecordUsageObservationMethod}, func(ctx context.Context, _ any) (any, error) {
		out = ctx
		return nil, nil
	})
	require.NoError(t, err)
	require.True(t, serviceidentity.HasSystemCapability(out, RecordUsageObservationMethod))
	return out
}

func observationEnvelope(t *testing.T) (eventID, sendID, observationID, digest string, payload []byte) {
	t.Helper()
	send := evidence.SendRef{ObservationID: "obs-1", SendID: "obs-1/1/send-1", RequestID: "req-1", RootRequestID: "root-1",
		AttemptNumber: 1, UserID: 7, Purpose: evidence.PurposeUser, SourceKind: "channel", SourceID: 4, Model: "exact",
		Protocol: "openai_chat", Endpoint: "/v1/chat/completions"}
	buckets := [5]int64{10, 0, 0, 0, 2}
	record := &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, Status: "verified", Buckets: &buckets}, HTTPStatus: 200, Complete: true, Execution: "response_received"}
	event := evidence.NewSendEvent(send, "terminal", 1, record, evidence.CaptureComplete, "", time.Unix(1700000000, 0))
	encoded, digest, err := event.Encode()
	require.NoError(t, err)
	return event.ID, send.SendID, send.ObservationID, digest, encoded
}

func TestRecordUsageObservationValidatesDigestAndIdentity(t *testing.T) {
	repo := &stubObservationRepo{}
	uc := NewUsageObservationUsecase(repo)
	ctx := relayCapabilityContext(t)
	eventID, sendID, observationID, digest, payload := observationEnvelope(t)

	receipt, err := uc.Record(ctx, eventID, sendID, observationID, "terminal", digest, 1, payload)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.Equal(t, "stored", receipt.Status)
	require.Equal(t, digest, receipt.Digest)
	require.Len(t, repo.rows, 1)
	require.Equal(t, int64(7), repo.rows[0].UserID, "ownership comes from the signed envelope, not the caller")
	require.Equal(t, "verified", repo.rows[0].Status)

	_, err = uc.Record(ctx, eventID, sendID, observationID, "terminal", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1, payload)
	require.ErrorIs(t, err, ErrInvalidUsageObservation, "a forged digest is rejected")

	_, err = uc.Record(ctx, eventID, sendID, observationID, "terminal", digest, 2, payload)
	require.ErrorIs(t, err, ErrInvalidUsageObservation, "dto fields must match the signed envelope")

	_, err = uc.Record(ctx, eventID, sendID, observationID, "checkpoint", digest, 1, payload)
	require.ErrorIs(t, err, ErrInvalidUsageObservation)

	_, err = uc.Record(ctx, eventID, sendID, observationID, "terminal", digest, 1, nil)
	require.ErrorIs(t, err, ErrUsageObservationLimit)

	repo.duplicate = true
	receipt, err = uc.Record(ctx, eventID, sendID, observationID, "terminal", digest, 1, payload)
	require.NoError(t, err)
	require.True(t, receipt.Duplicate)
	require.Equal(t, "duplicate", receipt.Status)

	repo.err = ErrUsageObservationConflict
	_, err = uc.Record(ctx, eventID, sendID, observationID, "terminal", digest, 1, payload)
	require.ErrorIs(t, err, ErrUsageObservationConflict)
}

func TestRecordUsageObservationRequiresRelayCapability(t *testing.T) {
	uc := NewUsageObservationUsecase(&stubObservationRepo{})
	eventID, sendID, observationID, digest, payload := observationEnvelope(t)
	_, err := uc.Record(context.Background(), eventID, sendID, observationID, "terminal", digest, 1, payload)
	require.True(t, errors.Is(err, authorization.ErrDenied), "an unclassified caller must be denied: %v", err)

	userCtx := authorization.WithCredential(relayCapabilityContext(t), "user-session")
	_, err = uc.Record(userCtx, eventID, sendID, observationID, "terminal", digest, 1, payload)
	require.True(t, errors.Is(err, authorization.ErrDenied), "a user session cannot use the writer capability")
}

func TestListUsageObservationsRequiresWindowAndScope(t *testing.T) {
	uc := NewUsageObservationUsecase(&stubObservationRepo{})
	legacy := authorization.WithLegacyAuthorization(context.Background(), true)
	_, _, err := uc.ListUsageObservations(legacy, UsageObservationFilter{})
	require.Error(t, err, "an unbounded window must be rejected")

	_, _, err = uc.ListUsageObservations(legacy, UsageObservationFilter{StartTimeMS: 1000, EndTimeMS: 2000})
	require.True(t, errors.Is(err, authorization.ErrDenied), "legacy read without a user filter must be denied")

	_, _, err = uc.ListUsageObservations(legacy, UsageObservationFilter{StartTimeMS: 1000, EndTimeMS: 2000, UserID: 7})
	require.NoError(t, err)
}

func TestUsageObservationErrorReasonsAreDeclared(t *testing.T) {
	require.Equal(t, logv1.UsageObservationErrorReason_INVALID_USAGE_OBSERVATION.String(), errors.FromError(ErrInvalidUsageObservation).Reason)
	require.Equal(t, logv1.UsageObservationErrorReason_USAGE_OBSERVATION_CONFLICT.String(), errors.FromError(ErrUsageObservationConflict).Reason)
}

// R15: the query reuses a registered execution point. An unregistered point is
// rejected by the real Identity resolver before any permission check, so every
// admin query would fail.
func TestObservationQueryUsesARegisteredExecutionPoint(t *testing.T) {
	point, ok := authorization.Execution("log.requests.list")
	require.True(t, ok, "log.requests.list must stay registered")
	require.Contains(t, point.Operations, "log.request.list")
}

// R19: a non-durable consumer must fail closed. The usecase must never return
// Accepted=true, because the relay deletes its only durable copy on ACK.
func TestRecordUsageObservationRefusesNonDurableConsumer(t *testing.T) {
	repo := &stubObservationRepo{err: ErrUsageObservationUnavailable}
	uc := NewUsageObservationUsecase(repo)
	ctx := relayCapabilityContext(t)
	eventID, sendID, observationID, digest, payload := observationEnvelope(t)
	receipt, err := uc.Record(ctx, eventID, sendID, observationID, "terminal", digest, 1, payload)
	require.ErrorIs(t, err, ErrUsageObservationUnavailable)
	require.Nil(t, receipt)
}
