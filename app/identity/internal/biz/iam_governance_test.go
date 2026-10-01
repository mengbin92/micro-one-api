package biz

import (
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"testing"
	"time"
)

func TestCredentialTakeoverIncludesFutureAuthorityAndMaximumAllow(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	end := future.Add(time.Hour)
	c := authorization.Platform()
	all := authorization.Scope{Clauses: []authorization.Clause{{All: true}}}
	state := IAMConstraintState{Context: c, Roles: []IAMRole{{ID: 1, Context: c, Code: "target", Status: "enabled", Grants: []IAMGrant{{Operation: "billing.payment.read", Effect: authorization.Allow, Scope: all}, {Operation: "billing.payment.read", Effect: authorization.Deny, Scope: all}}}, {ID: 2, Context: c, Code: "authority", Status: "disabled"}}, Assignments: []IAMAssignment{{ID: 1, UserID: 20, RoleID: 1, Context: c, Validity: authorization.Interval{StartsAt: future, ExpiresAt: &end}, Boundary: all}}}
	takeover := m.Delegation{Context: c, TargetKind: "user_credentials", TargetUserScope: all, Validity: authorization.Interval{StartsAt: now.Add(-time.Hour)}, GrantCeiling: []m.Ceiling{{Context: c, Operation: "billing.payment.read", Scope: all}}}
	require.NoError(t, IAMCheckCredentialTakeover(now, state, nil, 10, 20, takeover))
	narrow := takeover
	narrow.GrantCeiling = nil
	require.ErrorIs(t, IAMCheckCredentialTakeover(now, state, nil, 10, 20, narrow), ErrIAMProtected)
	state.Roles[0].Inherits = []int64{2}
	authority := m.Delegation{ManagerRoleID: 2, TargetKind: "role_creation", Actions: []string{"iam.role.create"}, Validity: authorization.Interval{StartsAt: future, ExpiresAt: &end}}
	require.ErrorIs(t, IAMCheckCredentialTakeover(now, state, []m.Delegation{authority}, 10, 20, takeover), ErrIAMProtected)
	authority.Validity.StartsAt = end
	authority.Validity.ExpiresAt = nil
	require.NoError(t, IAMCheckCredentialTakeover(now, state, []m.Delegation{authority}, 10, 20, takeover))
	require.ErrorIs(t, IAMCheckCredentialTakeover(now, state, nil, 20, 20, takeover), ErrIAMProtected)
}
func TestManagementExecutionBindingIsExplicit(t *testing.T) {
	require.True(t, IAMExecutionBound("iam.role.permissions.update"))
	require.False(t, IAMExecutionBound("iam.menu.read"))
	require.True(t, IAMExecutionBound("channel.channel.read"), "B2 binds the channel read execution point")
	require.False(t, IAMExecutionBound("channel.account.oauth.bind"), "planned operations remain unbound until their owner handlers are delivered")
	require.False(t, IAMExecutionBound("billing.report.export"), "planned operations remain unbound until their owner handlers are delivered")
	require.True(t, IAMExecutionBound("log.request.content.read"))
	require.True(t, IAMExecutionBound("monitor.alert_rule.create"))
	require.False(t, IAMExecutionBound("notify.notification.acknowledge"), "planned operations remain unbound until their owner handlers are delivered")
	require.False(t, IAMExecutionBound("organization.member.invite"), "organization operations stay unbound")
	require.True(t, IAMExecutionBound("identity.user.credential.update"))
	require.True(t, IAMExecutionBound("identity.user.list"))
	require.True(t, IAMExecutionBound("identity.user.delete"))
}

func governanceTestView(t *testing.T) iamManagementView {
	t.Helper()
	c := authorization.Platform()
	now := time.Now().UTC().Truncate(time.Millisecond)
	all := iamAllScope
	v := iamManagementView{actor: authorization.Actor{UserID: 10, SessionID: "manager", ExpiresAt: now.Add(time.Hour)}, active: []int64{1}, future: []int64{1}, now: now, session: IAMSessionContext{ActivationState: "active"}, state: IAMConstraintState{Context: c, Roles: []IAMRole{
		{ID: 1, Context: c, Code: "manager", Status: "enabled"},
		{ID: 2, Context: c, Code: "child", Status: "enabled", Grants: []IAMGrant{{Operation: "channel.channel.read", Effect: authorization.Allow, Scope: all}}},
		{ID: 3, Context: c, Code: "senior", Status: "enabled", Inherits: []int64{2}},
		{ID: 4, Context: c, Code: "unrelated-finance", Status: "enabled", Grants: []IAMGrant{{Operation: "billing.payment.read", Effect: authorization.Allow, Scope: all}}},
	}}}
	for _, op := range []string{"iam.role.permissions.update", "identity.user_role.revoke", "iam.delegation.create", "iam.delegation.update", "iam.delegation.revoke"} {
		v.permissions = append(v.permissions, m.Permission{Code: op, Status: "enabled"})
		v.sources = append(v.sources, authorization.GrantSource{Context: c, Operation: op, Effect: authorization.Allow, Active: true, RoleScope: all, AssignmentBoundary: all, Validity: authorization.Interval{StartsAt: now.Add(-time.Hour)}})
	}
	for _, id := range []int64{2, 3} {
		v.delegations = append(v.delegations, m.Delegation{ID: id, Context: c, ManagerRoleID: 1, TargetKind: "role", TargetRoleID: id, Actions: []string{"iam.role.permissions.update", "identity.user_role.revoke"}, TargetUserScope: authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{20}}}}, GrantCeiling: []m.Ceiling{{Context: c, Operation: "channel.channel.read", Scope: all}}, Validity: authorization.Interval{StartsAt: now.Add(-time.Hour)}})
	}
	return v
}

func TestRoleGovernanceChecksIndirectFutureMembersAndSelf(t *testing.T) {
	v := governanceTestView(t)
	v.state.Assignments = []IAMAssignment{{ID: 1, Context: v.state.Context, UserID: 20, RoleID: 3, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now.Add(time.Hour)}}}
	affected, err := v.roleChange(v.state, v.state, 2, "iam.role.permissions.update")
	require.NoError(t, err)
	require.Equal(t, []int64{20}, affected)
	v.state.Assignments[0].UserID = 30
	_, err = v.roleChange(v.state, v.state, 2, "iam.role.permissions.update")
	require.ErrorIs(t, err, ErrIAMProtected)
	v.state.Assignments[0].UserID = 20
	v.future = append(v.future, 3)
	_, err = v.roleChange(v.state, v.state, 2, "iam.role.permissions.update")
	require.ErrorIs(t, err, ErrIAMProtected)
	v.future = []int64{1}
	v.delegations = v.delegations[:1]
	_, err = v.roleChange(v.state, v.state, 2, "iam.role.permissions.update")
	require.ErrorIs(t, err, ErrIAMProtected, "authority over child does not grant authority over its senior")
}

func TestRemovingDenyChecksOtherAssignmentsMaximum(t *testing.T) {
	v := governanceTestView(t)
	v.state.Roles[1].Grants = []IAMGrant{{Operation: "billing.payment.read", Effect: authorization.Deny, Scope: iamAllScope}}
	v.state.Assignments = []IAMAssignment{
		{ID: 1, Context: v.state.Context, UserID: 20, RoleID: 2, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now.Add(-time.Hour)}},
		{ID: 2, Context: v.state.Context, UserID: 20, RoleID: 4, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now.Add(time.Hour)}},
	}
	require.True(t, iamRoleHasDeny(v.state, 2))
	require.NoError(t, v.manageRole(v.state, 2, "identity.user_role.revoke", 20, nil, iamAllScope))
	require.ErrorIs(t, v.expandedUserCeiling(v.state, 2, 20, "identity.user_role.revoke"), ErrIAMProtected)
	v.delegations[0].GrantCeiling = append(v.delegations[0].GrantCeiling, m.Ceiling{Context: v.state.Context, Operation: "billing.payment.read", Scope: iamAllScope})
	require.NoError(t, v.expandedUserCeiling(v.state, 2, 20, "identity.user_role.revoke"))
}

func TestRedelegationCannotCrossKindScopeCeilingOrSelf(t *testing.T) {
	v := governanceTestView(t)
	v.state.Roles = append(v.state.Roles, IAMRole{ID: 5, Context: v.state.Context, Code: "new-manager", Status: "disabled"})
	parent := v.delegations[0]
	parent.CanRedelegate = true
	v.delegations = []m.Delegation{parent}
	child := parent
	child.ID = 0
	child.ManagerRoleID = 5
	child.CanRedelegate = false
	child.Validity.StartsAt = v.now
	require.NoError(t, v.governDelegation(child, "iam.delegation.create", nil))
	v.delegations[0].CanRedelegate = false
	require.ErrorIs(t, v.governDelegation(child, "iam.delegation.create", nil), ErrIAMProtected)
	v.delegations[0].CanRedelegate = true
	expanded := child
	expanded.TargetUserScope = iamAllScope
	require.ErrorIs(t, v.governDelegation(expanded, "iam.delegation.create", nil), ErrIAMProtected)
	expanded = child
	expanded.GrantCeiling = []m.Ceiling{{Context: v.state.Context, Operation: "billing.payment.read", Scope: iamAllScope}}
	require.ErrorIs(t, v.governDelegation(expanded, "iam.delegation.create", nil), ErrIAMProtected)
	expanded = child
	expanded.TargetKind = "role_creation"
	expanded.TargetRoleID = 0
	require.ErrorIs(t, v.governDelegation(expanded, "iam.delegation.create", nil), ErrIAMProtected)
	v.future = append(v.future, 5)
	require.ErrorIs(t, v.governDelegation(child, "iam.delegation.create", nil), ErrIAMProtected)
}

func TestAssignmentAuditUsesAuthoritativeRoleAndPersistedSourceIDs(t *testing.T) {
	v := governanceTestView(t)
	assignment := IAMAssignment{ID: 99, Context: v.state.Context, UserID: 20, RoleID: 2, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}}
	v.state.Assignments = []IAMAssignment{assignment}
	req := m.Request{ID: 99, UserID: 20}
	require.Equal(t, []IAMAssignment{assignment}, iamAuditBefore(v, "RevokeUserRole", req))
	require.Equal(t, "role:2", iamAuditTarget("RevokeUserRole", req, m.Response{Assignments: []IAMAssignment{assignment}}))
	before := v.state
	before.Assignments = nil
	impact, err := iamPermissionImpacts(before, v.state, []int64{20}, v.now, true)
	require.NoError(t, err)
	require.Zero(t, impact[0].After[0].AssignmentID)
	impact, err = iamPermissionImpacts(before, v.state, []int64{20}, v.now, false)
	require.NoError(t, err)
	require.Equal(t, int64(99), impact[0].After[0].AssignmentID)
}

func TestRootStillRequiresEnabledBoundOperation(t *testing.T) {
	v := governanceTestView(t)
	v.root = true
	require.NoError(t, v.permit("iam.role.permissions.update", 2, 0))
	v.permissions = nil
	require.Error(t, v.permit("iam.role.permissions.update", 2, 0))
	require.Error(t, v.permit("channel.channel.read", 2, 0))
}

func TestImpactDoesNotRevealUnmanagedAssignmentsOrHiddenInheritedGrants(t *testing.T) {
	v := governanceTestView(t)
	for _, op := range []string{"identity.user_role.read", "iam.role.permissions.read"} {
		v.permissions = append(v.permissions, m.Permission{Code: op, Status: "enabled"})
		v.sources = append(v.sources, authorization.GrantSource{Context: v.state.Context, Operation: op, Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now.Add(-time.Hour)}})
		v.delegations[0].Actions = append(v.delegations[0].Actions, op)
	}
	v.state.Assignments = []IAMAssignment{
		{ID: 1, Context: v.state.Context, UserID: 20, RoleID: 2, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}},
		{ID: 2, Context: v.state.Context, UserID: 20, RoleID: 4, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}},
	}
	impacts, err := v.permissionImpacts(v.state, v.state, []int64{20}, false)
	require.NoError(t, err)
	require.Len(t, impacts[0].Before, 1)
	require.Equal(t, "channel.channel.read", impacts[0].Before[0].Operation)
	// Hidden inherited grants cannot be exposed through the visible assignment.
	v.state.Roles[1].Inherits = []int64{4}
	impacts, err = v.permissionImpacts(v.state, v.state, []int64{20}, false)
	require.NoError(t, err)
	require.Len(t, impacts[0].After, 1)
	require.Equal(t, "channel.channel.read", impacts[0].After[0].Operation)
	v.delegations[0].Actions = []string{"identity.user_role.read"}
	impacts, err = v.permissionImpacts(v.state, v.state, []int64{20}, false)
	require.NoError(t, err)
	require.Empty(t, impacts[0].After)
	v.root = true
	impacts, err = v.permissionImpacts(v.state, v.state, []int64{20}, false)
	require.NoError(t, err)
	require.Len(t, impacts[0].After, 3)
}
