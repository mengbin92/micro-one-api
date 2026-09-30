package data

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/migrate"
	dbtest "micro-one-api/platform/database/testutil"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func iamTestEvent(id string) biz.IAMAuditEvent {
	return biz.IAMAuditEvent{EventID: id, Actor: authorization.Actor{ServiceID: "a2-test"}, Context: authorization.Platform(), TargetContext: authorization.Platform(), Action: "iam.test", Target: "test", Before: "{}", After: "{}", Diff: "{}", Result: "success", RequestID: id, Reason: "storage acceptance", OccurredAt: time.Now().UTC()}
}
func iamTestAssignment(userID, roleID int64) biz.IAMAssignment {
	return biz.IAMAssignment{UserID: userID, RoleID: roleID, Context: authorization.Platform(), Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Validity: authorization.Interval{StartsAt: time.Now().Add(-time.Minute).UTC()}, Origin: "default"}
}
func iamSeedUser(t *testing.T, db *gorm.DB, name string) int64 {
	t.Helper()
	row := map[string]any{"username": name, "status": biz.UserStatusEnabled, "role": biz.RoleCommonUser, "aff_code": name}
	require.NoError(t, db.Table("users").Create(row).Error)
	var id int64
	require.NoError(t, db.Table("users").Select("id").Where("username = ?", name).Scan(&id).Error)
	return id
}
func iamCount(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table(table).Count(&n).Error)
	return n
}

func TestIAMStorageDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver) // isolated local file/schema; fresh + repeat full migrations
			if driver == "sqlite" {
				require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
			}
			d := &Data{db: db}
			repo := NewIAMRepo(d)
			runner := NewIAMTxRunner(d)
			ctx := context.Background()
			t.Run("seed_and_catalog", func(t *testing.T) {
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					p, err := repo.Policy(ctx, tx)
					require.NoError(t, err)
					require.Equal(t, "legacy", p.Mode)
					require.Equal(t, "idle", p.Cutover)
					require.Equal(t, uint64(1), p.PolicyRevision)
					require.Equal(t, authorization.CatalogRevision, p.CatalogRevision)
					roles, err := repo.Roles(ctx, tx, authorization.Platform())
					require.NoError(t, err)
					require.Len(t, roles, 4)
					require.Equal(t, "root", roles[3].Code)
					require.NotEmpty(t, roles[3].Grants)
					return nil
				}))
				require.Equal(t, int64(len(authorization.Catalog())), iamCount(t, db, "iam_permissions"))
				var bound int64
				require.NoError(t, db.Table("iam_permissions").Where("binding_state <> 'unbound'").Count(&bound).Error)
				require.Zero(t, bound)
				for _, op := range authorization.Catalog() {
					var row struct {
						SupportedScopes, SupportedContextTypes string
						Protected                              bool
					}
					require.NoError(t, db.Table("iam_permissions").Where("code = ?", op.Code).Take(&row).Error)
					var scopes []authorization.ScopeKind
					var contexts []string
					require.NoError(t, jsonx.Unmarshal([]byte(row.SupportedScopes), &scopes))
					require.NoError(t, jsonx.Unmarshal([]byte(row.SupportedContextTypes), &contexts))
					require.Equal(t, op.Scopes, scopes)
					require.Equal(t, op.ContextTypes, contexts)
					require.Equal(t, op.Protected, row.Protected)
				}
			})
			uid := iamSeedUser(t, db, "atomic-user")
			t.Run("atomic_assignment_audit_and_versions", func(t *testing.T) {
				fail := errors.New("injected rollback")
				err := runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					_, err := repo.SaveAssignment(ctx, tx, iamTestAssignment(uid, 2), 0)
					if err != nil {
						return err
					}
					if err := repo.AppendAudit(ctx, tx, iamTestEvent("rolled-back")); err != nil {
						return err
					}
					return fail
				})
				require.ErrorIs(t, err, fail)
				require.Zero(t, iamCount(t, db, "iam_user_roles"))
				require.Zero(t, iamCount(t, db, "iam_audit_events"))
				// Missing success audit also rolls back an otherwise valid mutation.
				require.ErrorIs(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					_, err := repo.SaveAssignment(ctx, tx, iamTestAssignment(uid, 2), 0)
					return err
				}), biz.ErrIAMInvalidRelation)
				require.Zero(t, iamCount(t, db, "iam_user_roles"))
				var saved biz.IAMAssignment
				require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					var err error
					saved, err = repo.SaveAssignment(ctx, tx, iamTestAssignment(uid, 2), 0)
					if err != nil {
						return err
					}
					return repo.AppendAudit(ctx, tx, iamTestEvent("committed"))
				}))
				require.Equal(t, uint64(1), saved.Revision)
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					v, err := repo.UserRevision(ctx, tx, uid)
					require.NoError(t, err)
					require.Equal(t, uint64(2), v)
					a, err := repo.Assignments(ctx, tx, authorization.Platform(), uid)
					require.NoError(t, err)
					require.Len(t, a, 1)
					require.Equal(t, saved.ID, a[0].ID)
					return nil
				}))
				// A duplicate success audit prevents both the relation and version update.
				require.ErrorIs(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					saved.Revoked = true
					if _, err := repo.SaveAssignment(ctx, tx, saved, 1); err != nil {
						return err
					}
					return repo.AppendAudit(ctx, tx, iamTestEvent("committed"))
				}), biz.ErrIAMInvalidRelation)
				failure := iamTestEvent("failed-attempt")
				failure.Result = "failure"
				require.NoError(t, repo.AppendFailureAudit(ctx, failure))
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					events, err := repo.AuditEvents(ctx, tx, authorization.Platform(), 10)
					require.NoError(t, err)
					require.Len(t, events, 2)
					return nil
				}))
			})
			t.Run("concurrent_policy_cas", func(t *testing.T) {
				const attempts = 8
				var base uint64
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					policy, err := repo.Policy(ctx, tx)
					base = policy.PolicyRevision
					return err
				}))
				var wg sync.WaitGroup
				results := make(chan error, attempts)
				for i := 0; i < attempts; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						results <- runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
							if err := repo.AdvancePolicy(ctx, tx, base, false); err != nil {
								return err
							}
							return repo.AppendAudit(ctx, tx, iamTestEvent(fmt.Sprintf("cas-%d", i)))
						})
					}(i)
				}
				wg.Wait()
				close(results)
				success, conflict := 0, 0
				for err := range results {
					if err == nil {
						success++
					} else {
						require.ErrorIs(t, err, biz.ErrIAMRevisionConflict)
						conflict++
					}
				}
				require.Equal(t, 1, success)
				require.Equal(t, attempts-1, conflict)
			})
			t.Run("snapshot_is_consistent", func(t *testing.T) {
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					identity, err := repo.User(ctx, tx, uid)
					if err != nil {
						return err
					}
					before, err := repo.Policy(ctx, tx)
					if err != nil {
						return err
					}
					oldUser, err := repo.UserRevision(ctx, tx, uid)
					if err != nil {
						return err
					}
					require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, write biz.IAMTx) error {
						if err := repo.AdvancePolicy(ctx, write, before.PolicyRevision, false); err != nil {
							return err
						}
						if err := repo.AdvanceUser(ctx, write, uid, oldUser); err != nil {
							return err
						}
						return repo.AppendAudit(ctx, write, iamTestEvent("snapshot-writer"))
					}))
					after, err := repo.Policy(ctx, tx)
					if err != nil {
						return err
					}
					v, err := repo.UserRevision(ctx, tx, uid)
					if err != nil {
						return err
					}
					require.Equal(t, before, after)
					require.Equal(t, oldUser, v)
					sameIdentity, err := repo.User(ctx, tx, uid)
					if err != nil {
						return err
					}
					require.Equal(t, identity, sameIdentity)
					return nil
				}))
			})
			t.Run("role_metadata_cas_and_protection", func(t *testing.T) {
				var role biz.IAMRole
				require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					var err error
					role, err = repo.SaveRole(ctx, tx, biz.IAMRole{Context: authorization.Platform(), Code: "a2-draft", Name: "draft", Status: "draft"}, 0)
					if err != nil {
						return err
					}
					return repo.AppendAudit(ctx, tx, iamTestEvent("draft-role"))
				}))
				require.Equal(t, uint64(1), role.Revision)
				role.Name = "updated"
				require.NoError(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					var err error
					role, err = repo.SaveRole(ctx, tx, role, 1)
					if err != nil {
						return err
					}
					return repo.AppendAudit(ctx, tx, iamTestEvent("updated-role"))
				}))
				require.Equal(t, uint64(2), role.Revision)
				require.ErrorIs(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error { _, err := repo.SaveRole(ctx, tx, role, 1); return err }), biz.ErrIAMRevisionConflict)
				require.ErrorIs(t, runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					_, err := repo.SaveRole(ctx, tx, biz.IAMRole{ID: 4, Context: authorization.Platform(), Code: "root", Status: "disabled"}, 1)
					return err
				}), biz.ErrIAMProtected)
				org := authorization.Context{Type: "organization", OrganizationID: 7, Key: "organization:7"}
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					_, err := repo.Roles(ctx, tx, org)
					require.ErrorIs(t, err, biz.ErrIAMContextInvalid)
					return nil
				}))
			})
			t.Run("transaction_ownership_and_readonly", func(t *testing.T) {
				var saved biz.IAMTx
				require.NoError(t, runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx biz.IAMTx) error {
					saved = tx
					require.ErrorIs(t, repo.AdvancePolicy(ctx, tx, 3, false), biz.ErrIAMDependencyUnavailable)
					_, err := NewIAMRepo(&Data{db: db}).Policy(ctx, tx)
					require.ErrorIs(t, err, biz.ErrIAMDependencyUnavailable)
					return nil
				}))
				_, err := repo.Policy(ctx, saved)
				require.ErrorIs(t, err, biz.ErrIAMDependencyUnavailable)
			})
			t.Run("account_oauth_routing_default_and_audit_share_transaction", func(t *testing.T) {
				t.Setenv("IDENTITY_ROUTING_V2", "true")
				// Account helpers use the existing routing v2 write path inside the IAM tx.
				beforeUsers := iamCount(t, db, "users")
				beforeOAuth := iamCount(t, db, "user_oauth_identities")
				beforeRouting := iamCount(t, db, "user_routing_group_grants")
				fail := errors.New("injected account rollback")
				for _, role := range []int64{2, 4} {
					err := runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
						user, err := repo.CreateUser(ctx, tx, biz.User{Username: fmt.Sprintf("rollback-%d", role), AffCode: fmt.Sprintf("rb-%d", role), Group: "default", DefaultRoutingGroupID: 1, Status: biz.UserStatusEnabled})
						if err != nil {
							return err
						}
						_, err = repo.CreateOAuthIdentity(ctx, tx, biz.OAuthIdentity{UserID: user.ID, Provider: "test", ProviderID: fmt.Sprintf("test-%d", role)})
						if err != nil {
							return err
						}
						_, err = repo.SaveAssignment(ctx, tx, iamTestAssignment(user.ID, role), 0)
						if err != nil {
							return err
						}
						if err := repo.AppendAudit(ctx, tx, iamTestEvent(fmt.Sprintf("account-%d", role))); err != nil {
							return err
						}
						return fail
					})
					require.ErrorIs(t, err, fail)
				}
				require.Equal(t, beforeUsers, iamCount(t, db, "users"))
				require.Equal(t, beforeOAuth, iamCount(t, db, "user_oauth_identities"))
				require.Equal(t, beforeRouting, iamCount(t, db, "user_routing_group_grants"))
			})
			t.Run("database_constraints", func(t *testing.T) { iamCheckConstraints(t, db, uid) })
			t.Run("negative_migration", func(t *testing.T) {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "999_iam_invalid.sql"), []byte("CREATE WRONG IAM;"), 0600))
				_, err = migrate.NewWithDriver(sqlDB, dir, driver).Apply(ctx)
				require.Error(t, err)
				var n int64
				require.NoError(t, db.Table("schema_migrations").Where("version = ?", "999_iam_invalid").Count(&n).Error)
				require.Zero(t, n)
			})
		})
	}
}

func iamCheckConstraints(t *testing.T, db *gorm.DB, uid int64) {
	t.Helper()
	org := map[string]any{"context_type": "organization", "organization_id": 7, "context_key": "organization:7", "code": "test", "name": "test", "description": "", "status": "draft"}
	require.NoError(t, db.Table("iam_roles").Create(org).Error)
	var orgID int64
	require.NoError(t, db.Table("iam_roles").Select("id").Where("context_key = ?", "organization:7").Scan(&orgID).Error)
	all := `{"clauses":[{"all":true}]}`
	tests := []struct {
		name, table string
		row         map[string]any
	}{
		{"noncanonical_context", "iam_roles", map[string]any{"context_type": "platform", "organization_id": 7, "context_key": "platform", "code": "bad", "name": "bad", "description": "", "status": "draft"}},
		{"cross_context_assignment", "iam_user_roles", map[string]any{"context_key": "platform", "user_id": uid, "role_id": orgID, "allow_boundary": all, "starts_at": 1, "status": "active", "origin": "default"}},
		{"cross_context_inheritance", "iam_role_inheritance", map[string]any{"context_key": "platform", "senior_role_id": 2, "junior_role_id": orgID}},
		{"self_inheritance", "iam_role_inheritance", map[string]any{"context_key": "platform", "senior_role_id": 2, "junior_role_id": 2}},
		{"invalid_interval", "iam_user_roles", map[string]any{"context_key": "platform", "user_id": uid, "role_id": 1, "allow_boundary": all, "starts_at": 2, "expires_at": 2, "status": "active", "origin": "default"}},
		{"missing_candidate_batch", "iam_user_roles", map[string]any{"context_key": "platform", "user_id": uid, "role_id": 1, "allow_boundary": all, "starts_at": 1, "status": "active", "origin": "legacy_candidate"}},
		{"invalid_delegation_kind_fields", "iam_delegations", map[string]any{"context_key": "platform", "manager_role_id": 2, "target_kind": "user_credentials", "target_role_id": 1, "actions": "[]", "target_user_scope": all, "grant_ceiling": "[]", "starts_at": 1}},
		{"cross_context_delegation", "iam_delegations", map[string]any{"context_key": "platform", "manager_role_id": 2, "target_kind": "role", "target_role_id": orgID, "actions": "[]", "target_user_scope": all, "grant_ceiling": "[]", "starts_at": 1}},
		{"invalid_constraint", "iam_role_constraints", map[string]any{"context_key": "platform", "type": "SSD", "name": "invalid", "max_count": 0}},
		{"invalid_policy_singleton", "iam_policy_state", map[string]any{"id": 2, "policy_revision": 1, "catalog_revision": 1, "authorization_mode": "legacy", "cutover_state": "idle"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { require.Error(t, db.Table(tt.table).Create(tt.row).Error) })
	}
	require.Error(t, db.Table("iam_policy_state").Where("id = 1").Update("authorization_mode", "iam").Error)
	// A session can only activate roles in its own context.
	require.NoError(t, db.Table("iam_sessions").Create(map[string]any{"session_id": "constraint-jti", "user_id": uid, "expires_at": time.Now().Add(time.Hour).UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_session_contexts").Create(map[string]any{"session_id": "constraint-jti", "context_key": "platform", "activation_state": "active"}).Error)
	require.Error(t, db.Table("iam_session_roles").Create(map[string]any{"session_id": "constraint-jti", "context_key": "platform", "role_id": orgID}).Error)
	require.NoError(t, db.Table("iam_role_constraints").Create(map[string]any{"context_key": "platform", "type": "SSD", "name": "test", "max_count": 1}).Error)
	var cid int64
	require.NoError(t, db.Table("iam_role_constraints").Select("id").Where("name = ?", "test").Scan(&cid).Error)
	require.Error(t, db.Table("iam_role_constraint_members").Create(map[string]any{"context_key": "platform", "constraint_id": cid, "role_id": orgID}).Error)
}

func TestIAMSQLiteBusyRetry(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	var files []struct {
		Seq        int
		Name, File string
	}
	require.NoError(t, db.Raw("PRAGMA database_list").Scan(&files).Error)
	require.NotEmpty(t, files)
	contender, err := gorm.Open(sqlite.Open(files[0].File+"?_foreign_keys=on&_busy_timeout=1"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := contender.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var attempts atomic.Int32
	first := make(chan struct{})
	var once sync.Once
	require.NoError(t, contender.Callback().Raw().Before("gorm:raw").Register("iam-busy-attempt", func(db *gorm.DB) {
		if strings.HasPrefix(db.Statement.SQL.String(), "UPDATE iam_policy_state") {
			attempts.Add(1)
			once.Do(func() { close(first) })
		}
	}))
	locked := db.Begin()
	require.NoError(t, locked.Error)
	require.NoError(t, locked.Exec("UPDATE iam_policy_state SET policy_revision = policy_revision WHERE id = 1").Error)
	d := &Data{db: contender}
	repo := NewIAMRepo(d)
	runner := NewIAMTxRunner(d)
	completed := make(chan error, 1)
	var callbacks atomic.Int32
	go func() {
		completed <- runner.RunIAMWrite(context.Background(), func(ctx context.Context, tx biz.IAMTx) error {
			callbacks.Add(1)
			if err := repo.AdvancePolicy(ctx, tx, 1, false); err != nil {
				return err
			}
			return repo.AppendAudit(ctx, tx, iamTestEvent("busy-retry"))
		})
	}()
	<-first
	time.Sleep(35 * time.Millisecond)
	require.NoError(t, locked.Rollback().Error)
	require.NoError(t, <-completed)
	require.Greater(t, attempts.Load(), int32(1))
	require.Equal(t, int32(1), callbacks.Load())
	require.Equal(t, int64(1), iamCount(t, db, "iam_audit_events"))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, runner.RunIAMWrite(cancelled, func(context.Context, biz.IAMTx) error { t.Error("cancelled callback ran"); return nil }), context.Canceled)
	event := iamTestEvent("audit-unavailable")
	event.Result = "failure"
	require.ErrorIs(t, NewIAMRepo(&Data{}).AppendFailureAudit(context.Background(), event), biz.ErrIAMDependencyUnavailable)
}

// This verifies the A2 storage primitive, not runtime bootstrap adoption (A4).
func TestIAMConcurrentBootstrapStorage(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			ctx := context.Background()
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					d := &Data{db: db}
					repo := NewIAMRepo(d)
					runner := NewIAMTxRunner(d)
					results <- runner.RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error {
						p, err := repo.Policy(ctx, tx)
						if err != nil {
							return err
						}
						if err := p.CheckWrite(authorization.BootstrapWrite, false); err != nil {
							return err
						}
						count, err := repo.CountUsers(ctx, tx)
						if err != nil {
							return err
						}
						if count != 0 {
							return nil
						}
						user, err := repo.CreateUser(ctx, tx, biz.User{Username: "root", AffCode: "root", Status: biz.UserStatusEnabled, Role: biz.RoleRootUser})
						if err != nil {
							return err
						}
						a := iamTestAssignment(user.ID, 4)
						a.Origin = "bootstrap"
						if _, err := repo.SaveAssignment(ctx, tx, a, 0); err != nil {
							return err
						}
						return repo.AppendAudit(ctx, tx, iamTestEvent(fmt.Sprintf("bootstrap-%d", i)))
					})
				}(i)
			}
			for i := 0; i < 2; i++ {
				require.NoError(t, <-results)
			}
			require.Equal(t, int64(1), iamCount(t, db, "users"))
			require.Equal(t, int64(1), iamCount(t, db, "iam_user_roles"))
			require.Equal(t, int64(1), iamCount(t, db, "iam_audit_events"))
		})
	}
}
