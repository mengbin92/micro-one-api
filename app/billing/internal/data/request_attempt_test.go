package data

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
)

func TestRequestAttemptsSurviveReopenAndScopeByUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "billing.db")
	db := setupReservationTestDB(t, path)
	repo := NewReservationRepo(&Data{db: db})
	ctx := context.Background()
	for _, row := range []*biz.Reservation{
		{ReservationID: "r2", UserID: "1", RequestID: "a2", RootRequestID: "root", AttemptNumber: 2, Status: "committed", SourceKind: "channel", UpstreamModelID: "Mapped-B", ActualCost: 80},
		{ReservationID: "r1", UserID: "1", RequestID: "a1", RootRequestID: "root", AttemptNumber: 1, Status: "reserved", SourceKind: "channel", UpstreamModelID: "Mapped-A"},
		{ReservationID: "other-user", UserID: "2", RequestID: "a1", RootRequestID: "root", AttemptNumber: 1, Status: "committed"},
		{ReservationID: "other-root", UserID: "1", RequestID: "a3", RootRequestID: "different", AttemptNumber: 1, Status: "committed"},
	} {
		row.CreatedAt, row.UpdatedAt, row.ExpiredAt = time.Now(), time.Now(), time.Now().Add(time.Minute)
		require.NoError(t, repo.CreateReservation(ctx, row))
	}
	require.NoError(t, db.Model(&reservationModel{}).Where("reservation_id = ?", "r2").Update("actual_cost", 80).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err = db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	reopened := NewReservationRepo(&Data{db: db}).(biz.RequestAttemptRepo)
	rows, total, err := reopened.ListRequestAttempts(ctx, "1", "root", 0, 100)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 2)
	require.Equal(t, "a1", rows[0].RequestID)
	require.Equal(t, "reserved", rows[0].Status, "failed release remains visible")
	require.Equal(t, "Mapped-A", rows[0].UpstreamModelID)
	require.Equal(t, "committed", rows[1].Status)
	require.EqualValues(t, 80, rows[1].ActualCost)
	page, total, err := reopened.ListRequestAttempts(ctx, "1", "root", 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, page, 1)
	require.Equal(t, "r2", page[0].ReservationID)
}
