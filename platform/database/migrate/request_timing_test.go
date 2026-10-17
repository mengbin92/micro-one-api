package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestTimingUpgradeKeepsHistoricalNullsAndTimeIndexes(t *testing.T) {
	db := openScratchSqlite(t)
	dir := sqliteDialectDir(t)
	stage := tempDirWithFiles(t, []string{"000_create_full_schema.sql", "107_add_log_request_trace.sql"}, dir)
	_, err := NewWithDriver(db, stage, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO logs (level, message, source, user_id, request_id, created_at) VALUES ('consume', '', 'relay-gateway', 7, 'old-request', 1000)`)
	require.NoError(t, err)
	upgrade := tempDirWithFiles(t, []string{"137_add_request_timing_summaries.sql"}, dir)
	runner := NewWithDriver(db, upgrade, "sqlite3")
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"137_add_request_timing_summaries"}, applied)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM logs WHERE observation_id IS NULL AND request_timing_json IS NULL AND request_started_at_ms IS NULL AND request_outcome IS NULL AND otel_trace_id IS NULL`).Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.Exec(`INSERT INTO logs (level, message, created_at, source, observation_id, user_id, request_started_at_ms) VALUES ('info', '', 1000, 'relay-request-timing', 'obs', 7, 1000)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO logs (level, message, created_at, source, observation_id, user_id, request_started_at_ms) VALUES ('info', '', 1000, 'relay-request-timing', 'obs', 7, 2000)`)
	require.Error(t, err, "observation uniqueness does not depend on request/trace IDs")
	for _, tc := range []struct{ query, index string }{
		{`SELECT id FROM logs WHERE source = 'relay-request-timing' AND user_id = 7 AND request_started_at_ms >= 0 AND request_started_at_ms < 2000 ORDER BY request_started_at_ms DESC, id DESC LIMIT 50`, "idx_log_trace_user_time"},
		{`SELECT id FROM logs WHERE source = 'relay-request-timing' AND request_started_at_ms >= 0 AND request_started_at_ms < 2000 ORDER BY request_started_at_ms DESC, id DESC LIMIT 50`, "idx_log_trace_time"},
		{`SELECT id FROM logs WHERE source = 'relay-request-timing' AND user_id = 7 AND root_request_id = 'root' AND request_started_at_ms >= 0 ORDER BY request_started_at_ms DESC, id DESC LIMIT 50`, "idx_log_trace_root_time"},
	} {
		rows, err := db.Query("EXPLAIN QUERY PLAN " + tc.query)
		require.NoError(t, err)
		var plans []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
			plans = append(plans, detail)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		plan := strings.Join(plans, "\n")
		require.Contains(t, plan, tc.index)
		require.NotContains(t, plan, "TEMP B-TREE")
	}
	again, err := runner.Apply(context.Background())
	require.NoError(t, err)
	require.Empty(t, again)
}
