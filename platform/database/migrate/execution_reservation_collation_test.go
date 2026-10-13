package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The migration smoke database is disposable; never enable this against production.
func TestExecutionReservationCollationIntegration(t *testing.T) {
	if os.Getenv("MIGRATIONS_RESERVATION_INTEGRATION") != "1" || os.Getenv("MIGRATIONS_DRIVER") != "mysql" {
		t.Skip("requires the MySQL migration smoke database")
	}
	db, err := sql.Open("mysql", os.Getenv("MIGRATIONS_DSN"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "131_match_execution_reservation_collation.sql"))
	require.NoError(t, err)
	const query = `SELECT reservation_id FROM billing_reservations WHERE reservation_id IN (SELECT reservation_id FROM billing_execution_intents WHERE evidence_deadline < UNIX_TIMESTAMP())`
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_general_ci"} {
		t.Run(collation, func(t *testing.T) {
			_, err := db.Exec("ALTER TABLE billing_reservations MODIFY COLUMN reservation_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE " + collation + " NOT NULL")
			require.NoError(t, err)
			_, err = db.Exec("ALTER TABLE billing_execution_intents MODIFY COLUMN reservation_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL")
			require.NoError(t, err)
			rows, err := db.Query(query)
			if rows != nil {
				_ = rows.Close()
			}
			require.ErrorContains(t, err, "Illegal mix of collations")
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "999_reservation_"+collation+".sql"), body, 0600))
			runner := NewWithDriver(db, dir, "mysql")
			applied, err := runner.Apply(context.Background())
			require.NoError(t, err)
			require.Len(t, applied, 1)
			rows, err = db.Query(query)
			require.NoError(t, err)
			require.NoError(t, rows.Close())
			var actual string
			require.NoError(t, db.QueryRow("SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='billing_execution_intents' AND COLUMN_NAME='reservation_id'").Scan(&actual))
			require.Equal(t, collation, actual)
			applied, err = runner.Apply(context.Background())
			require.NoError(t, err)
			require.Empty(t, applied)
		})
	}
}
