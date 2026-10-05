package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

type iamReviewRepo struct{ IAMManagementRepo }

func (iamReviewRepo) User(_ context.Context, _ IAMTx, id int64) (User, error) {
	return User{ID: id, Status: UserStatusEnabled}, nil
}

func TestIAMReviewAssignmentDenyWindow(t *testing.T) {
	for _, method := range []string{"AssignUserRole", "BatchAssignUserRoles"} {
		for _, change := range []string{"delay", "shorten", "revoke"} {
			t.Run(method+"/"+change, func(t *testing.T) {
				v := governanceTestView(t)
				v.state.Roles[1].Grants = []IAMGrant{{Operation: "billing.payment.read", Effect: authorization.Deny, Scope: iamAllScope}}
				for _, op := range []string{"identity.user_role.assign", "identity.user_role.batch_assign"} {
					v.permissions = append(v.permissions, m.Permission{Code: op, Status: "enabled"})
					v.sources = append(v.sources, authorization.GrantSource{Context: v.state.Context, Operation: op, Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now.Add(-time.Hour)}})
					v.delegations[0].Actions = append(v.delegations[0].Actions, op)
				}
				old := IAMAssignment{ID: 1, Revision: 1, Context: v.state.Context, UserID: 20, RoleID: 2, Boundary: iamAllScope, Origin: "explicit", Validity: authorization.Interval{StartsAt: v.now.Add(-time.Hour)}}
				v.state.Assignments = []IAMAssignment{old, {ID: 2, Context: v.state.Context, UserID: 20, RoleID: 4, Boundary: iamAllScope, Origin: "explicit", Validity: authorization.Interval{StartsAt: v.now.Add(-time.Hour)}}}
				next := old
				switch change {
				case "delay":
					next.Validity.StartsAt = v.now.Add(time.Hour)
				case "shorten":
					end := v.now.Add(time.Minute)
					next.Validity.ExpiresAt = &end
				case "revoke":
					next.Revoked = true
				}
				req := m.Request{Context: v.state.Context, ID: old.ID, UserID: old.UserID, ExpectedRevision: old.Revision, Assignment: &next, Assignments: []IAMAssignment{next}}
				uc := &IAMGovernanceUsecase{repo: iamReviewRepo{}}
				_, _, err := uc.propose(context.Background(), nil, v, method, req, IAMMethods[method])
				require.ErrorIs(t, err, ErrIAMProtected, "removing mandatory deny must check the unrelated financial allow")
				v.delegations[0].GrantCeiling = append(v.delegations[0].GrantCeiling, m.Ceiling{Context: v.state.Context, Operation: "billing.payment.read", Scope: iamAllScope})
				_, _, err = uc.propose(context.Background(), nil, v, method, req, IAMMethods[method])
				require.NoError(t, err)
			})
		}
	}
}

func TestIAMReviewArchivedPermissionCannotRevive(t *testing.T) {
	v := governanceTestView(t)
	v.root = true
	v.permissions = append(v.permissions, m.Permission{Code: "iam.permission.enable", Status: "enabled"}, m.Permission{ID: 99, Code: "channel.channel.test", Name: "retired", Status: "archived", Revision: 2})
	v.sources = append(v.sources, authorization.GrantSource{Context: v.state.Context, Operation: "iam.permission.enable", Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}})
	req := m.Request{ID: 99, ExpectedRevision: 2, Permission: &m.Permission{Status: "enabled"}}
	_, _, err := (&IAMGovernanceUsecase{}).propose(context.Background(), nil, v, "SetPermissionStatus", req, "iam.permission.enable")
	require.ErrorIs(t, err, ErrIAMProtected)
}

func TestIAMReviewRootDisplayUsesExplicitGrants(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	c := authorization.Platform()
	root := IAMRole{ID: 1, Context: c, Code: "root", Builtin: true, Status: "enabled", Grants: []IAMGrant{{Operation: "admin.console.enter", Effect: authorization.Allow, Scope: iamAllScope}}}
	f := &iamProjectionRepo{iamSnapshotFake: &iamSnapshotFake{
		user:    User{ID: 42, Status: UserStatusEnabled},
		policy:  authorization.PolicyState{Mode: "iam", Cutover: "complete"},
		session: IAMSessionContext{SessionID: "root-jti", UserID: 42, Context: c, ExpiresAt: now.Add(time.Hour), ActivationState: "active", ActiveRoleIDs: []int64{1}},
		state:   IAMConstraintState{Context: c, Roles: []IAMRole{root}, Assignments: []IAMAssignment{{ID: 1, Context: c, UserID: 42, RoleID: 1, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: now}}}},
	}, permissions: []m.Permission{{Code: "admin.console.enter", Status: "enabled"}, {Code: "channel.channel.delete", Status: "enabled"}}}
	uc := NewIdentityUsecase(&mockIdentityRepo{}, nil)
	uc.SetIAMRuntime(f, iamSnapshotRunner{})
	uc.now = func() time.Time { return now }
	snapshot, err := uc.readIAMAuthorization(context.Background(), authorization.Actor{UserID: 42, SessionID: "root-jti", ExpiresAt: f.session.ExpiresAt}, c)
	require.NoError(t, err)
	require.Equal(t, []string{"admin.console.enter"}, snapshot.PermittedOperations)
}

func TestIAMReviewRoleSourcesHideIntermediateRoles(t *testing.T) {
	v := governanceTestView(t)
	// A visible parent and leaf do not authorize reading the hidden middle.
	v.state.Roles[1].Inherits = []int64{3}
	v.state.Roles[2].Inherits = []int64{4}
	v.permissions = append(v.permissions, m.Permission{Code: "iam.role.permissions.read", Status: "enabled"})
	v.sources = append(v.sources, authorization.GrantSource{Context: v.state.Context, Operation: "iam.role.permissions.read", Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}})
	v.delegations = []m.Delegation{}
	for _, id := range []int64{2, 4} {
		v.delegations = append(v.delegations, m.Delegation{ID: id, Context: v.state.Context, ManagerRoleID: 1, TargetKind: "role", TargetRoleID: id, Actions: []string{"iam.role.permissions.read"}, Validity: authorization.Interval{StartsAt: v.now}})
	}
	result, err := (&IAMGovernanceUsecase{}).read(context.Background(), nil, v, "GetRolePermissions", m.Request{Context: v.state.Context, ID: 2}, "iam.role.permissions.read")
	require.NoError(t, err)
	for _, source := range result.Sources {
		require.NotContains(t, source.InheritancePath, int64(3), "hidden role IDs and paths are not grant read authority")
	}
}

func TestIAMReviewAssignmentBoundaryMatchesInheritedOperations(t *testing.T) {
	now, state := iamConstraintFixture()
	state.Roles[3].Grants = []IAMGrant{{Operation: "admin.console.enter", Effect: authorization.Allow, Scope: iamAllScope}}
	a := iamAssignmentFixture(1, 20, 1, now, nil)
	a.Boundary = authorization.Scope{Clauses: []authorization.Clause{{RoutingGroupIDs: []int64{11}}}}
	state.Assignments = []IAMAssignment{a}
	_, err := IAMCheckConstraints(now, state)
	require.ErrorIs(t, err, ErrIAMScopeInvalid, "a successful write must not leave the target's next authorization snapshot invalid")
}

func TestIAMReviewAssignmentPayloadCannotBorrowRevocationAuthority(t *testing.T) {
	for _, method := range []string{"AssignUserRole", "BatchAssignUserRoles"} {
		t.Run(method, func(t *testing.T) {
			v := governanceTestView(t)
			op := IAMMethods[method]
			v.permissions = []m.Permission{{Code: op, Status: "enabled"}}
			v.sources = []authorization.GrantSource{{Context: v.state.Context, Operation: op, Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}}}
			v.delegations[0].Actions = []string{op}
			a := IAMAssignment{ID: 1, Revision: 1, Context: v.state.Context, UserID: 20, RoleID: 2, Boundary: iamAllScope, Origin: "explicit", Validity: authorization.Interval{StartsAt: v.now}}
			v.state.Assignments = []IAMAssignment{a}
			a.Revoked = true
			_, _, err := (&IAMGovernanceUsecase{repo: iamReviewRepo{}}).propose(context.Background(), nil, v, method, m.Request{Context: v.state.Context, ID: 1, UserID: 20, ExpectedRevision: 1, Assignment: &a, Assignments: []IAMAssignment{a}}, op)
			require.Error(t, err, "assign/batch authority cannot replace the independent revoke operation and delegation action")
		})
	}
}

func TestIAMReviewEffectivePermissionsRequiresGrantReadAuthority(t *testing.T) {
	v := governanceTestView(t)
	for _, op := range []string{"iam.authorization.user.read", "identity.user_role.read"} {
		v.permissions = append(v.permissions, m.Permission{Code: op, Status: "enabled"})
		v.sources = append(v.sources, authorization.GrantSource{Context: v.state.Context, Operation: op, Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}})
	}
	v.delegations[0].Actions = append(v.delegations[0].Actions, "identity.user_role.read")
	v.state.Assignments = []IAMAssignment{{ID: 1, Context: v.state.Context, UserID: 20, RoleID: 2, Boundary: iamAllScope, Validity: authorization.Interval{StartsAt: v.now}}}
	_, err := (&IAMGovernanceUsecase{}).read(context.Background(), nil, v, "GetUserEffectivePermissions", m.Request{Context: v.state.Context, UserID: 20}, "iam.authorization.user.read")
	require.ErrorIs(t, err, ErrIAMProtected, "member read must not disclose role grants without independent grant read authority")
}

func TestIAMReviewMenuAncestorsDoNotGrantParentOperations(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	f := &iamProjectionRepo{permissions: []m.Permission{{Code: "channel.channel.list", Status: "enabled"}}, menus: []m.Menu{
		{ID: 1, Name: "Parent", Enabled: true, RouteKey: "settings", RequiredAll: []string{"system.option.read"}},
		{ID: 2, ParentID: 1, Name: "Channels", Enabled: true, RouteKey: "channels", RequiredAll: []string{"channel.channel.list"}},
	}}
	uc := NewIdentityUsecase(&mockIdentityRepo{}, nil)
	uc.iam = f
	out := &IAMAuthorizationSnapshot{Actor: authorization.Actor{UserID: 20, SessionID: "menu-jti", ExpiresAt: now.Add(time.Hour)}, Context: authorization.Platform(), Policy: authorization.PolicyState{Mode: "iam"}, Session: IAMSessionContext{ActivationState: "active"}, Sources: []authorization.GrantSource{{Context: authorization.Platform(), Operation: "channel.channel.list", Effect: authorization.Allow, Active: true, RoleScope: iamAllScope, AssignmentBoundary: iamAllScope, Validity: authorization.Interval{StartsAt: now}}}}
	require.NoError(t, uc.projectSessionAuthorization(context.Background(), nil, out, IAMConstraintState{}, now))
	require.Len(t, out.Menus, 2, "a visible child retains its presentation ancestor")
	require.Equal(t, []string{"channel.channel.list"}, out.PermittedOperations, "parent visibility grants no parent operation")
}
