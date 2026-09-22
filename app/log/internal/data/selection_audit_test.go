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

func TestSelectionAuditIsolationRetentionAndReplay(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(map[bool]string{true: "memory", false: "sqlite"}[memory], func(t *testing.T) {
			repo := NewMemoryRepositoryForTest()
			if !memory {
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				require.NoError(t, db.AutoMigrate(&logModel{}, &logIngestDedupeClaimModel{}))
				sqlDB, err := db.DB()
				require.NoError(t, err)
				t.Cleanup(func() { _ = sqlDB.Close() })
				repo = &Repository{db: db}
			}
			ctx := context.Background()
			old := time.Now().Add(-31 * 24 * time.Hour)
			audit := &biz.LogEntry{UserID: 7, RootRequestID: "root", Level: "audit", Source: "routing-selection", ModelName: "model", CreatedAt: old, DedupeKey: "selection:7:root:planned"}
			require.NoError(t, repo.Create(ctx, audit))
			require.NoError(t, repo.Create(ctx, audit))
			rows, err := repo.ListSelectionAudit(ctx, 7, "root")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			userRows, total, err := repo.ListByUser(ctx, 7, 1, 20, "", "")
			require.NoError(t, err)
			require.Empty(t, userRows)
			require.Zero(t, total)
			usage, err := repo.UsageByUser(ctx, 7, time.Time{}, time.Time{})
			require.NoError(t, err)
			require.Empty(t, usage)
			_, err = repo.DeleteBefore(ctx, time.Now().Add(-30*24*time.Hour))
			require.NoError(t, err)
			require.NoError(t, repo.Create(ctx, audit))
			rows, err = repo.ListSelectionAudit(ctx, 7, "root")
			require.NoError(t, err)
			require.Empty(t, rows)
		})
	}
}
