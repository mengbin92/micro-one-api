package data

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	dbtest "micro-one-api/platform/database/testutil"
)

// The only permissive authorizer is test-only. No runtime A3 constructor binds
// an authorizer; production user/session and delegation checks arrive in A4/A5.
type iamA3TestAuthorizer struct{}

func (iamA3TestAuthorizer) AuthorizeIAMChanges(_ context.Context, _ biz.IAMTx, req biz.IAMConstraintRequest, _ biz.IAMConstraintState) error {
	if req.Actor.ServiceID != "isolated-a3-test" {
		return biz.ErrIAMProtected
	}
	return nil
}

type iamA3Fixture struct {
	t      *testing.T
	db     *gorm.DB
	repo   biz.IAMConstraintRepo
	runner biz.IAMTxRunner
	uc     *biz.IAMConstraintsUsecase
	ctx    context.Context
	seq    int
}

func iamA3Int(n int64) *int64 { return &n }
func newIAMA3Fixture(t *testing.T, driver string) *iamA3Fixture {
	t.Helper()
	db := dbtest.RoutingContextDB(t, driver)
	if driver == "sqlite" {
		require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	}
	d := &Data{db: db}
	repo := NewIAMConstraintRepo(d)
	runner := NewIAMTxRunner(d)
	// Test-only cutover on an isolated schema. No application API exposes this.
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "a3-isolated", "cutover_verified_at": time.Now().UTC().UnixMilli()}).Error)
	return &iamA3Fixture{t: t, db: db, repo: repo, runner: runner, uc: biz.NewIAMConstraintsUsecase(repo, runner, iamA3TestAuthorizer{}), ctx: context.Background()}
}
func (f *iamA3Fixture) role(code string, max *int64) int64 {
	f.t.Helper()
	var role biz.IAMRole
	f.seq++
	require.NoError(f.t, f.runner.RunIAMWrite(f.ctx, func(ctx context.Context, tx biz.IAMTx) error {
		var err error
		role, err = f.repo.SaveRole(ctx, tx, biz.IAMRole{Context: authorization.Platform(), Code: code, Name: code, Status: "enabled", MaxMembers: max}, 0)
		if err != nil {
			return err
		}
		return f.repo.AppendAudit(ctx, tx, iamTestEvent(fmt.Sprintf("setup-role-%d", f.seq)))
	}))
	return role.ID
}
func (f *iamA3Fixture) state() (biz.IAMConstraintState, uint64) {
	f.t.Helper()
	var state biz.IAMConstraintState
	var revision uint64
	require.NoError(f.t, f.runner.ReadIAMSnapshot(f.ctx, func(ctx context.Context, tx biz.IAMTx) error {
		p, err := f.repo.Policy(ctx, tx)
		if err != nil {
			return err
		}
		revision = p.PolicyRevision
		state, err = f.repo.ConstraintState(ctx, tx, authorization.Platform())
		return err
	}))
	return state, revision
}
func (f *iamA3Fixture) request(changes ...biz.IAMConstraintChange) biz.IAMConstraintRequest {
	f.t.Helper()
	_, rev := f.state()
	f.seq++
	id := fmt.Sprintf("request-%d", f.seq)
	return biz.IAMConstraintRequest{Context: authorization.Platform(), Actor: authorization.Actor{ServiceID: "isolated-a3-test"}, ExpectedPolicyRevision: rev, Changes: changes, EventID: id, RequestID: id, Reason: "isolated A3 acceptance"}
}
func (f *iamA3Fixture) apply(changes ...biz.IAMConstraintChange) {
	f.t.Helper()
	_, err := f.uc.Apply(f.ctx, f.request(changes...))
	require.NoError(f.t, err)
}
func (f *iamA3Fixture) assignment(user, role int64, start time.Time, end *time.Time) biz.IAMAssignment {
	a := iamTestAssignment(user, role)
	a.Origin = "explicit"
	a.Validity = authorization.Interval{StartsAt: start, ExpiresAt: end}
	return a
}
func (f *iamA3Fixture) activeSession(id string, user int64, roles ...int64) {
	f.t.Helper()
	require.NoError(f.t, f.db.Table("iam_sessions").Create(map[string]any{"session_id": id, "user_id": user, "expires_at": time.Now().Add(3 * time.Hour).UnixMilli()}).Error)
	require.NoError(f.t, f.db.Table("iam_session_contexts").Create(map[string]any{"session_id": id, "context_key": "platform", "activation_state": "active"}).Error)
	for _, role := range roles {
		require.NoError(f.t, f.db.Create(&iamSessionRoleModel{SessionID: id, ContextKey: "platform", RoleID: role}).Error)
	}
}
func iamA3Role(state biz.IAMConstraintState, id int64) biz.IAMRole {
	return state.Roles[slices.IndexFunc(state.Roles, func(r biz.IAMRole) bool { return r.ID == id })]
}
func iamA3Topology(r biz.IAMRole) biz.IAMRoleTopology {
	return biz.IAMRoleTopology{ID: r.ID, Status: r.Status, MaxMembers: r.MaxMembers, Inherits: slices.Clone(r.Inherits)}
}
func iamA3Violation(t *testing.T, err error, kind string) *biz.IAMConstraintViolation {
	t.Helper()
	var violation *biz.IAMConstraintViolation
	require.ErrorAs(t, err, &violation)
	require.ErrorIs(t, err, biz.ErrIAMConstraintsViolated)
	require.NotEmpty(t, violation.Conflicts)
	found := false
	for _, c := range violation.Conflicts {
		if c.Kind == kind {
			found = true
		}
	}
	require.True(t, found, "expected %s in %+v", kind, violation.Conflicts)
	return violation
}

func TestIAMConstraintsDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Run("future_reservation_adjacency_extend_restore", func(t *testing.T) {
				f := newIAMA3Fixture(t, driver)
				role := f.role("future", iamA3Int(1))
				u1 := iamSeedUser(t, f.db, "one")
				u2 := iamSeedUser(t, f.db, "two")
				start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
				end := start.Add(time.Hour)
				a := f.assignment(u1, role, start, &end)
				f.apply(biz.IAMConstraintChange{Assignment: &a})
				b := f.assignment(u2, role, start, &end)
				req := f.request(biz.IAMConstraintChange{Assignment: &b})
				preview, err := f.uc.Preview(f.ctx, req)
				require.NoError(t, err)
				require.Len(t, preview.Conflicts, 1)
				require.Equal(t, start, preview.Conflicts[0].Validity.StartsAt)
				before, rev := f.state()
				_, err = f.uc.Apply(f.ctx, req)
				v := iamA3Violation(t, err, "MAX_MEMBERS")
				require.Equal(t, preview.Conflicts, v.Conflicts)
				after, newRev := f.state()
				require.Equal(t, before, after)
				require.Equal(t, rev, newRev)
				var failure int64
				require.NoError(t, f.db.Table("iam_audit_events").Where("event_id = ? AND result = 'failure'", req.EventID).Count(&failure).Error)
				require.Equal(t, int64(1), failure)
				b.Validity = authorization.Interval{StartsAt: end}
				f.apply(biz.IAMConstraintChange{Assignment: &b})
				state, _ := f.state()
				a = state.Assignments[0]
				extension := end.Add(time.Hour)
				a.Validity.ExpiresAt = &extension
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Assignment: &a, ExpectedRevision: a.Revision}))
				iamA3Violation(t, err, "MAX_MEMBERS")
				a.Revoked = true
				f.apply(biz.IAMConstraintChange{Assignment: &a, ExpectedRevision: a.Revision})
				state, _ = f.state()
				a = state.Assignments[0]
				a.Revoked = false
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Assignment: &a, ExpectedRevision: a.Revision}))
				iamA3Violation(t, err, "MAX_MEMBERS")
			})
			t.Run("concurrent_capacity_and_preview_revision", func(t *testing.T) {
				f := newIAMA3Fixture(t, driver)
				role := f.role("reserved", iamA3Int(1))
				u1 := iamSeedUser(t, f.db, "one")
				u2 := iamSeedUser(t, f.db, "two")
				start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
				end := start.Add(time.Hour)
				a, b := f.assignment(u1, role, start, &end), f.assignment(u2, role, start, &end)
				requests := []biz.IAMConstraintRequest{f.request(biz.IAMConstraintChange{Assignment: &a}), f.request(biz.IAMConstraintChange{Assignment: &b})}
				type result struct {
					i   int
					err error
				}
				done := make(chan result, 2)
				for i, req := range requests {
					go func(i int, req biz.IAMConstraintRequest) { _, err := f.uc.Apply(f.ctx, req); done <- result{i, err} }(i, req)
				}
				success, loser := 0, -1
				for i := 0; i < 2; i++ {
					r := <-done
					if r.err == nil {
						success++
					} else {
						require.ErrorIs(t, r.err, biz.ErrIAMRevisionConflict)
						loser = r.i
					}
				}
				require.Equal(t, 1, success)
				require.GreaterOrEqual(t, loser, 0)
				require.Equal(t, int64(1), iamCount(t, f.db, "iam_user_roles"))
				_, err := f.uc.Preview(f.ctx, requests[loser])
				require.ErrorIs(t, err, biz.ErrIAMRevisionConflict)
				_, revision := f.state()
				requests[loser].ExpectedPolicyRevision = revision
				requests[loser].EventID = "retry-loser"
				requests[loser].RequestID = "retry-loser"
				_, err = f.uc.Apply(f.ctx, requests[loser])
				iamA3Violation(t, err, "MAX_MEMBERS")
			})
			t.Run("graph_dag_propagation_and_future_members", func(t *testing.T) {
				f := newIAMA3Fixture(t, driver)
				parent := f.role("parent", nil)
				child := f.role("child", iamA3Int(1))
				u1 := iamSeedUser(t, f.db, "one")
				u2 := iamSeedUser(t, f.db, "two")
				start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
				a, b := f.assignment(u1, parent, start, nil), f.assignment(u2, child, start, nil)
				f.apply(biz.IAMConstraintChange{Assignment: &a}, biz.IAMConstraintChange{Assignment: &b})
				before, _ := f.state()
				r := iamA3Role(before, parent)
				topology := iamA3Topology(r)
				topology.Inherits = []int64{child}
				_, err := f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Role: &topology, ExpectedRevision: r.Revision}))
				iamA3Violation(t, err, "MAX_MEMBERS")
				after, _ := f.state()
				require.Equal(t, before, after)
				// Remove the second source, then accept the graph and reject a reverse edge.
				b = after.Assignments[1]
				b.Revoked = true
				f.apply(biz.IAMConstraintChange{Assignment: &b, ExpectedRevision: b.Revision})
				f.apply(biz.IAMConstraintChange{Role: &topology, ExpectedRevision: r.Revision})
				state, _ := f.state()
				junior := iamA3Role(state, child)
				reverse := iamA3Topology(junior)
				reverse.Inherits = []int64{parent}
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Role: &reverse, ExpectedRevision: junior.Revision}))
				require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
				state, _ = f.state()
				require.Empty(t, iamA3Role(state, child).Inherits)
			})
			t.Run("constraint_enable_batch_revoke_and_delete", func(t *testing.T) {
				f := newIAMA3Fixture(t, driver)
				alpha := f.role("alpha", nil)
				beta := f.role("beta", nil)
				u := iamSeedUser(t, f.db, "one")
				now := time.Now().UTC().Add(-time.Minute)
				a, b := f.assignment(u, alpha, now, nil), f.assignment(u, beta, now, nil)
				f.apply(biz.IAMConstraintChange{Assignment: &a}, biz.IAMConstraintChange{Assignment: &b})
				rule := biz.IAMRoleConstraint{Context: authorization.Platform(), Kind: "SSD", Name: "duties", MaxCount: 1, RoleIDs: []int64{alpha, beta}, Enabled: true}
				_, err := f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Constraint: &rule}))
				iamA3Violation(t, err, "SSD")
				require.Zero(t, iamCount(t, f.db, "iam_role_constraints"))
				rule.Enabled = false
				f.apply(biz.IAMConstraintChange{Constraint: &rule})
				state, _ := f.state()
				stored := state.Constraints[0]
				stored.Enabled = true
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Constraint: &stored, ExpectedRevision: stored.Revision}))
				iamA3Violation(t, err, "SSD")
				state, _ = f.state()
				b = state.Assignments[1]
				b.Revoked = true
				// Final-state preflight is independent of the order of these changes.
				f.apply(biz.IAMConstraintChange{Constraint: &stored, ExpectedRevision: stored.Revision}, biz.IAMConstraintChange{Assignment: &b, ExpectedRevision: b.Revision})
				state, _ = f.state()
				require.True(t, state.Constraints[0].Enabled)
				require.True(t, state.Assignments[1].Revoked)
				id := state.Constraints[0].ID
				f.apply(biz.IAMConstraintChange{DeleteConstraint: &id, ExpectedRevision: state.Constraints[0].Revision})
				require.Zero(t, iamCount(t, f.db, "iam_role_constraint_members"))
				require.Zero(t, iamCount(t, f.db, "iam_role_constraints"))
			})
			t.Run("session_dsd_graph_enable_and_activation_limits", func(t *testing.T) {
				f := newIAMA3Fixture(t, driver)
				alpha := f.role("alpha", nil)
				beta := f.role("beta", nil)
				parent := f.role("parent", nil)
				u := iamSeedUser(t, f.db, "one")
				now := time.Now().UTC().Add(-time.Minute)
				state, _ := f.state()
				p := iamA3Role(state, parent)
				graph := iamA3Topology(p)
				graph.Inherits = []int64{alpha}
				f.apply(biz.IAMConstraintChange{Role: &graph, ExpectedRevision: p.Revision})
				a, b := f.assignment(u, parent, now, nil), f.assignment(u, beta, now, nil)
				f.apply(biz.IAMConstraintChange{Assignment: &a}, biz.IAMConstraintChange{Assignment: &b})
				f.activeSession("real-jti", u, parent)
				rule := biz.IAMRoleConstraint{Context: authorization.Platform(), Kind: "DSD", Name: "session duties", MaxCount: 1, RoleIDs: []int64{alpha, beta}, Enabled: true}
				f.apply(biz.IAMConstraintChange{Constraint: &rule})
				state, _ = f.state()
				p = iamA3Role(state, parent)
				graph = iamA3Topology(p)
				graph.Inherits = []int64{alpha, beta}
				_, err := f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Role: &graph, ExpectedRevision: p.Revision}))
				v := iamA3Violation(t, err, "DSD")
				require.Equal(t, []string{"real-jti"}, v.Conflicts[0].SessionIDs)
				activation := biz.IAMActivationChange{SessionID: "real-jti", RoleIDs: []int64{parent, beta}}
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Activation: &activation, ExpectedRevision: 1}))
				iamA3Violation(t, err, "DSD")
				state, _ = f.state()
				require.Equal(t, uint64(1), state.Sessions[0].Revision)
				require.Equal(t, []int64{parent}, state.Sessions[0].ActiveRoleIDs)
				// Caps also include inherited roles, and nil restores unlimited capacity.
				limits := biz.IAMLimits{MaxRolesPerUser: iamA3Int(2), MaxRolesPerSession: iamA3Int(1)}
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Limits: &limits}))
				iamA3Violation(t, err, "MAX_ROLES_PER_USER")
				iamA3Violation(t, err, "MAX_ROLES_PER_SESSION")
				limits = biz.IAMLimits{MaxRolesPerUser: iamA3Int(3), MaxRolesPerSession: iamA3Int(2)}
				f.apply(biz.IAMConstraintChange{Limits: &limits})
				activation.RoleIDs = []int64{beta}
				f.apply(biz.IAMConstraintChange{Activation: &activation, ExpectedRevision: 1})
				state, _ = f.state()
				require.Equal(t, uint64(2), state.Sessions[0].Revision)
				require.Equal(t, []int64{beta}, state.Sessions[0].ActiveRoleIDs)
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Activation: &activation, ExpectedRevision: 1}))
				require.ErrorIs(t, err, biz.ErrIAMRevisionConflict)
				activation.RoleIDs = []int64{999}
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Activation: &activation, ExpectedRevision: 2}))
				require.ErrorIs(t, err, biz.ErrIAMProtected)
				// Re-enabling a previously active beta restores its DSD contribution.
				state, _ = f.state()
				r := iamA3Role(state, beta)
				disabled := iamA3Topology(r)
				disabled.Status = "disabled"
				f.apply(biz.IAMConstraintChange{Role: &disabled, ExpectedRevision: r.Revision})
				require.NoError(t, f.db.Create(&iamSessionRoleModel{SessionID: "real-jti", ContextKey: "platform", RoleID: parent}).Error)
				state, _ = f.state()
				r = iamA3Role(state, beta)
				enabled := iamA3Topology(r)
				enabled.Status = "enabled"
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Role: &enabled, ExpectedRevision: r.Revision}))
				iamA3Violation(t, err, "DSD")
			})
			t.Run("atomic_storage_failure_audit_and_mode_gate", func(t *testing.T) {
				f := newIAMA3Fixture(t, driver)
				id := f.role("atomic", nil)
				state, revision := f.state()
				r := iamA3Role(state, id)
				topology := iamA3Topology(r)
				topology.Status = "disabled"
				injected := errors.New("injected second write failure")
				repo := iamA3FailRepo{IAMConstraintRepo: f.repo, err: injected}
				uc := biz.NewIAMConstraintsUsecase(repo, f.runner, iamA3TestAuthorizer{})
				req := f.request(biz.IAMConstraintChange{Role: &topology, ExpectedRevision: r.Revision}, biz.IAMConstraintChange{Limits: &biz.IAMLimits{MaxRolesPerUser: iamA3Int(1)}})
				_, err := uc.Apply(f.ctx, req)
				require.ErrorIs(t, err, injected)
				after, newRev := f.state()
				require.Equal(t, state, after)
				require.Equal(t, revision, newRev)
				var failures int64
				require.NoError(t, f.db.Table("iam_audit_events").Where("event_id = ? AND result = 'failure'", req.EventID).Count(&failures).Error)
				require.Equal(t, int64(1), failures)
				// Duplicate success event ID rolls back role and policy revisions too.
				req = f.request(biz.IAMConstraintChange{Role: &topology, ExpectedRevision: r.Revision})
				req.EventID = "setup-role-1"
				_, err = f.uc.Apply(f.ctx, req)
				require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
				after, newRev = f.state()
				require.Equal(t, state, after)
				require.Equal(t, revision, newRev)
				denied := biz.NewIAMConstraintsUsecase(f.repo, f.runner, nil)
				_, err = denied.Apply(f.ctx, f.request(biz.IAMConstraintChange{Role: &topology, ExpectedRevision: r.Revision}))
				require.ErrorIs(t, err, biz.ErrIAMProtected)
				require.NoError(t, f.db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "legacy", "cutover_state": "idle", "cutover_batch_id": "", "cutover_verified_at": nil}).Error)
				_, err = f.uc.Apply(f.ctx, f.request(biz.IAMConstraintChange{Role: &topology, ExpectedRevision: r.Revision}))
				require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
				// DDL mirrors must reject non-positive caps in every driver.
				require.Error(t, f.db.Table("iam_policy_state").Where("id = 1").Update("max_roles_per_user", 0).Error)
				require.Error(t, f.db.Table("iam_policy_state").Where("id = 1").Update("max_roles_per_session", -1).Error)
			})
		})
	}
}

type iamA3FailRepo struct {
	biz.IAMConstraintRepo
	err error
}

func (r iamA3FailRepo) SaveLimits(context.Context, biz.IAMTx, biz.IAMLimits) error { return r.err }
