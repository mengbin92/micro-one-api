package data

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/metrics"
)

func TestPersistedSnapshotFailureMetrics(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	uc, _, _, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
	r, err := uc.ReserveQuota(context.Background(), "7001", "metrics-request", 1000, "m", "1", 0, requestContext())
	require.NoError(t, err)
	var original reservationModel
	require.NoError(t, db.Where("reservation_id = ?", r.ReservationID).First(&original).Error)
	for _, tc := range []struct {
		name, operation, reason string
		corrupt                 func(*reservationModel)
	}{
		{"decode", "read", "decode", func(m *reservationModel) { m.RequestSnapshot = stringPtr("{") }},
		{"invalid content", "validate", "content", func(m *reservationModel) { m.RequestSnapshot = stringPtr("{}") }},
		{"digest", "read", "digest", func(m *reservationModel) { m.RequestSnapshotHash = stringPtr("corrupted") }},
		{"subject", "read", "subject", func(m *reservationModel) { m.UserID = "different-user" }},
		{"missing snapshot", "read", "missing_snapshot", func(m *reservationModel) { m.RequestSnapshot = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metric := metrics.RoutingSnapshotFailures.WithLabelValues(tc.operation, tc.reason)
			before := testutil.ToFloat64(metric)
			_, err := reservationFromModel(&original)
			require.NoError(t, err)
			require.Equal(t, before, testutil.ToFloat64(metric))
			row := original
			tc.corrupt(&row)
			_, err = reservationFromModel(&row)
			require.ErrorIs(t, err, biz.ErrRequestSnapshotInvalid)
			require.Equal(t, before+1, testutil.ToFloat64(metric))
		})
	}
}
