package data

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	dbtest "micro-one-api/platform/database/testutil"
)

func migrationEvidence(root int64, manifest biz.IAMMigrationManifest) *biz.IAMCutoverEvidence {
	now := time.Now().UTC()
	return &biz.IAMCutoverEvidence{BatchID: "d0-test", RootUserID: root, SourceDigest: fmt.Sprintf("%064d", 1), ManifestDigest: biz.IAMMigrationDigest(manifest), DatabaseIdentity: "scratch", CapturedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Barrier: "test external barrier", Drained: "no inflight transactions", OldWritersExited: "old writer stopped", OldDBChannelsRevoked: "denied old channel", FinanceIsolation: "finance paused", FinanceReplay: "idempotent replay", Rollback: "IAM-compatible image", Frontend: "C frontend", Regression: "six mandatory regressions", Instances: map[string]string{"admin": "a", "identity": "i", "channel": "c", "billing": "b", "config": "f", "log": "l", "monitor": "m", "notify": "n", "relay": "r"}}
}

func migrationSeed(t *testing.T, db *gorm.DB, name string, role int32) int64 {
	id := iamSeedUser(t, db, name)
	require.NoError(t, db.Table("users").Where("id = ?", id).Update("role", role).Error)
	return id
}

func TestIAMMigrationDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			d := &Data{db: db}
			repo := NewIAMMigrationRepo(d)
			runner := NewIAMTxRunner(d)
			uc := biz.NewIAMMigrationUsecase(repo, runner)
			ctx := context.Background()
			root := migrationSeed(t, db, "root-account", 100)
			admin := migrationSeed(t, db, "inactive-admin", 10)
			deleted := migrationSeed(t, db, "later-deleted", 1)
			guest := migrationSeed(t, db, "guest", 0)
			require.NoError(t, db.Table("users").Where("id = ?", guest).Update("status", 2).Error)
			manifest := biz.IAMMigrationManifest{}
			evidence := migrationEvidence(root, manifest)
			serial := 0
			execute := func(command string) (biz.IAMMigrationReport, error) {
				serial++
				status, err := uc.Execute(ctx, biz.IAMMigrationRequest{Command: "status"})
				require.NoError(t, err)
				return uc.Execute(ctx, biz.IAMMigrationRequest{Command: command, BatchID: "d0-test", Reason: "D0 isolated rehearsal", RequestID: fmt.Sprint("step-", serial), ExpectedPolicyRevision: status.Policy.PolicyRevision, Evidence: evidence, Manifest: manifest})
			}
			// Preserve a non-candidate bootstrap/default relation and its immutable ID.
			var preserved biz.IAMAssignment
			require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
				var err error
				preserved, err = repo.SaveAssignment(ctx, tx, iamTestAssignment(root, 4), 0)
				if err != nil {
					return err
				}
				return repo.AppendAudit(ctx, tx, iamTestEvent("preserved"))
			}))
			inventory, err := execute("inventory")
			require.NoError(t, err)
			require.Len(t, inventory.Users, 4)
			require.False(t, inventory.Verified)
			report, err := execute("apply")
			require.NoError(t, err, "report: %+v", report)
			require.True(t, report.Verified)
			require.Equal(t, int64(4), iamCount(t, db, "iam_user_roles"))
			// Online candidates can predate the final cutover batch. Replace only
			// those candidate rows, even when the unique user/role key is unchanged.
			require.NoError(t, db.Table("iam_user_roles").Where("origin = ?", "legacy_candidate").Update("migration_batch_id", "prior-online-batch").Error)
			_, err = execute("apply")
			require.NoError(t, err)
			shadow, err := execute("shadow")
			require.NoError(t, err)
			require.True(t, shadow.Verified)
			for _, diff := range shadow.Differences {
				require.NotEqual(t, "unexpected", diff.Kind, "%+v", diff)
			}
			// Changes after online candidates include an account with no requests.
			require.NoError(t, db.Table("users").Where("id = ?", admin).Update("role", 1).Error)
			newUser := migrationSeed(t, db, "never-requested", 1)
			require.NoError(t, db.Create(&iamSessionModel{SessionID: "deleted-session", UserID: deleted, ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Revision: 1}).Error)
			require.NoError(t, db.Create(&iamSessionContextModel{SessionID: "deleted-session", ContextKey: "platform", ActivationState: "active", Revision: 1}).Error)
			require.NoError(t, db.Create(&iamSessionRoleModel{SessionID: "deleted-session", ContextKey: "platform", RoleID: 2}).Error)
			require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
				if err := repo.DeleteAccount(ctx, tx, deleted); err != nil {
					return err
				}
				return repo.AppendAudit(ctx, tx, iamTestEvent("deleted"))
			}))
			before, err := execute("verify")
			require.NoError(t, err)
			require.Contains(t, before.MismatchedUsers, admin)
			require.Contains(t, before.MismatchedUsers, newUser)
			require.Empty(t, before.OrphanAssignments)
			_, err = execute("block")
			require.NoError(t, err)
			require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
				p, err := repo.Policy(ctx, tx)
				require.NoError(t, err)
				require.Error(t, p.CheckWrite(authorization.LegacyAccountWrite, false))
				require.Error(t, p.CheckWrite(authorization.IAMManagementWrite, false))
				return nil
			}))
			// Unknown roles prevent the entire final rebuild, keeping blocked.
			require.NoError(t, db.Table("users").Where("id = ?", newUser).Update("role", 77).Error)
			_, err = execute("activate")
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			state, err := execute("status")
			require.NoError(t, err)
			require.Equal(t, "blocked", state.Policy.Cutover)
			require.Equal(t, int64(3), iamCount(t, db, "iam_user_roles"))
			require.NoError(t, db.Table("users").Where("id = ?", newUser).Update("role", 1).Error)
			// Failure after storage mutations (duplicate success audit) rolls back
			// assignments, versions, catalog, cleanup and mode in every dialect.
			status, err := execute("status")
			require.NoError(t, err)
			req := biz.IAMMigrationRequest{Command: "activate", BatchID: "d0-test", ExpectedPolicyRevision: status.Policy.PolicyRevision, Reason: "audit failure", RequestID: "preserved", Evidence: evidence}
			_, err = uc.Execute(ctx, req)
			require.Error(t, err)
			require.Equal(t, int64(3), iamCount(t, db, "iam_user_roles"))
			require.Zero(t, iamCount(t, db, "iam_sessions"))
			rebuilt, err := execute("rebuild")
			require.NoError(t, err)
			require.True(t, rebuilt.Verified)
			require.Equal(t, int64(4), iamCount(t, db, "iam_user_roles"))
			require.Zero(t, iamCount(t, db, "iam_sessions"))
			var preservedRow iamAssignmentModel
			require.NoError(t, db.First(&preservedRow, preserved.ID).Error)
			require.Equal(t, "default", preservedRow.Origin)
			// Replay the exact committed request with its now-stale CAS. A lost
			// response must return the receipt, without repeating writes or audits.
			status, err = execute("status")
			require.NoError(t, err)
			replay := biz.IAMMigrationRequest{Command: "rebuild", BatchID: "d0-test", ExpectedPolicyRevision: status.Policy.PolicyRevision, Reason: "rebuild retry", RequestID: "rebuild-replay", Evidence: evidence}
			committed, err := uc.Execute(ctx, replay)
			require.NoError(t, err)
			auditCount := iamCount(t, db, "iam_audit_events")
			received, err := uc.Execute(ctx, replay)
			require.NoError(t, err)
			require.Equal(t, committed, received)
			require.Equal(t, auditCount, iamCount(t, db, "iam_audit_events"))
			replay.Reason = "changed request with reused ID"
			_, err = uc.Execute(ctx, replay)
			require.Error(t, err)
			again, err := execute("rebuild")
			require.NoError(t, err)
			require.Equal(t, rebuilt.Digest, again.Digest)
			// Import is atomic: a protected second role rolls back the first one.
			maxMembers := int64(1)
			custom := biz.IAMRole{Context: authorization.Platform(), Code: "d0-self", Name: "D0 self", Status: "enabled", MaxMembers: &maxMembers, Grants: []biz.IAMGrant{{Operation: "identity.session.roles.read", Effect: authorization.Allow, Scope: authorization.Scope{Clauses: []authorization.Clause{{Self: true}}}}}}
			manifest.Roles = []biz.IAMRole{custom, {Context: authorization.Platform(), Code: "root", Builtin: true, Status: "enabled"}}
			evidence.ManifestDigest = biz.IAMMigrationDigest(manifest)
			_, err = execute("import")
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			require.Equal(t, int64(4), iamCount(t, db, "iam_roles"))
			manifest.Roles = []biz.IAMRole{custom}
			evidence.ManifestDigest = biz.IAMMigrationDigest(manifest)
			imported, err := execute("import")
			require.NoError(t, err)
			manifest = imported.Manifest
			require.Len(t, manifest.Roles, 1)
			// Future cardinality violations block the whole import, before those
			// assignments become active. One future assignment is valid.
			future := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
			assignment := func(user int64) biz.IAMAssignment {
				return biz.IAMAssignment{UserID: user, RoleID: manifest.Roles[0].ID, Context: authorization.Platform(), Origin: "explicit", Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Validity: authorization.Interval{StartsAt: future}}
			}
			manifest.Assignments = []biz.IAMAssignment{assignment(admin), assignment(newUser)}
			evidence.ManifestDigest = biz.IAMMigrationDigest(manifest)
			conflicted, err := execute("import")
			require.Error(t, err)
			require.NotEmpty(t, conflicted.Conflicts)
			require.Equal(t, int64(4), iamCount(t, db, "iam_user_roles"))
			manifest.Assignments = manifest.Assignments[:1]
			evidence.ManifestDigest = biz.IAMMigrationDigest(manifest)
			imported, err = execute("import")
			require.NoError(t, err)
			approved := imported.Manifest
			manifest = biz.IAMMigrationManifest{}
			evidence.ManifestDigest = biz.IAMMigrationDigest(manifest)
			_, err = execute("activate")
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			manifest = approved
			evidence.ManifestDigest = biz.IAMMigrationDigest(manifest)
			activated, err := execute("activate")
			require.NoError(t, err)
			require.Equal(t, "iam", activated.Policy.Mode)
			require.Equal(t, "verified", activated.Policy.Cutover)
			// A failed complete cannot restart legacy backfill or open normal writes.
			evidence.Frontend = ""
			_, err = execute("resume")
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			_, err = execute("rebuild")
			require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
			_, err = execute("apply")
			require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
			state, err = execute("status")
			require.NoError(t, err)
			require.Equal(t, "verified", state.Policy.Cutover)
			// After verified, numeric-role drift never becomes new IAM authority.
			require.NoError(t, db.Table("users").Where("id = ?", root).Update("role", 1).Error)
			evidence.Frontend = "C frontend"
			completed, err := execute("resume")
			require.NoError(t, err)
			require.Equal(t, "complete", completed.Policy.Cutover)
			require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
				p, err := repo.Policy(ctx, tx)
				require.NoError(t, err)
				require.NoError(t, p.CheckWrite(authorization.IAMManagementWrite, false))
				require.Error(t, p.CheckWrite(authorization.LegacyAccountWrite, false))
				return nil
			}))
			_, err = execute("resume")
			require.NoError(t, err)
		})
	}
}
