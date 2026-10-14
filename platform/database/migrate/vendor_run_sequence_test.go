package migrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVendorReplaySequenceBackfillsHistoricalOrdering(t *testing.T) {
	db := openScratchSqlite(t)
	dir := sqliteDialectDir(t)
	stage := tempDirWithFiles(t, []string{"132_vendor_reconciliation.sql"}, dir)
	_, err := NewWithDriver(db, stage, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO billing_vendor_reports (id, scope_hash, version, payload, imported_at) VALUES ('report', 'scope', 'v1', '{}', 1)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO billing_vendor_reconciliation_runs (id, report_id, payload, created_at) VALUES ('b', 'report', '{}', 1), ('a', 'report', '{}', 1), ('c', 'report', '{}', 2)`)
	require.NoError(t, err)
	upgrade := tempDirWithFiles(t, []string{"132_vendor_reconciliation.sql", "135_vendor_reconciliation_run_sequence.sql"}, dir)
	applied, err := NewWithDriver(db, upgrade, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"135_vendor_reconciliation_run_sequence"}, applied)
	rows, err := db.Query(`SELECT id, sequence FROM billing_vendor_reconciliation_runs ORDER BY sequence`)
	require.NoError(t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		var sequence int64
		require.NoError(t, rows.Scan(&id, &sequence))
		ids = append(ids, id)
		require.EqualValues(t, len(ids), sequence)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"a", "b", "c"}, ids, "backfill preserves the previously selected historical latest run")
	_, err = db.Exec(`INSERT INTO billing_vendor_reconciliation_runs (id, report_id, payload, created_at, sequence) VALUES ('collision', 'report', '{}', 3, 3)`)
	require.Error(t, err, "one report must not accept two replays with the same sequence")
	applied, err = NewWithDriver(db, upgrade, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	require.Empty(t, applied)
}
