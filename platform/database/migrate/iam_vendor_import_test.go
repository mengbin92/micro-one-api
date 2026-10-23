package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

const iamVendorImportMigration = "140_publish_vendor_report_import"
const iamVendorImportReceipt = "migration:140:vendor-report-import"

func TestIAMVendorImportMigrationNeverReopensMySQLTemporaryGuard(t *testing.T) {
	root := filepath.Dir(sqliteDialectDir(t))
	for _, dialect := range []string{"", "postgres", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, dialect, iamVendorImportMigration+".sql"))
			require.NoError(t, err)
			for _, statement := range splitStatements(string(body)) {
				require.LessOrEqual(t, strings.Count(strings.ToLower(statement), "iam_vendor_import_release_guard"), 1, "%s", statement)
			}
		})
	}
}

// Opt-in DSNs must identify disposable local database servers. Each scenario
// creates and drops its own schema; deployment environment variables are unused.
func iamVendorImportDatabase(t *testing.T, driver string) *sql.DB {
	t.Helper()
	if driver == "sqlite3" {
		db := openScratchSqlite(t)
		db.SetMaxOpenConns(1)
		return db
	}
	dsn := os.Getenv("IAM_VENDOR_IMPORT_TEST_" + strings.ToUpper(driver) + "_DSN")
	if dsn == "" {
		t.Skip("disposable " + driver + " DSN not supplied")
	}
	schema := fmt.Sprintf("iam_vendor_import_test_%d", time.Now().UnixNano())
	var db *sql.DB
	if driver == "mysql" {
		cfg, err := mysqldriver.ParseDSN(dsn)
		require.NoError(t, err)
		admin, err := sql.Open("mysql", cfg.FormatDSN())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		_, err = admin.Exec("CREATE DATABASE " + schema + " CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci")
		require.NoError(t, err)
		t.Cleanup(func() { _, err := admin.Exec("DROP DATABASE " + schema); require.NoError(t, err) })
		cfg.DBName = schema
		db, err = sql.Open("mysql", cfg.FormatDSN())
		require.NoError(t, err)
	} else {
		cfg, err := pgx.ParseConfig(dsn)
		require.NoError(t, err)
		admin := stdlib.OpenDB(*cfg)
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		_, err = admin.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
		cfg.RuntimeParams["search_path"] = schema
		db = stdlib.OpenDB(*cfg)
	}
	db.SetMaxOpenConns(1) // The release guard is connection-local.
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))
	return db
}

func iamVendorImportUpgrade(t *testing.T, driver string) (*sql.DB, *Runner) {
	t.Helper()
	db := iamVendorImportDatabase(t, driver)
	dir := filepath.Dir(sqliteDialectDir(t))
	if driver == "sqlite3" {
		dir = filepath.Join(dir, "sqlite")
	} else if driver == "postgres" {
		dir = filepath.Join(dir, "postgres")
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < iamVendorImportMigration+".sql" {
			files = append(files, entry.Name())
		}
	}
	_, err = NewWithDriver(db, tempDirWithFiles(t, files, dir), driver).Apply(context.Background())
	require.NoError(t, err)
	files = append(files, iamVendorImportMigration+".sql")
	return db, NewWithDriver(db, tempDirWithFiles(t, files, dir), driver)
}

func iamVendorImportRows(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	require.NoError(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var out []string
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		require.NoError(t, rows.Scan(targets...))
		for i, value := range values {
			if raw, ok := value.([]byte); ok {
				values[i] = string(raw)
			}
		}
		raw, err := jsonx.Marshal(values)
		require.NoError(t, err)
		out = append(out, string(raw))
	}
	require.NoError(t, rows.Err())
	sort.Strings(out)
	return out
}

func iamVendorImportState(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range []string{"iam_resources", "iam_permissions", "iam_roles", "iam_role_permissions", "iam_user_roles", "iam_sessions", "iam_session_contexts", "iam_session_roles", "iam_policy_state", "iam_audit_events", "schema_migrations"} {
		out[table] = iamVendorImportRows(t, db, "SELECT * FROM "+table)
	}
	out["users"] = iamVendorImportRows(t, db, "SELECT id,authorization_revision FROM users")
	return out
}

func iamVendorImportSeedAssignment(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		"INSERT INTO users (id,username,authorization_revision) VALUES (900001,'iam-vendor-import-test',17)",
		`INSERT INTO iam_user_roles (context_key,user_id,role_id,allow_boundary,starts_at,status,revision,origin) SELECT 'platform',900001,id,'{"clauses":[{"all":true}]}',1,'active',5,'explicit' FROM iam_roles WHERE context_key='platform' AND code='root'`,
		"INSERT INTO iam_sessions (session_id,user_id,expires_at,revision) VALUES ('iam-vendor-import-session',900001,9999999999999,9)",
		"INSERT INTO iam_session_contexts (session_id,context_key,activation_state,revision) VALUES ('iam-vendor-import-session','platform','active',11)",
		"INSERT INTO iam_session_roles (session_id,context_key,role_id) SELECT 'iam-vendor-import-session','platform',id FROM iam_roles WHERE context_key='platform' AND code='root'",
	} {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
}

func TestIAMVendorImportUpgradeDialects(t *testing.T) {
	for _, driver := range []string{"sqlite3", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Run("root_only_and_replay_safe", func(t *testing.T) {
				db, runner := iamVendorImportUpgrade(t, driver)
				iamVendorImportSeedAssignment(t, db)
				before := iamVendorImportState(t, db)
				otherPermissions := iamVendorImportRows(t, db, "SELECT * FROM iam_permissions WHERE code<>'billing.vendor_report.import'")
				otherRoles := iamVendorImportRows(t, db, "SELECT * FROM iam_roles WHERE NOT (context_key='platform' AND code='root')")
				policy := iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state")
				catalog := iamReadPagesScalar(t, db, "SELECT catalog_revision FROM iam_policy_state")
				rootRevision := iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'")
				grants := iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions")
				applied, err := runner.Apply(context.Background())
				require.NoError(t, err)
				require.Equal(t, []string{iamVendorImportMigration}, applied)
				require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code='billing.vendor_report.import' AND status='enabled' AND binding_state='bound' AND revision=2"))
				require.EqualValues(t, 1, iamReadPagesScalar(t, db, `SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id=g.role_id JOIN iam_permissions p ON p.id=g.permission_id WHERE p.code='billing.vendor_report.import' AND r.context_type='platform' AND r.context_key='platform' AND r.organization_id=0 AND r.code='root' AND r.builtin=1 AND r.status='enabled' AND g.effect='allow' AND g.scope_descriptor='{"clauses":[{"all":true}]}'`))
				require.Equal(t, grants+1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions"))
				require.Equal(t, before["iam_role_permissions"], iamVendorImportRows(t, db, "SELECT * FROM iam_role_permissions WHERE permission_id<>(SELECT id FROM iam_permissions WHERE code='billing.vendor_report.import')"))
				require.Equal(t, otherPermissions, iamVendorImportRows(t, db, "SELECT * FROM iam_permissions WHERE code<>'billing.vendor_report.import'"))
				require.Equal(t, otherRoles, iamVendorImportRows(t, db, "SELECT * FROM iam_roles WHERE NOT (context_key='platform' AND code='root')"))
				require.Equal(t, rootRevision+1, iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'"))
				require.Equal(t, policy+1, iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state"))
				require.Equal(t, catalog+1, iamReadPagesScalar(t, db, "SELECT catalog_revision FROM iam_policy_state"))
				after := iamVendorImportState(t, db)
				for _, table := range []string{"iam_resources", "users", "iam_user_roles", "iam_sessions", "iam_session_contexts", "iam_session_roles"} {
					require.Equal(t, before[table], after[table], table)
				}
				require.Len(t, after["iam_audit_events"], len(before["iam_audit_events"])+1)
				var auditBefore, auditAfter, versions string
				require.NoError(t, db.QueryRow("SELECT before_data,after_data,decision_versions FROM iam_audit_events WHERE event_id='"+iamVendorImportReceipt+"' AND actor_service_id='schema-migrate' AND action='iam.catalog.release.vendor_report_import' AND result='success'").Scan(&auditBefore, &auditAfter, &versions))
				require.True(t, jsonx.Valid([]byte(auditBefore)))
				require.True(t, jsonx.Valid([]byte(auditAfter)))
				require.NotEqual(t, auditBefore, auditAfter)
				var decisionVersions struct{ Policy, Catalog uint64 }
				require.NoError(t, jsonx.Unmarshal([]byte(versions), &decisionVersions))
				require.EqualValues(t, policy+1, decisionVersions.Policy)
				require.EqualValues(t, catalog+1, decisionVersions.Catalog)
				applied, err = runner.Apply(context.Background())
				require.NoError(t, err)
				require.Empty(t, applied)
				require.Equal(t, after, iamVendorImportState(t, db), "ordinary repeat is a no-op")

				// Losing runner metadata must not undo an operator's later disable.
				_, err = db.Exec("UPDATE iam_permissions SET status='disabled',revision=revision+1 WHERE code='billing.vendor_report.import'")
				require.NoError(t, err)
				_, err = db.Exec("DELETE FROM schema_migrations WHERE version='" + iamVendorImportMigration + "'")
				require.NoError(t, err)
				replayBefore := iamVendorImportState(t, db)
				applied, err = runner.Apply(context.Background())
				require.NoError(t, err)
				require.Equal(t, []string{iamVendorImportMigration}, applied)
				replayAfter := iamVendorImportState(t, db)
				delete(replayBefore, "schema_migrations")
				delete(replayAfter, "schema_migrations")
				require.Equal(t, replayBefore, replayAfter, "the release receipt prevents resurrection and duplicate audit/revision changes")
			})

			for _, effect := range []string{"deny", "allow"} {
				t.Run("preserves_existing_"+effect, func(t *testing.T) {
					db, runner := iamVendorImportUpgrade(t, driver)
					_, err := db.Exec(`INSERT INTO iam_role_permissions (context_key,role_id,permission_id,effect,scope_descriptor,revision) SELECT 'platform',r.id,p.id,'` + effect + `','{"clauses":[{"all":true}]}',7 FROM iam_roles r CROSS JOIN iam_permissions p WHERE r.context_key='platform' AND r.code='root' AND p.code='billing.vendor_report.import'`)
					require.NoError(t, err)
					grants := iamVendorImportRows(t, db, "SELECT * FROM iam_role_permissions")
					roles := iamVendorImportRows(t, db, "SELECT * FROM iam_roles")
					_, err = runner.Apply(context.Background())
					require.NoError(t, err)
					require.Equal(t, grants, iamVendorImportRows(t, db, "SELECT * FROM iam_role_permissions"))
					require.Equal(t, roles, iamVendorImportRows(t, db, "SELECT * FROM iam_roles"))
					require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code='billing.vendor_report.import' AND status='enabled' AND binding_state='bound'"))
				})
			}

			for _, blocked := range []struct{ name, mutation string }{
				{"disabled_permission", "UPDATE iam_permissions SET status='disabled' WHERE code='billing.vendor_report.import'"},
				{"archived_permission", "UPDATE iam_permissions SET status='archived' WHERE code='billing.vendor_report.import'"},
				{"wrong_owner", "UPDATE iam_resources SET owner_service='channel' WHERE code='billing.vendor_report'"},
				{"disabled_resource", "UPDATE iam_resources SET enabled=0 WHERE code='billing.vendor_report'"},
				{"wrong_action", "UPDATE iam_permissions SET action='unexpected_import' WHERE code='billing.vendor_report.import'"},
				{"disabled_root", "UPDATE iam_roles SET status='disabled' WHERE context_key='platform' AND code='root'"},
				{"nonbuiltin_root", "UPDATE iam_roles SET builtin=0 WHERE context_key='platform' AND code='root'"},
				{"missing_root", "UPDATE iam_roles SET code='former_root' WHERE context_key='platform' AND code='root'"},
			} {
				t.Run("blocks_"+blocked.name, func(t *testing.T) {
					db, runner := iamVendorImportUpgrade(t, driver)
					_, err := db.Exec(blocked.mutation)
					require.NoError(t, err)
					before := iamVendorImportState(t, db)
					_, err = runner.Apply(context.Background())
					require.ErrorContains(t, err, "iam_vendor_import_catalog_and_root_must_be_publishable")
					require.Equal(t, before, iamVendorImportState(t, db), "failed publication must leave all persistent evidence and authorization state unchanged")
				})
			}
		})
	}
}

func TestIAMVendorImportUpgradeRollsBackWhenAuditCannotBeRecorded(t *testing.T) {
	db, runner := iamVendorImportUpgrade(t, "sqlite3")
	_, err := db.Exec("CREATE TRIGGER reject_vendor_import_audit BEFORE INSERT ON iam_audit_events WHEN NEW.event_id='" + iamVendorImportReceipt + "' BEGIN SELECT RAISE(ABORT, 'isolated audit write failure'); END")
	require.NoError(t, err)
	before := iamVendorImportState(t, db)
	_, err = runner.Apply(context.Background())
	require.ErrorContains(t, err, "isolated audit write failure")
	require.Equal(t, before, iamVendorImportState(t, db))
	_, err = db.Exec("DROP TRIGGER reject_vendor_import_audit")
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err, "a failed audit must not leave a stale guard or receipt")
}

func TestIAMVendorImportUpgradeMySQLMetadataFailureRollsBackPublication(t *testing.T) {
	db, runner := iamVendorImportUpgrade(t, "mysql")
	// MySQL DROP TABLE implicitly commits, even for a temporary table. Fail the
	// runner's final metadata INSERT to catch a release committed before it.
	_, err := db.Exec("CREATE TRIGGER reject_vendor_import_metadata BEFORE INSERT ON schema_migrations FOR EACH ROW BEGIN IF NEW.version='" + iamVendorImportMigration + "' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='isolated migration metadata failure'; END IF; END")
	require.NoError(t, err)
	before := iamVendorImportState(t, db)
	_, err = runner.Apply(context.Background())
	require.ErrorContains(t, err, "isolated migration metadata failure")
	require.Equal(t, before, iamVendorImportState(t, db), "a metadata write failure must roll back permission, grant, revisions and receipt together")
	_, err = db.Exec("DROP TRIGGER reject_vendor_import_metadata")
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err, "publication must succeed after the metadata failure is removed")
}
