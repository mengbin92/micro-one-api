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

const iamReadPagesMigration = "136_publish_read_only_evidence_pages"
const iamReadPagesReceipt = "migration:136:read-only-evidence-pages"

func TestIAMReadPagesMigrationNeverReopensMySQLTemporaryGuard(t *testing.T) {
	root := filepath.Dir(sqliteDialectDir(t))
	for _, dialect := range []string{"", "postgres", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, dialect, iamReadPagesMigration+".sql"))
			require.NoError(t, err)
			for _, statement := range splitStatements(string(body)) {
				// MySQL returns ER_CANT_REOPEN_TABLE even when references are in
				// separate scalar subqueries of the same UPDATE statement.
				require.LessOrEqual(t, strings.Count(strings.ToLower(statement), "iam_read_pages_release_guard"), 1, "%s", statement)
			}
		})
	}
}

func iamReadPagesUpgrade(t *testing.T) (*sql.DB, *Runner) {
	t.Helper()
	db := openScratchSqlite(t)
	db.SetMaxOpenConns(1)
	dir := sqliteDialectDir(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < iamReadPagesMigration+".sql" {
			files = append(files, entry.Name())
		}
	}
	stage := tempDirWithFiles(t, files, dir)
	_, err = NewWithDriver(db, stage, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	files = append(files, iamReadPagesMigration+".sql")
	return db, NewWithDriver(db, tempDirWithFiles(t, files, dir), "sqlite3")
}

func iamReadPagesScalar(t *testing.T, db *sql.DB, query string) int64 {
	t.Helper()
	var value int64
	require.NoError(t, db.QueryRow(query).Scan(&value))
	return value
}

func TestIAMReadPagesUpgradeRootOnlyAndReplaySafe(t *testing.T) {
	db, runner := iamReadPagesUpgrade(t)
	policy := iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state")
	catalog := iamReadPagesScalar(t, db, "SELECT catalog_revision FROM iam_policy_state")
	rootRevision := iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'")
	grants := iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions")
	otherRoleRevisions := iamReadPagesScalar(t, db, "SELECT SUM(revision) FROM iam_roles WHERE code<>'root'")
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{iamReadPagesMigration}, applied)
	require.EqualValues(t, 2, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code IN ('billing.vendor_report.read','channel.upstream_contract.read') AND status='enabled' AND binding_state='bound' AND revision=2"))
	require.EqualValues(t, 2, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_roles r ON r.id=g.role_id JOIN iam_permissions p ON p.id=g.permission_id WHERE p.code IN ('billing.vendor_report.read','channel.upstream_contract.read') AND r.context_key='platform' AND r.code='root' AND r.builtin=1 AND g.effect='allow' AND g.scope_descriptor='{\"clauses\":[{\"all\":true}]}'"))
	require.Equal(t, grants+2, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions"))
	require.Equal(t, otherRoleRevisions, iamReadPagesScalar(t, db, "SELECT SUM(revision) FROM iam_roles WHERE code<>'root'"))
	require.Equal(t, rootRevision+1, iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'"))
	require.Equal(t, policy+1, iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state"))
	require.Equal(t, catalog+1, iamReadPagesScalar(t, db, "SELECT catalog_revision FROM iam_policy_state"))
	require.EqualValues(t, 5, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code IN ('billing.vendor_report.import','billing.vendor_report.attribute','channel.upstream_contract.create','channel.upstream_contract.publish','channel.upstream_contract.quarantine') AND status='draft'"))
	var before, after, versions string
	require.NoError(t, db.QueryRow("SELECT before_data,after_data,decision_versions FROM iam_audit_events WHERE event_id='"+iamReadPagesReceipt+"'").Scan(&before, &after, &versions))
	var auditBefore, auditAfter map[string]any
	require.NoError(t, jsonx.Unmarshal([]byte(before), &auditBefore))
	require.NoError(t, jsonx.Unmarshal([]byte(after), &auditAfter))
	require.Equal(t, "draft", auditBefore["vendor_report_status"])
	require.Equal(t, "enabled", auditAfter["vendor_report_status"])
	require.Equal(t, float64(0), auditBefore["root_read_grants"])
	require.Equal(t, float64(2), auditAfter["root_read_grants"])
	var decisionVersions struct{ Policy, Catalog uint64 }
	require.NoError(t, jsonx.Unmarshal([]byte(versions), &decisionVersions))
	require.EqualValues(t, policy+1, decisionVersions.Policy)
	require.EqualValues(t, catalog+1, decisionVersions.Catalog)
	// Replaying the SQL itself must also be harmless after a later operator deny.
	_, err = db.Exec("UPDATE iam_permissions SET status='disabled' WHERE code='billing.vendor_report.read'")
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM schema_migrations WHERE version='" + iamReadPagesMigration + "'")
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code='billing.vendor_report.read' AND status='disabled'"))
	require.Equal(t, policy+1, iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state"))
	require.Equal(t, rootRevision+1, iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'"))
	require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_audit_events WHERE event_id='"+iamReadPagesReceipt+"' AND actor_service_id='schema-migrate' AND action='iam.catalog.release.read_only_evidence_pages' AND result='success'"))
}

func TestIAMReadPagesUpgradePreservesExplicitDeny(t *testing.T) {
	db, runner := iamReadPagesUpgrade(t)
	_, err := db.Exec("INSERT INTO iam_role_permissions (context_key,role_id,permission_id,effect,scope_descriptor) SELECT 'platform',r.id,p.id,'deny','{\"clauses\":[{\"all\":true}]}' FROM iam_roles r JOIN iam_permissions p ON p.code='billing.vendor_report.read' WHERE r.context_key='platform' AND r.code='root'")
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_permissions p ON p.id=g.permission_id WHERE p.code='billing.vendor_report.read' AND g.effect='deny' AND g.revision=1"))
	require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_permissions p ON p.id=g.permission_id WHERE p.code='billing.vendor_report.read' AND g.effect='allow'"))
}

func TestIAMReadPagesUpgradeBlocksGovernedCatalog(t *testing.T) {
	for _, status := range []string{"disabled", "archived"} {
		t.Run(status, func(t *testing.T) {
			db, runner := iamReadPagesUpgrade(t)
			_, err := db.Exec("UPDATE iam_permissions SET status=? WHERE code='billing.vendor_report.read'", status)
			require.NoError(t, err)
			policy := iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state")
			grants := iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions")
			_, err = runner.Apply(context.Background())
			require.ErrorContains(t, err, "iam_read_pages_catalog_and_root_must_be_publishable")
			require.Equal(t, policy, iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state"))
			require.Equal(t, grants, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions"))
			require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code='channel.upstream_contract.read' AND status<>'draft'"))
			require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_audit_events WHERE event_id='"+iamReadPagesReceipt+"'"))
		})
	}
}

func TestIAMReadPagesUpgradeDoesNotRequireRootBeforeIAMCutover(t *testing.T) {
	db, runner := iamReadPagesUpgrade(t)
	_, err := db.Exec("DELETE FROM iam_role_permissions WHERE role_id IN (SELECT id FROM iam_roles WHERE context_key='platform' AND code='root'); DELETE FROM iam_roles WHERE context_key='platform' AND code='root'")
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code IN ('billing.vendor_report.read','channel.upstream_contract.read') AND status='enabled'"))
	require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_roles WHERE code='root'"))
	require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions g JOIN iam_permissions p ON p.id=g.permission_id WHERE p.code IN ('billing.vendor_report.read','channel.upstream_contract.read')"))
	require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_user_roles"))
}

func TestIAMReadPagesUpgradeBlocksDisabledRootAfterIAMCutover(t *testing.T) {
	db, runner := iamReadPagesUpgrade(t)
	_, err := db.Exec("UPDATE iam_policy_state SET authorization_mode='iam',cutover_state='complete',cutover_batch_id='isolated-test',cutover_verified_at=1 WHERE id=1; UPDATE iam_roles SET status='disabled' WHERE context_key='platform' AND code='root'")
	require.NoError(t, err)
	policy := iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state")
	_, err = runner.Apply(context.Background())
	require.ErrorContains(t, err, "iam_read_pages_catalog_and_root_must_be_publishable")
	require.Equal(t, policy, iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state"))
	require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code IN ('billing.vendor_report.read','channel.upstream_contract.read') AND status<>'draft'"))
}

func TestIAMReadPagesUpgradeRollsBackWhenAuditCannotBeRecorded(t *testing.T) {
	db, runner := iamReadPagesUpgrade(t)
	_, err := db.Exec("CREATE TRIGGER reject_read_pages_audit BEFORE INSERT ON iam_audit_events WHEN NEW.event_id='" + iamReadPagesReceipt + "' BEGIN SELECT RAISE(ABORT, 'isolated audit write failure'); END")
	require.NoError(t, err)
	policy := iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state")
	grants := iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions")
	rootRevision := iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'")
	_, err = runner.Apply(context.Background())
	require.ErrorContains(t, err, "isolated audit write failure")
	require.Equal(t, policy, iamReadPagesScalar(t, db, "SELECT policy_revision FROM iam_policy_state"))
	require.Equal(t, grants, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_role_permissions"))
	require.Equal(t, rootRevision, iamReadPagesScalar(t, db, "SELECT revision FROM iam_roles WHERE context_key='platform' AND code='root'"))
	require.Zero(t, iamReadPagesScalar(t, db, "SELECT COUNT(*) FROM iam_permissions WHERE code IN ('billing.vendor_report.read','channel.upstream_contract.read') AND (status<>'draft' OR revision<>1)"))
	_, err = db.Exec("DROP TRIGGER reject_read_pages_audit")
	require.NoError(t, err)
	_, err = runner.Apply(context.Background())
	require.NoError(t, err, "a failed transaction must not leave a stale release guard or receipt")
}
