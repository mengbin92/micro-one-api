package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

const iamUpstreamContractWriteMigration = "142_publish_upstream_contract_write_permissions"
const iamUpstreamContractWriteReceipt = "migration:142:upstream-contract-write"

var iamUpstreamContractWriteCodes = []string{
	"channel.upstream_contract.create",
	"channel.upstream_contract.publish",
	"channel.upstream_contract.quarantine",
}

func TestIAMUpstreamContractWriteMigrationNeverReopensTemporaryGuard(t *testing.T) {
	root := filepath.Dir(sqliteDialectDir(t))
	for _, dialect := range []string{"", "postgres", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, dialect, iamUpstreamContractWriteMigration+".sql"))
			require.NoError(t, err)
			for _, statement := range splitStatements(string(body)) {
				require.LessOrEqual(t, strings.Count(strings.ToLower(statement), "iam_upstream_contract_release_guard"), 1, "%s", statement)
			}
		})
	}
}

// Opt-in DSNs must identify disposable local database servers. Each scenario
// creates and drops its own schema; deployment environment variables are unused.
func iamUpstreamContractWriteUpgrade(t *testing.T, driver string) (*sql.DB, *Runner) {
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
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < iamUpstreamContractWriteMigration+".sql" {
			files = append(files, entry.Name())
		}
	}
	_, err = NewWithDriver(db, tempDirWithFiles(t, files, dir), driver).Apply(context.Background())
	require.NoError(t, err)
	files = append(files, iamUpstreamContractWriteMigration+".sql")
	return db, NewWithDriver(db, tempDirWithFiles(t, files, dir), driver)
}

func TestIAMUpstreamContractWriteUpgradeDialects(t *testing.T) {
	for _, driver := range []string{"sqlite3", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Run("root_only_and_replay_safe", func(t *testing.T) {
				db, runner := iamUpstreamContractWriteUpgrade(t, driver)
				iamVendorImportSeedAssignment(t, db)
				before := iamVendorImportState(t, db)
				inClause := "'" + strings.Join(iamUpstreamContractWriteCodes, "','") + "'"
				otherPermissions := iamVendorImportRows(t, db, "SELECT * FROM iam_permissions WHERE code NOT IN ("+inClause+")")
				otherRoles := iamVendorImportRows(t, db, "SELECT * FROM iam_roles WHERE NOT (context_key='platform' AND code='root')")
				policy := iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state")
				catalog := iamReadPagesScalar(t, db, "SELECT catalog_revision FROM iam_policy_state")
				rootRevision := iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'")
				grants := iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions")
				applied, err := runner.Apply(context.Background())
				require.NoError(t, err)
				require.Equal(t, []string{iamUpstreamContractWriteMigration}, applied)
				for _, code := range iamUpstreamContractWriteCodes {
					require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code='"+code+"' AND status='enabled' AND binding_state='bound' AND revision=2"), code)
					require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id=g.role_id JOIN iam_permissions p ON p.id=g.permission_id WHERE p.code='"+code+"' AND r.context_type='platform' AND r.context_key='platform' AND r.organization_id=0 AND r.code='root' AND r.builtin=1 AND r.status='enabled' AND g.effect='allow' AND g.scope_descriptor='{\"clauses\":[{\"all\":true}]}'"), code)
				}
				require.Equal(t, grants+3, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions"))
				require.Equal(t, before["iam_role_permissions"], iamVendorImportRows(t, db, "SELECT * FROM iam_role_permissions WHERE permission_id NOT IN (SELECT id FROM iam_permissions WHERE code IN ("+inClause+"))"))
				require.Equal(t, otherPermissions, iamVendorImportRows(t, db, "SELECT * FROM iam_permissions WHERE code NOT IN ("+inClause+")"))
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
				require.NoError(t, db.QueryRow("SELECT before_data,after_data,decision_versions FROM iam_audit_events WHERE event_id='"+iamUpstreamContractWriteReceipt+"' AND actor_service_id='schema-migrate' AND action='iam.catalog.release.upstream_contract_write' AND result='success'").Scan(&auditBefore, &auditAfter, &versions))
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
				require.Equal(t, after, iamVendorImportState(t, db))
			})
		})
	}
}
