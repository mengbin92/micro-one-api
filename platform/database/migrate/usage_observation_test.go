package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageObservationMigrationUsesRetentionAndPageIndexes(t *testing.T) {
	db := openSqlite(t)
	dir := tempDirWithFiles(t, []string{"152_create_upstream_usage_observations.sql"}, sqliteDialectDir(t))
	runner := NewWithDriver(db, dir, "sqlite3")
	_, err := runner.Apply(context.Background())
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err, "the actual migration runner must be idempotent")

	for _, tc := range []struct {
		name  string
		query string
		index string
	}{
		{
			name:  "retention",
			query: `EXPLAIN QUERY PLAN DELETE FROM upstream_usage_observations WHERE received_at_ms < 1000`,
			index: "idx_usage_observation_received",
		},
		{
			name:  "global page",
			query: `EXPLAIN QUERY PLAN SELECT event_id, observed_at_ms, user_id, usage_status FROM upstream_usage_observations WHERE observed_at_ms >= 1000 AND observed_at_ms < 2000 ORDER BY observed_at_ms DESC, event_id DESC LIMIT 51`,
			index: "idx_usage_observation_observed",
		},
		{
			name:  "user page",
			query: `EXPLAIN QUERY PLAN SELECT event_id, observed_at_ms, user_id, usage_status FROM upstream_usage_observations WHERE user_id = 7 AND observed_at_ms >= 1000 AND observed_at_ms < 2000 ORDER BY observed_at_ms DESC, event_id DESC LIMIT 51`,
			index: "idx_usage_observation_user_time",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := usageObservationQueryPlan(t, db, tc.query)
			t.Log(plan)
			require.Contains(t, plan, tc.index, "the deployed schema must index this query")
			require.NotContains(t, plan, "TEMP B-TREE", "keyset pagination must not sort tied timestamps")
		})
	}
}

func usageObservationQueryPlan(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query(query)
	require.NoError(t, err)
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		details = append(details, detail)
	}
	require.NoError(t, rows.Err())
	return strings.Join(details, "\n")
}

func TestUsageObservationMySQLMigrationKeepsIndexesInAtomicDDL(t *testing.T) {
	path := filepath.Join(sqliteDialectDir(t), "..", "152_create_upstream_usage_observations.sql")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	statements := splitStatements(string(body))
	require.Len(t, statements, 1, "MySQL must not commit the table separately from its indexes before a retry")
	require.Contains(t, statements[0], "event_id VARCHAR(255) COLLATE utf8mb4_bin PRIMARY KEY", "event IDs have exact case-sensitive identity")
}
