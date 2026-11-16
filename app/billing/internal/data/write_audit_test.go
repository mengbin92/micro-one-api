package data

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"micro-one-api/app/billing/internal/biz"
)

func TestWriteAuditRepoUnavailableWithoutDatabase(t *testing.T) {
	for name, repo := range map[string]biz.WriteAuditRepo{
		"missing database": NewWriteAuditRepo(&Data{}),
		"nil receiver":     (*writeAuditRepo)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			page, err := repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Limit: 10})
			rows, total := page.Items, page.Total
			if err == nil {
				require.NotEmpty(t, page.StoreID)
			}
			require.ErrorIs(t, err, biz.ErrWriteAuditUnavailable)
			require.Nil(t, rows)
			require.Zero(t, total)
		})
	}
}

func openWriteAuditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(`CREATE TABLE resource_write_audits (
		event_id VARCHAR(64) PRIMARY KEY,
		actor_user_id BIGINT NOT NULL,
		operation VARCHAR(160) NOT NULL,
		resource_id VARCHAR(128) NOT NULL,
		decision_versions TEXT NOT NULL,
		reason TEXT NOT NULL,
		occurred_at BIGINT NOT NULL,
		result VARCHAR(16) NOT NULL
	)`).Error)
	return db
}

func seedWriteAudits(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO resource_write_audits
		(event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result)
		VALUES
		('ev-1', 7, 'billing.vendor.import', '9', '[1]', 'ops', 100, 'success'),
		('ev-2', 8, 'billing.reconcile.run', '4', '[2]', 'ops', 300, 'failure'),
		('ev-3', 7, 'billing.reconcile.run', '5', '[3]', 'ops', 200, 'success')`).Error)
}

func TestWriteAuditRepoListsWithFilters(t *testing.T) {
	db := openWriteAuditTestDB(t)
	seedWriteAudits(t, db)
	repo := NewWriteAuditRepo(&Data{db: db})

	page, err := repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Limit: 10})
	rows, total := page.Items, page.Total
	if err == nil {
		require.NotEmpty(t, page.StoreID)
	}
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Equal(t, []string{"ev-2", "ev-3", "ev-1"}, []string{rows[0].EventID, rows[1].EventID, rows[2].EventID},
		"occurred_at DESC ordering")

	page, err = repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Operation: "billing.reconcile", Limit: 10})
	rows, total = page.Items, page.Total
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "operation prefix match")
	require.Equal(t, "billing.reconcile.run", rows[0].Operation)

	page, err = repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Result: "failure", Limit: 10})
	total = page.Total
	require.NoError(t, err)
	require.EqualValues(t, 1, total)

	page, err = repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{ActorUserID: 7, StartTimeMS: 150, Limit: 10})
	total = page.Total
	require.NoError(t, err)
	require.EqualValues(t, 1, total)

	page, err = repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Limit: 1, Offset: 1})
	rows, total = page.Items, page.Total
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, rows, 1)
	require.Equal(t, "ev-3", rows[0].EventID)
}
