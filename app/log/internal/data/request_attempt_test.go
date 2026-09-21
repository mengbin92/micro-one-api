package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/log/internal/biz"
)

func TestExecutionIdentityRoundTripSingleAndBatch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&logModel{}, &logIngestDedupeClaimModel{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := &Repository{db: db}
	ctx := context.Background()
	for _, batch := range []bool{false, true} {
		entry := &biz.LogEntry{Level: "consume", UserID: 1, RequestID: "attempt", RootRequestID: "root", AttemptNumber: 2, ReservationID: "reservation", SourceKind: "channel", UpstreamModelID: "Mapped-Model", CreatedAt: time.Now()}
		if batch {
			require.NoError(t, repo.CreateBatch(ctx, []*biz.LogEntry{entry}))
		} else {
			require.NoError(t, repo.Create(ctx, entry))
		}
	}
	rows, total, err := repo.ListByUser(ctx, 1, 1, 20, "consume", "")
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	for _, row := range rows {
		got, err := repo.Get(ctx, row.ID)
		require.NoError(t, err)
		for _, e := range []*biz.LogEntry{row, got} {
			require.Equal(t, "root", e.RootRequestID)
			require.EqualValues(t, 2, e.AttemptNumber)
			require.Equal(t, "reservation", e.ReservationID)
			require.Equal(t, "channel", e.SourceKind)
			require.Equal(t, "Mapped-Model", e.UpstreamModelID)
		}
	}
}
