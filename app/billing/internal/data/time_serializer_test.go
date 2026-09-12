package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/platform/database/testutil"
)

func TestBillingSQLiteHistoricalExpiration(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	now := time.Now().UTC().Truncate(time.Second)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, tc := range []struct {
		id      string
		expired any
	}{
		{"past_epoch", past.Unix()}, {"future_epoch", future.Unix()},
		{"past_text", past}, {"future_text", future}, {"no_expiry", nil},
	} {
		require.NoError(t, db.Table("billing_reservations").Create(map[string]any{"reservation_id": tc.id, "user_id": "42", "request_id": tc.id, "model": "m", "amount": 1, "status": biz.ReservationStatusReserved, "created_at": past.Unix(), "updated_at": now, "expired_at": tc.expired}).Error)
	}
	repo := NewReservationRepo(&Data{db: db})
	expired, err := repo.GetExpiredReservations(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0, len(expired))
	for _, r := range expired {
		ids = append(ids, r.ReservationID)
		require.True(t, r.ExpiredAt.Equal(past))
		require.True(t, r.CreatedAt.Equal(past))
		require.True(t, r.UpdatedAt.Equal(now))
		require.Nil(t, r.RequestSnapshot, "historical evidence must not be fabricated")
	}
	require.ElementsMatch(t, []string{"past_epoch", "past_text"}, ids)
	r, err := repo.GetReservation(context.Background(), "no_expiry")
	require.NoError(t, err)
	require.True(t, r.ExpiredAt.IsZero())
}
