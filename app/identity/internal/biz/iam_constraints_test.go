package biz

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
)

func iamLimit(n int64) *int64 { return &n }
func iamConstraintFixture() (time.Time, IAMConstraintState) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	c := authorization.Platform()
	return now, IAMConstraintState{Context: c, Roles: []IAMRole{
		{ID: 1, Context: c, Code: "a", Status: "enabled", Revision: 1, Inherits: []int64{2, 3}},
		{ID: 2, Context: c, Code: "b", Status: "enabled", Revision: 1, Inherits: []int64{4}},
		{ID: 3, Context: c, Code: "c", Status: "enabled", Revision: 1, Inherits: []int64{4}},
		{ID: 4, Context: c, Code: "d", Status: "enabled", Revision: 1},
	}}
}
func iamAssignmentFixture(id, user, role int64, start time.Time, end *time.Time) IAMAssignment {
	return IAMAssignment{ID: id, UserID: user, RoleID: role, Context: authorization.Platform(), Validity: authorization.Interval{StartsAt: start, ExpiresAt: end}, Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Origin: "explicit", Revision: 1}
}
func iamSessionFixture(id string, user int64, end time.Time, active ...int64) IAMSessionContext {
	return IAMSessionContext{SessionID: id, UserID: user, Context: authorization.Platform(), ExpiresAt: end, ActivationState: "active", ActiveRoleIDs: active, Revision: 1, SessionRevision: 1}
}
func iamKinds(conflicts []IAMConstraintConflict) []string {
	out := []string{}
	for _, c := range conflicts {
		out = append(out, c.Kind)
	}
	return out
}

func TestIAMConstraintDAGAndDedup(t *testing.T) {
	now, s := iamConstraintFixture()
	closures, err := IAMRoleClosures(s.Context, s.Roles)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3, 4}, closures[1])
	s.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 1, now, nil), iamAssignmentFixture(2, 7, 4, now, nil)}
	s.Roles[3].MaxMembers = iamLimit(1)
	s.Limits = IAMLimits{MaxRolesPerUser: iamLimit(4), MaxRolesPerSession: iamLimit(3)}
	s.Sessions = []IAMSessionContext{iamSessionFixture("s", 7, now.Add(time.Hour), 2, 3)}
	conflicts, err := IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	s.Roles[1].Status = "disabled"
	closures, err = IAMRoleClosures(s.Context, s.Roles)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3, 4}, closures[1])
	s.Roles[2].Status = "draft"
	closures, err = IAMRoleClosures(s.Context, s.Roles)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, closures[1])
	// Graph integrity is checked even for disabled or disconnected nodes.
	for _, tt := range []struct {
		name  string
		child []int64
	}{{"self", []int64{4}}, {"cycle", []int64{1}}, {"unknown", []int64{99}}, {"duplicate", []int64{2, 2}}} {
		t.Run(tt.name, func(t *testing.T) {
			_, copy := iamConstraintFixture()
			copy.Roles[3].Status = "disabled"
			copy.Roles[3].Inherits = tt.child
			_, err := IAMRoleClosures(copy.Context, copy.Roles)
			require.ErrorIs(t, err, ErrIAMInvalidRelation)
		})
	}
	other := s
	other.Roles = slices.Clone(s.Roles)
	other.Roles[1].Context = authorization.Context{Type: "organization", OrganizationID: 1, Key: "organization:1"}
	_, err = IAMCheckConstraints(now, other)
	require.Error(t, err)
	other.Context = other.Roles[1].Context
	_, err = IAMCheckConstraints(now, other)
	require.ErrorIs(t, err, ErrIAMContextInvalid)
}
func TestIAMFutureIntervalsReserveAllConstraints(t *testing.T) {
	now, s := iamConstraintFixture()
	start, end := now.Add(24*time.Hour), now.Add(25*time.Hour)
	s.Roles[3].MaxMembers = iamLimit(1)
	s.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 2, start, &end), iamAssignmentFixture(2, 8, 3, start, &end)}
	conflicts, err := IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Equal(t, "MAX_MEMBERS", conflicts[0].Kind)
	require.Equal(t, []int64{7, 8}, conflicts[0].UserIDs)
	require.Equal(t, start, conflicts[0].Validity.StartsAt)
	require.Equal(t, &end, conflicts[0].Validity.ExpiresAt)
	s.Assignments[1].Validity = authorization.Interval{StartsAt: end}
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	// Renewing/restoring an earlier relation must reserve the whole future window.
	later := end.Add(time.Hour)
	s.Assignments[0].Validity.ExpiresAt = &later
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Equal(t, end, conflicts[0].Validity.StartsAt)
	s.Assignments[1].Revoked = true
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	s.Assignments[1].Revoked = false
	s.Assignments[0].Validity.ExpiresAt = nil
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Nil(t, conflicts[0].Validity.ExpiresAt)
	// Advancing the clock requires no expiry cleanup job and drops past windows.
	conflicts, err = IAMCheckConstraints(later, s)
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Equal(t, later, conflicts[0].Validity.StartsAt)
}
func TestIAMSSDDSDAndActivationClosure(t *testing.T) {
	now, s := iamConstraintFixture()
	end := now.Add(2 * time.Hour)
	s.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 1, now, &end)}
	s.Constraints = []IAMRoleConstraint{
		{ID: 1, Context: s.Context, Kind: "SSD", RoleIDs: []int64{2, 4}, MaxCount: 1, Enabled: true},
		{ID: 2, Context: s.Context, Kind: "DSD", RoleIDs: []int64{2, 3}, MaxCount: 1, Enabled: true},
	}
	// SSD sees all assigned roles even with no activated session or an empty
	// permission boundary; deny scopes and activation cannot conceal assignment.
	s.Assignments[0].Boundary = authorization.Scope{Clauses: []authorization.Clause{}}
	conflicts, err := IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Equal(t, []string{"SSD"}, iamKinds(conflicts))
	s.Constraints[0].Enabled = false
	s.Sessions = []IAMSessionContext{iamSessionFixture("junior", 7, end, 2)}
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts) // junior does not activate senior/sibling
	s.Sessions[0].ActiveRoleIDs = []int64{1}
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Equal(t, []string{"DSD"}, iamKinds(conflicts))
	require.Equal(t, []string{"junior"}, conflicts[0].SessionIDs)
	s.Sessions[0].ActiveRoleIDs = []int64{2}
	s.Sessions = append(s.Sessions, iamSessionFixture("other", 7, end, 3))
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts) // DSD is per session
	s.Sessions[0].ActiveRoleIDs = []int64{1}
	s.Sessions[0].RevokedAt = &now
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	s.Sessions[0].RevokedAt = nil
	s.Sessions[0].ContextRevokedAt = &now
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	s.Sessions[0].ContextRevokedAt = nil
	s.Sessions[0].ExpiresAt = now
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
}
func TestIAMGraphEnableAndFutureSessionWindows(t *testing.T) {
	now, s := iamConstraintFixture()
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	s.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 1, start, &end)}
	s.Sessions = []IAMSessionContext{iamSessionFixture("future-source", 7, now.Add(3*time.Hour), 1)}
	s.Constraints = []IAMRoleConstraint{{ID: 1, Context: s.Context, Kind: "DSD", RoleIDs: []int64{2, 3}, MaxCount: 1, Enabled: true}}
	s.Limits = IAMLimits{MaxRolesPerUser: iamLimit(3), MaxRolesPerSession: iamLimit(3)}
	s.Roles[2].Status = "disabled"
	conflicts, err := IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	s.Roles[2].Status = "enabled"
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Equal(t, []string{"MAX_ROLES_PER_USER", "MAX_ROLES_PER_SESSION", "DSD"}, iamKinds(conflicts))
	for _, c := range conflicts {
		require.Equal(t, start, c.Validity.StartsAt)
		require.Equal(t, &end, c.Validity.ExpiresAt)
	}
	// A session expiring exactly at the next assignment's start is not active.
	s.Sessions[0].ExpiresAt = start
	conflicts, err = IAMCheckConstraints(now, s)
	require.NoError(t, err)
	require.Equal(t, []string{"MAX_ROLES_PER_USER"}, iamKinds(conflicts))
}
func TestIAMInvalidConstraintInputs(t *testing.T) {
	now, s := iamConstraintFixture()
	bad := []IAMConstraintState{}
	one := s
	one.Limits.MaxRolesPerUser = iamLimit(0)
	bad = append(bad, one)
	one = s
	one.Constraints = []IAMRoleConstraint{{ID: 1, Context: s.Context, Kind: "OTHER", MaxCount: 1, RoleIDs: []int64{2}}}
	bad = append(bad, one)
	one = s
	one.Constraints = []IAMRoleConstraint{{ID: 1, Context: s.Context, Kind: "SSD", MaxCount: 1, RoleIDs: []int64{2, 2}}}
	bad = append(bad, one)
	one = s
	one.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 2, now, &now)}
	bad = append(bad, one)
	one = s
	one.Sessions = []IAMSessionContext{iamSessionFixture("s", 7, now.Add(time.Hour), 99)}
	bad = append(bad, one)
	for _, state := range bad {
		_, err := IAMCheckConstraints(now, state)
		require.Error(t, err)
	}
}

// The biz seam deliberately fakes storage and transaction behavior. Real lock,
// CAS, audit and database rollback coverage lives in data/iam_constraints_test.go.
type constraintFake struct {
	IAMConstraintRepo
	policy authorization.PolicyState
	state  IAMConstraintState
	audit  []IAMAuditEvent
	writes int
}
type constraintTestTx struct{}

func (constraintTestTx) Handle() any { return nil }
func (f *constraintFake) Policy(context.Context, IAMTx) (authorization.PolicyState, error) {
	return f.policy, nil
}
func (f *constraintFake) ConstraintState(context.Context, IAMTx, authorization.Context) (IAMConstraintState, error) {
	return f.state, nil
}
func (f *constraintFake) SaveLimits(_ context.Context, _ IAMTx, limits IAMLimits) error {
	f.state.Limits = limits
	f.writes++
	return nil
}
func (f *constraintFake) AdvancePolicy(_ context.Context, _ IAMTx, expected uint64, _ bool) error {
	if expected != f.policy.PolicyRevision {
		return ErrIAMRevisionConflict
	}
	f.policy.PolicyRevision++
	return nil
}
func (f *constraintFake) AppendFailureAudit(_ context.Context, e IAMAuditEvent) error {
	f.audit = append(f.audit, e)
	return nil
}
func (f *constraintFake) AppendAudit(_ context.Context, _ IAMTx, e IAMAuditEvent) error {
	f.audit = append(f.audit, e)
	return nil
}
func (f *constraintFake) RunIAMWrite(ctx context.Context, fn func(context.Context, IAMTx) error) error {
	return fn(ctx, constraintTestTx{})
}
func (f *constraintFake) ReadIAMSnapshot(ctx context.Context, fn func(context.Context, IAMTx) error) error {
	return fn(ctx, constraintTestTx{})
}

type constraintTestAuthorizer struct {
	err   error
	calls int
}

func (a *constraintTestAuthorizer) AuthorizeIAMChanges(context.Context, IAMTx, IAMConstraintRequest, IAMConstraintState) error {
	a.calls++
	return a.err
}
func TestIAMConstraintUsecaseGatesAndPreview(t *testing.T) {
	now, s := iamConstraintFixture()
	s.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 1, now, nil)}
	f := &constraintFake{state: s, policy: authorization.PolicyState{Mode: "iam", Cutover: "complete", BatchID: "isolated", VerifiedAt: &now, PolicyRevision: 1, CatalogRevision: 1}}
	authorizer := &constraintTestAuthorizer{}
	uc := NewIAMConstraintsUsecase(f, f, authorizer)
	uc.now = func() time.Time { return now }
	req := IAMConstraintRequest{Context: s.Context, ExpectedPolicyRevision: 1, EventID: "a3", RequestID: "a3", Reason: "test", Changes: []IAMConstraintChange{{Limits: &IAMLimits{MaxRolesPerUser: iamLimit(1)}}}}
	preview, err := uc.Preview(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, preview.Conflicts, 1)
	require.Zero(t, f.writes)
	require.Nil(t, s.Limits.MaxRolesPerUser)
	_, err = uc.Apply(context.Background(), req)
	var violation *IAMConstraintViolation
	require.ErrorAs(t, err, &violation)
	require.ErrorIs(t, err, ErrIAMConstraintsViolated)
	require.Equal(t, preview.Conflicts, violation.Conflicts)
	require.Zero(t, f.writes)
	req.Changes[0].Limits.MaxRolesPerUser = iamLimit(4)
	result, err := uc.Apply(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.PolicyRevision)
	require.Equal(t, 1, f.writes)
	require.Len(t, f.audit, 2)
	require.Equal(t, uint64(1), f.audit[1].Versions.Policy)
	_, err = uc.Apply(context.Background(), req)
	require.ErrorIs(t, err, ErrIAMRevisionConflict)
	req.ExpectedPolicyRevision = 2
	authorizer.err = ErrIAMProtected
	_, err = uc.Preview(context.Background(), req)
	require.ErrorIs(t, err, ErrIAMProtected)
	uc.authorizer = nil
	_, err = uc.Apply(context.Background(), req)
	require.ErrorIs(t, err, ErrIAMProtected)
	f.policy = authorization.PolicyState{Mode: "legacy", Cutover: "idle", PolicyRevision: 2, CatalogRevision: 1}
	_, err = uc.Apply(context.Background(), req)
	require.ErrorIs(t, err, ErrIAMCutoverBlocked)
}
func TestIAMBatchProposalImmutabilityAndActivationOwnership(t *testing.T) {
	now, s := iamConstraintFixture()
	s.Assignments = []IAMAssignment{iamAssignmentFixture(1, 7, 1, now, nil)}
	s.Sessions = []IAMSessionContext{iamSessionFixture("s", 7, now.Add(time.Hour), 2)}
	ch := []IAMConstraintChange{{ExpectedRevision: 1, Role: &IAMRoleTopology{ID: 1, Status: "disabled", Inherits: []int64{2, 3}}}, {ExpectedRevision: 1, Activation: &IAMActivationChange{SessionID: "s", RoleIDs: []int64{2}}}}
	_, _, err := iamProposeChanges(s, ch, now)
	require.ErrorIs(t, err, ErrIAMProtected)
	require.Equal(t, "enabled", s.Roles[0].Status)
	require.Equal(t, []int64{2}, s.Sessions[0].ActiveRoleIDs)
	s.Assignments[0].Validity.StartsAt = now.Add(time.Minute)
	_, _, err = iamProposeChanges(s, ch[1:], now)
	require.ErrorIs(t, err, ErrIAMProtected)
	ch = []IAMConstraintChange{{Role: &IAMRoleTopology{}, Limits: &IAMLimits{}}}
	_, _, err = iamProposeChanges(s, ch, now)
	require.ErrorIs(t, err, ErrIAMInvalidRelation)
	require.True(t, errors.Is(&IAMConstraintViolation{}, ErrIAMConstraintsViolated))
}
