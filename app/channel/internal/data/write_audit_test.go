package data

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"micro-one-api/app/channel/internal/biz"
)

func TestWriteAuditRepoUnavailableWithoutDatabase(t *testing.T) {
	for name, repo := range map[string]biz.WriteAuditRepo{
		"memory repository": NewWriteAuditRepo(newMemoryRepository()),
		"nil receiver":      (*writeAuditRepo)(nil),
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

func TestWriteAuditRepoListsOwnerRows(t *testing.T) {
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
	require.NoError(t, db.Exec(`INSERT INTO resource_write_audits
		(event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result)
		VALUES
		('ev-1', 3, 'channel.model_mapping.create', '12', '[2]', 'ops', 100, 'success'),
		('ev-2', 3, 'channel.channel.update', '1', '[2]', 'ops', 300, 'failure')`).Error)

	repo := NewWriteAuditRepo(&Repository{db: db})
	page, err := repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Limit: 10})
	rows, total := page.Items, page.Total
	if err == nil {
		require.NotEmpty(t, page.StoreID)
	}
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, []string{"ev-2", "ev-1"}, []string{rows[0].EventID, rows[1].EventID})

	page, err = repo.ListWriteAudits(t.Context(), biz.WriteAuditFilter{Operation: "channel.model_mapping", Limit: 10})
	rows, total = page.Items, page.Total
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "channel.model_mapping.create", rows[0].Operation)
	require.Equal(t, "12", rows[0].ResourceID)
}
