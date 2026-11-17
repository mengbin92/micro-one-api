package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
)

type observationServiceRepo struct {
	rows []*biz.UsageObservationRow
}

func (r *observationServiceRepo) Record(context.Context, *biz.UsageObservationRow) (bool, error) {
	return false, nil
}
func (r *observationServiceRepo) ListUsageObservations(context.Context, biz.UsageObservationFilter) ([]*biz.UsageObservationRow, string, error) {
	return r.rows, "", nil
}
func (r *observationServiceRepo) GetUsageObservation(_ context.Context, eventID string) (*biz.UsageObservationRow, error) {
	for _, row := range r.rows {
		if row.EventID == eventID {
			return row, nil
		}
	}
	return nil, biz.ErrUsageObservationNotFound
}
func (r *observationServiceRepo) DeleteObservationsBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func observationServiceRow() *biz.UsageObservationRow {
	return &biz.UsageObservationRow{
		EventID: "event-1", SendID: "obs/1/send", ObservationID: "obs", Category: "terminal", Sequence: 1,
		PayloadSHA256: "digest", Payload: []byte(`{"version":1,"record":{"observation":{"protocol":"openai_chat"}}}`),
		UserID: 7, Protocol: "openai_chat", SourceKind: "channel", SourceID: 4, Purpose: "user",
		UpstreamModelID: "exact", AttemptRequestID: "req", RootRequestID: "root", Capture: "complete",
		Status: "verified", ObservedAtMS: 1_700_000_000_000, ReceivedAtMS: 1_700_000_000_100,
	}
}

// R17: a list page never carries payload bytes; a single event is fetched
// explicitly. A page therefore cannot grow past the client receive limit just
// because individual events are large.
func TestListUsageObservationsReturnsSummariesAndGetReturnsPayload(t *testing.T) {
	repo := &observationServiceRepo{rows: []*biz.UsageObservationRow{observationServiceRow()}}
	svc := NewLogService(nil)
	svc.SetUsageObservationUsecase(biz.NewUsageObservationUsecase(repo))

	ctx := authorization.WithLegacyAuthorization(context.Background(), true)
	page, err := svc.ListUsageObservations(ctx, &logv1.ListUsageObservationsRequest{StartTimeMs: 1, EndTimeMs: 2, UserId: ptrInt64(7)})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Empty(t, page.Items[0].Payload, "the list must not echo payload bytes")

	detail, err := svc.GetUsageObservation(ctx, &logv1.GetUsageObservationRequest{EventId: "event-1"})
	require.NoError(t, err)
	require.NotEmpty(t, detail.Payload, "the single-event getter carries the canonical payload")
}

func ptrInt64(value int64) *int64 { return &value }
