package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"
)

type iamA4Fixture struct {
	t      *testing.T
	db     *gorm.DB
	uc     *biz.IdentityUsecase
	repo   biz.IAMRuntimeRepo
	runner biz.IAMTxRunner
	ctx    context.Context
	seq    int
}

func newIAMA4Fixture(t *testing.T, driver string) *iamA4Fixture {
	t.Helper()
	db := dbtest.RoutingContextDB(t, driver)
	if driver == "sqlite" {
		require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	}
	t.Setenv("JWT_SECRET_KEY", "a4-isolated-secret")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "a4-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	d := &Data{db: db}
	repo := NewIAMRuntimeRepo(d)
	runner := NewIAMTxRunner(d)
	uc := biz.NewIdentityUsecase(NewRepository(d), nil)
	uc.SetIAMRuntime(repo, runner)
	return &iamA4Fixture{t: t, db: db, uc: uc, repo: repo, runner: runner, ctx: context.Background()}
}
func (f *iamA4Fixture) mode(mode, state string) {
	f.t.Helper()
	updates := map[string]any{"authorization_mode": mode, "cutover_state": state, "cutover_batch_id": "a4-isolated", "cutover_verified_at": time.Now().UnixMilli()}
	if mode == "legacy" {
		updates["cutover_verified_at"] = nil
	}
	if state == "idle" {
		updates["cutover_batch_id"] = ""
	}
	require.NoError(f.t, f.db.Table("iam_policy_state").Where("id = 1").Updates(updates).Error)
}
func (f *iamA4Fixture) register(name string) *biz.User {
	f.t.Helper()
	u, err := f.uc.Register(f.ctx, name, "password123", name+"@example.com", "default")
	require.NoError(f.t, err)
	return u
}
func (f *iamA4Fixture) token(uid int64, jti string, epoch int64, exp time.Time) string {
	f.t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, biz.UserSessionClaims{UserID: uid, Role: 100, PwdEpoch: epoch, TokenType: "user_session", RegisteredClaims: jwt.RegisteredClaims{ID: jti, Subject: fmt.Sprint(uid), Issuer: "micro-one-api", Audience: []string{"micro-one-api-web"}, ExpiresAt: jwt.NewNumericDate(exp)}}).SignedString([]byte("a4-isolated-secret"))
	require.NoError(f.t, err)
	return raw
}
func (f *iamA4Fixture) versions(uid int64) (uint64, uint64) {
	f.t.Helper()
	var u, p uint64
	require.NoError(f.t, f.runner.ReadIAMSnapshot(f.ctx, func(ctx context.Context, tx biz.IAMTx) error {
		policy, err := f.repo.Policy(ctx, tx)
		if err != nil {
			return err
		}
		p = policy.PolicyRevision
		u, err = f.repo.UserRevision(ctx, tx, uid)
		return err
	}))
	return u, p
}
func (f *iamA4Fixture) setup(fn func(context.Context, biz.IAMTx) error) {
	f.t.Helper()
	f.seq++
	require.NoError(f.t, f.runner.RunIAMWrite(f.ctx, func(ctx context.Context, tx biz.IAMTx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return f.repo.AppendAudit(ctx, tx, iamTestEvent(fmt.Sprintf("a4-setup-%d", f.seq)))
	}))
}
func (f *iamA4Fixture) role(code string) int64 {
	f.t.Helper()
	var id int64
	f.setup(func(ctx context.Context, tx biz.IAMTx) error {
		r, err := f.repo.SaveRole(ctx, tx, biz.IAMRole{Context: authorization.Platform(), Code: code, Name: code, Status: "enabled"}, 0)
		id = r.ID
		return err
	})
	return id
}
func (f *iamA4Fixture) assign(uid, role int64, start time.Time, end *time.Time) {
	f.t.Helper()
	f.setup(func(ctx context.Context, tx biz.IAMTx) error {
		a := iamTestAssignment(uid, role)
		a.Origin = "explicit"
		a.Validity = authorization.Interval{StartsAt: start, ExpiresAt: end}
		_, err := f.repo.SaveAssignment(ctx, tx, a, 0)
		return err
	})
}
func (f *iamA4Fixture) failCreate(table string) func() {
	f.t.Helper()
	name := "a4-injected-create"
	require.NoError(f.t, f.db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(errors.New("injected A4 failure"))
		}
	}))
	return func() { require.NoError(f.t, f.db.Callback().Create().Remove(name)) }
}

func TestIAMA4AccountRuntimeDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			t.Run("concurrent_real_bootstrap", func(t *testing.T) {
				var wg sync.WaitGroup
				results := make(chan *biz.BootstrapResult, 2)
				errs := make(chan error, 2)
				for range 2 {
					wg.Add(1)
					go func() { defer wg.Done(); result, err := f.uc.EnsureRootAdmin(f.ctx); results <- result; errs <- err }()
				}
				wg.Wait()
				close(results)
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
				created := 0
				for r := range results {
					if r.Created {
						created++
					} else {
						require.Empty(t, r.PlainPassword)
					}
				}
				require.Equal(t, 1, created)
				require.EqualValues(t, 1, iamCount(t, f.db, "users"))
				require.EqualValues(t, 1, iamCount(t, f.db, "iam_user_roles"))
				require.EqualValues(t, 1, iamCount(t, f.db, "iam_audit_events"), "only the successful bootstrap emits an event")
				var a iamAssignmentModel
				require.NoError(t, f.db.First(&a).Error)
				require.Equal(t, "bootstrap", a.Origin)
				require.EqualValues(t, 4, a.RoleID)
			})
			root, err := f.uc.GetUser(f.ctx, 1)
			require.NoError(t, err)
			t.Run("all_creation_entries", func(t *testing.T) {
				registered := f.register("registered")
				require.NoError(t, f.uc.UpdateSelf(f.ctx, root.ID, "", "Root label", "", "", true))
				code, err := f.uc.GetOrCreateAffCode(f.ctx, root.ID)
				require.NoError(t, err)
				invited, err := f.uc.RegisterWithAffCode(f.ctx, "invited", "password123", "invited@example.com", "default", code)
				require.NoError(t, err)
				require.Equal(t, root.ID, invited.InviterID)
				admin, err := f.uc.CreateUser(f.ctx, "admin-created", "Created", "created@example.com", "password123", "default", 0)
				require.NoError(t, err)
				oauth, _, created, err := f.uc.OAuthLogin(f.ctx, "github", "a4-id", "oauth", "oauth@example.com", "OAuth")
				require.NoError(t, err)
				require.True(t, created)
				for _, u := range []*biz.User{registered, invited, admin, oauth} {
					var a iamAssignmentModel
					require.NoError(t, f.db.Where("user_id = ?", u.ID).Take(&a).Error)
					require.Equal(t, "default", a.Origin)
					require.EqualValues(t, 2, a.RoleID)
				}
				// Existing OAuth logins must not overwrite a previously governed assignment.
				f.assign(oauth.ID, 3, time.Now().Add(-time.Minute), nil)
				before := iamCount(t, f.db, "iam_user_roles")
				_, _, created, err = f.uc.OAuthLogin(f.ctx, "github", "a4-id", "ignored", "", "ignored")
				require.NoError(t, err)
				require.False(t, created)
				require.Equal(t, before, iamCount(t, f.db, "iam_user_roles"))
			})
			t.Run("oauth_and_audit_failure_rollback", func(t *testing.T) {
				beforeUsers := iamCount(t, f.db, "users")
				beforeAssignments := iamCount(t, f.db, "iam_user_roles")
				_, policy := f.versions(root.ID)
				cleanup := f.failCreate("user_oauth_identities")
				user, _, created, err := f.uc.OAuthLogin(f.ctx, "oidc", "failed-id", "failed-oauth", "", "failed")
				require.Error(t, err)
				require.Nil(t, user)
				require.False(t, created)
				cleanup()
				require.Equal(t, beforeUsers, iamCount(t, f.db, "users"))
				require.Equal(t, beforeAssignments, iamCount(t, f.db, "iam_user_roles"))
				_, after := f.versions(root.ID)
				require.Equal(t, policy, after)
				cleanup = f.failCreate("iam_audit_events")
				user, err = f.uc.Register(f.ctx, "failed-audit", "password123", "", "default")
				require.Error(t, err)
				require.Contains(t, err.Error(), "failure audit")
				require.Nil(t, user)
				cleanup()
				require.Equal(t, beforeUsers, iamCount(t, f.db, "users"))
				require.Equal(t, beforeAssignments, iamCount(t, f.db, "iam_user_roles"))
			})
			t.Run("explicit_fields_and_credential_revocation", func(t *testing.T) {
				u := f.register("credential-user")
				raw := f.token(u.ID, "old-password-jti", 0, time.Now().Add(time.Hour))
				f.mode("iam", "complete")
				snap, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
				require.NoError(t, err)
				require.Equal(t, "old-password-jti", snap.Session.SessionID)
				f.mode("legacy", "idle")
				require.NoError(t, f.db.Table("users").Where("id = ?", u.ID).Update("balance", 999).Error)
				require.NoError(t, f.uc.UpdateSelf(f.ctx, u.ID, "", "New label", "new-password123", "password123", true))
				stored, err := f.uc.GetUser(f.ctx, u.ID)
				require.NoError(t, err)
				require.EqualValues(t, 999, stored.Balance)
				require.Equal(t, biz.RoleCommonUser, stored.Role)
				require.Equal(t, u.Email, stored.Email)
				require.Greater(t, stored.PasswordChangedAt, int64(0))
				var s iamSessionModel
				require.NoError(t, f.db.Where("session_id = ?", "old-password-jti").Take(&s).Error)
				require.NotNil(t, s.RevokedAt)
				require.EqualValues(t, 2, s.Revision)
				_, err = f.uc.ValidateSessionToken(f.ctx, raw)
				require.ErrorIs(t, err, biz.ErrSessionRevoked)
				require.NoError(t, f.uc.DeleteUser(f.ctx, u.ID))
				require.ErrorIs(t, f.uc.DeleteUser(f.ctx, root.ID), biz.ErrIAMProtected)
			})
			t.Run("cutover_gates_and_raw_writer_rejection", func(t *testing.T) {
				raw := NewRepository(&Data{db: f.db})
				require.ErrorIs(t, raw.CreateUser(f.ctx, &biz.User{}), biz.ErrIAMProtected)
				require.ErrorIs(t, raw.UpdateUser(f.ctx, root), biz.ErrIAMProtected)
				require.ErrorIs(t, raw.DeleteUser(f.ctx, root.ID), biz.ErrIAMProtected)
				for _, state := range []struct{ mode, cutover string }{{"legacy", "blocked"}, {"iam", "verified"}, {"iam", "complete"}} {
					f.mode(state.mode, state.cutover)
					if state.cutover == "complete" {
						// B1 opens only the verified self path. This old caller has no
						// independently verified JWT/JTI and still cannot mutate.
						require.ErrorIs(t, f.uc.UpdateSelfEmail(f.ctx, root.ID, "new@example.com"), biz.ErrInvalidToken)
					} else {
						require.ErrorIs(t, f.uc.UpdateSelfEmail(f.ctx, root.ID, "new@example.com"), biz.ErrIAMCutoverBlocked)
					}
					_, err := f.uc.SetRole(f.ctx, root, 2, biz.RoleAdminUser)
					require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
					require.ErrorIs(t, f.uc.DeleteUser(f.ctx, 2), biz.ErrIAMCutoverBlocked)
					_, err = f.uc.CreateUser(f.ctx, "old-admin-path", "x", "", "", "default", 0)
					require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
					if state.cutover != "complete" {
						_, err = f.uc.Register(f.ctx, "blocked-new", "password123", "", "default")
						require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
					}
				}
				// IAM public registration writes the default assignment atomically.
				fresh := f.register("iam-registered")
				require.Positive(t, fresh.ID)
				f.mode("legacy", "idle")
			})
		})
	}
}

func TestIAMA4SessionRuntimeDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			user := f.register("session-user")
			f.mode("iam", "complete")
			r1, r2 := f.role("session-a"), f.role("session-b")
			f.assign(user.ID, r1, time.Now().Add(-time.Hour), nil)
			f.assign(user.ID, r2, time.Now().Add(-time.Hour), nil)
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				return f.repo.SaveConstraint(ctx, tx, biz.IAMRoleConstraint{Context: authorization.Platform(), Kind: "DSD", Name: "a-or-b", RoleIDs: []int64{r1, r2}, MaxCount: 1, Enabled: true}, 0)
			})
			raw := f.token(user.ID, "real-legacy-jti", 0, time.Now().Add(2*time.Hour))
			snap, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			require.Equal(t, "selection_required", snap.Session.ActivationState)
			require.Empty(t, snap.ActiveRoleIDs)
			require.ElementsMatch(t, []int64{2, r1, r2}, snap.AuthorizedRoleIDs)
			require.EqualValues(t, 1, iamCount(t, f.db, "iam_sessions"))
			require.EqualValues(t, 3, iamCount(t, f.db, "iam_user_roles"))
			require.Empty(t, snap.User.PasswordHash)
			_, err = f.uc.ActivateSessionRoles(f.ctx, raw, authorization.Platform(), []int64{r1, r2}, snap.Session.Revision, "conflicting")
			require.ErrorIs(t, err, biz.ErrIAMConstraintsViolated)
			_, err = f.uc.ActivateSessionRoles(f.ctx, raw, authorization.Platform(), []int64{4}, snap.Session.Revision, "forged root")
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			active, err := f.uc.ActivateSessionRoles(f.ctx, raw, authorization.Platform(), []int64{r1}, snap.Session.Revision, "choose a")
			require.NoError(t, err)
			require.Equal(t, []int64{r1}, active.ActiveRoleIDs)
			require.Equal(t, snap.Versions.User, active.Versions.User)
			require.Equal(t, snap.Versions.Policy, active.Versions.Policy)
			require.Equal(t, snap.Session.Revision+1, active.Versions.SessionContext)
			_, err = f.uc.ActivateSessionRoles(f.ctx, raw, authorization.Platform(), []int64{r2}, snap.Session.Revision, "stale version")
			require.ErrorIs(t, err, biz.ErrIAMRevisionConflict)
			// The JWT role=100 cannot authorize root or legacy admin guards in IAM.
			authenticated, err := f.uc.ValidateSessionToken(f.ctx, raw)
			require.NoError(t, err)
			require.False(t, authenticated.IsAdmin())
			org := authorization.Context{Type: "organization", OrganizationID: 1, Key: "organization:1"}
			_, err = f.uc.GetSessionAuthorization(f.ctx, raw, org)
			require.ErrorIs(t, err, biz.ErrIAMContextInvalid)
			expired := f.token(user.ID, "expired-jti", 0, time.Now().Add(-time.Hour))
			_, err = f.uc.GetSessionAuthorization(f.ctx, expired, authorization.Platform())
			require.ErrorIs(t, err, biz.ErrInvalidToken)
			require.EqualValues(t, 1, iamCount(t, f.db, "iam_sessions"))
			require.NoError(t, f.uc.RevokeOwnSession(f.ctx, raw, authorization.Platform(), false, active.Session.Revision, "sign out platform"))
			_, err = f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.ErrorIs(t, err, biz.ErrSessionRevoked)
			require.EqualValues(t, 1, iamCount(t, f.db, "iam_sessions"))
			another := f.token(user.ID, "second-real-jti", 0, time.Now().Add(time.Hour))
			snap, err = f.uc.GetSessionAuthorization(f.ctx, another, authorization.Platform())
			require.NoError(t, err)
			require.NoError(t, f.uc.RevokeOwnSession(f.ctx, another, authorization.Platform(), true, snap.Session.SessionRevision, "sign out all contexts"))
			_, err = f.uc.ValidateSessionToken(f.ctx, another)
			require.ErrorIs(t, err, biz.ErrSessionRevoked)
			// A numeric JTI is still an opaque session key, never a user ID.
			numeric := f.token(user.ID, "999999", 0, time.Now().Add(time.Hour))
			numericSnap, err := f.uc.GetSessionAuthorization(f.ctx, numeric, authorization.Platform())
			require.NoError(t, err)
			require.Equal(t, "999999", numericSnap.Session.SessionID)
			require.Equal(t, user.ID, numericSnap.User.ID)
			var event iamAuditModel
			require.NoError(t, f.db.Where("action = ? AND target = ?", "session.initialize", "999999").Take(&event).Error)
			auditEvent, err := event.toBiz()
			require.NoError(t, err)
			require.Equal(t, numericSnap.Versions.User, auditEvent.Versions.User)

		})
	}
}

func TestIAMA4SnapshotSourcesTimeAndRescueDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			root, err := f.uc.GetUser(f.ctx, 1)
			require.NoError(t, err)
			user := f.register("source-user")
			f.mode("iam", "complete")
			active, inactive := f.role("active-source"), f.role("mandatory-deny")
			all, _ := jsonx.Marshal(authorization.Scope{Clauses: []authorization.Clause{{All: true}}})
			var permission int64
			require.NoError(t, f.db.Table("iam_permissions").Select("id").Where("code = ?", "identity.user.read").Scan(&permission).Error)
			require.Positive(t, permission)
			for _, entry := range []struct {
				role   int64
				effect string
			}{{active, "allow"}, {inactive, "deny"}} {
				require.NoError(t, f.db.Table("iam_role_permissions").Create(map[string]any{"context_key": "platform", "role_id": entry.role, "permission_id": permission, "effect": entry.effect, "scope_descriptor": string(all)}).Error)
			}
			until := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			future := time.Now().Add(30 * time.Minute).Truncate(time.Millisecond)
			f.assign(user.ID, active, time.Now().Add(-time.Hour), &until)
			f.assign(user.ID, inactive, future, nil)
			raw := f.token(user.ID, "source-jti", 0, time.Now().Add(time.Hour))
			snap, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			snap, err = f.uc.ActivateSessionRoles(f.ctx, raw, authorization.Platform(), []int64{active}, snap.Session.Revision, "choose source")
			require.NoError(t, err)
			require.Equal(t, future.UTC(), snap.ValidUntil)
			require.Len(t, snap.Sources, 2)
			require.True(t, snap.Sources[0].Active)
			require.False(t, snap.Sources[1].Active)
			require.Equal(t, authorization.Deny, snap.Sources[1].Effect)
			// Revocation affects the next snapshot with no timer/cache cleanup.
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				as, err := f.repo.Assignments(ctx, tx, authorization.Platform(), user.ID)
				if err != nil {
					return err
				}
				for _, a := range as {
					if a.RoleID == active {
						a.Revoked = true
						_, err = f.repo.SaveAssignment(ctx, tx, a, a.Revision)
						return err
					}
				}
				return errors.New("missing assignment")
			})
			after, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			require.Empty(t, after.ActiveRoleIDs)
			require.Greater(t, after.Versions.User, snap.Versions.User)
			require.Greater(t, after.Versions.Policy, snap.Versions.Policy)
			rootToken := f.token(root.ID, "root-rescue-jti", 0, time.Now().Add(time.Hour))
			rootSnap, err := f.uc.GetSessionAuthorization(f.ctx, rootToken, authorization.Platform())
			require.NoError(t, err)
			t.Setenv("ADMIN_TOKEN", "isolated-rescue-token")
			t.Setenv("IAM_RESCUE_ENABLED", "false")
			require.ErrorIs(t, f.uc.RescueRootCredential(f.ctx, "isolated-rescue-token", root.ID, "rescue-password", "lost root", rootSnap.Versions.User, rootSnap.Versions.Policy), biz.ErrIAMProtected)
			t.Setenv("IAM_RESCUE_ENABLED", "true")
			require.ErrorIs(t, f.uc.RescueRootCredential(f.ctx, "forged", root.ID, "rescue-password", "lost root", rootSnap.Versions.User, rootSnap.Versions.Policy), biz.ErrIAMProtected)
			ur, pr := f.versions(user.ID)
			require.ErrorIs(t, f.uc.RescueRootCredential(f.ctx, "isolated-rescue-token", user.ID, "rescue-password", "not root", ur, pr), biz.ErrIAMProtected)
			ur, pr = f.versions(root.ID)
			require.ErrorIs(t, f.uc.RescueRootCredential(f.ctx, "isolated-rescue-token", root.ID, "rescue-password", "stale", ur+1, pr), biz.ErrIAMRevisionConflict)
			require.NoError(t, f.uc.RescueRootCredential(f.ctx, "isolated-rescue-token", root.ID, "rescue-password", "lost root", ur, pr))
			stored, err := f.uc.GetUser(f.ctx, root.ID)
			require.NoError(t, err)
			require.NoError(t, bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("rescue-password")))
			require.Equal(t, root.Role, stored.Role)
			require.Equal(t, root.Email, stored.Email)
			_, err = f.uc.GetSessionAuthorization(f.ctx, rootToken, authorization.Platform())
			require.ErrorIs(t, err, biz.ErrSessionRevoked)
			var events []iamAuditModel
			require.NoError(t, f.db.Find(&events).Error)
			for _, event := range events {
				require.False(t, strings.Contains(fmt.Sprint(event), "rescue-password"))
				require.False(t, strings.Contains(fmt.Sprint(event), "isolated-rescue-token"))
			}
		})
	}
}

// These are owner-verified remote facts, obtained before database callbacks.
type iamA4GroupReader struct{ calls int }

func (r *iamA4GroupReader) FindRoutingGroup(_ context.Context, key string) (*routing.Group, error) {
	r.calls++
	id := int64(71)
	if key == "next" {
		id = 72
	}
	return &routing.Group{ID: id, Key: key, Status: "enabled"}, nil
}

func TestIAMA4RoutingAtomicRuntimeDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			t.Setenv("IDENTITY_ROUTING_V2", "true")
			reader := &iamA4GroupReader{}
			f.uc.SetRoutingGroupReader(reader)
			u := f.register("routing-user")
			require.EqualValues(t, 71, u.DefaultRoutingGroupID)
			require.EqualValues(t, 1, iamCount(t, f.db, "user_routing_group_grants"))
			beforeUser, beforePolicy := f.versions(u.ID)
			beforeAudit := iamCount(t, f.db, "iam_audit_events")
			cleanup := f.failCreate("routing_change_outbox")
			require.Error(t, f.uc.UpdateUser(f.ctx, u.ID, "Label", "", "next", biz.UserStatusEnabled))
			cleanup()
			stored, err := f.uc.GetUser(f.ctx, u.ID)
			require.NoError(t, err)
			require.Equal(t, "default", stored.Group)
			require.EqualValues(t, 71, stored.DefaultRoutingGroupID)
			ur, pr := f.versions(u.ID)
			require.Equal(t, beforeUser, ur)
			require.Equal(t, beforePolicy, pr)
			require.EqualValues(t, 1, iamCount(t, f.db, "user_routing_group_grants"))
			require.Zero(t, iamCount(t, f.db, "routing_change_outbox"))
			require.Equal(t, beforeAudit+1, iamCount(t, f.db, "iam_audit_events"), "only the independent failure audit remains")
			require.NoError(t, f.uc.UpdateUser(f.ctx, u.ID, "Label", "", "next", biz.UserStatusEnabled))
			stored, err = f.uc.GetUser(f.ctx, u.ID)
			require.NoError(t, err)
			require.Equal(t, "next", stored.Group)
			require.EqualValues(t, 72, stored.DefaultRoutingGroupID)
			require.EqualValues(t, 2, stored.RoutingAccessRevision)
			ur, pr = f.versions(u.ID)
			require.Equal(t, beforeUser+1, ur)
			require.Equal(t, beforePolicy+1, pr)
			require.EqualValues(t, 1, iamCount(t, f.db, "routing_change_outbox"))
			_, err = f.uc.UpdateRoutingAccess(f.ctx, biz.RoutingAccessChange{UserID: u.ID, ExpectedRevision: 2, Operation: "public_access", PublicGroupAccess: "all"})
			require.NoError(t, err)
			ur, pr = f.versions(u.ID)
			require.Equal(t, beforeUser+2, ur)
			require.Equal(t, beforePolicy+2, pr)
			f.mode("iam", "complete")
			_, err = f.uc.UpdateRoutingAccess(f.ctx, biz.RoutingAccessChange{UserID: u.ID, ExpectedRevision: 3, Operation: "public_access", PublicGroupAccess: "explicit_only"})
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			_, err = NewRepository(&Data{db: f.db}).BackfillRoutingGroups(f.ctx, []*routing.Group{{ID: 72, Key: "next"}}, true)
			require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
		})
	}
}

func TestIAMA4SnapshotSinglePrimaryViewDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			user := f.register("consistent-user")
			f.mode("iam", "complete")
			raw := f.token(user.ID, "consistent-jti", 0, time.Now().Add(time.Hour))
			before, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			var fired atomic.Bool
			callback := "a4-between-snapshot-reads"
			require.NoError(t, f.db.Callback().Query().After("gorm:query").Register(callback, func(read *gorm.DB) {
				if read.Statement.Table != "users" || !fired.CompareAndSwap(false, true) {
					return
				}
				// A second connection commits changes after the snapshot's user read but
				// before it reads session, policy relations and user revision.
				err := f.db.Transaction(func(write *gorm.DB) error {
					if err := write.Table("users").Where("id = ?", user.ID).Updates(map[string]any{"authorization_revision": gorm.Expr("authorization_revision + 1"), "display_name": "committed-later"}).Error; err != nil {
						return err
					}
					if err := write.Table("iam_policy_state").Where("id = 1").UpdateColumn("policy_revision", gorm.Expr("policy_revision + 1")).Error; err != nil {
						return err
					}
					return write.Table("iam_session_contexts").Where("session_id = ?", before.Session.SessionID).UpdateColumn("revision", gorm.Expr("revision + 1")).Error
				})
				if err != nil {
					read.AddError(err)
				}
			}))
			during, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			require.NoError(t, f.db.Callback().Query().Remove(callback))
			require.True(t, fired.Load())
			require.Equal(t, before.Versions, during.Versions)
			require.Equal(t, before.User.DisplayName, during.User.DisplayName)
			after, err := f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			require.Equal(t, before.Versions.User+1, after.Versions.User)
			require.Equal(t, before.Versions.Policy+1, after.Versions.Policy)
			require.Equal(t, before.Versions.SessionContext+1, after.Versions.SessionContext)
			require.Equal(t, "committed-later", after.User.DisplayName)
		})
	}
}

func TestIAMA4DefaultRoleCapacityAndBootstrapRollbackDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			cleanup := f.failCreate("iam_user_roles")
			result, err := f.uc.EnsureRootAdmin(f.ctx)
			require.Error(t, err)
			require.Nil(t, result)
			cleanup()
			require.Zero(t, iamCount(t, f.db, "users"))
			require.Zero(t, iamCount(t, f.db, "iam_user_roles"))
			require.NoError(t, f.db.Table("iam_roles").Where("code = ?", "member").Update("max_members", 1).Error)
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for i := range 2 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := f.uc.Register(f.ctx, fmt.Sprintf("capacity-%d", i), "password123", "", "default")
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
				} else {
					require.ErrorIs(t, err, biz.ErrIAMConstraintsViolated)
				}
			}
			require.Equal(t, 1, successes)
			require.EqualValues(t, 1, iamCount(t, f.db, "users"))
			require.EqualValues(t, 1, iamCount(t, f.db, "iam_user_roles"))
		})
	}
}
