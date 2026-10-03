package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

type iamProjectionRepo struct {
	*iamSnapshotFake
	permissions []m.Permission
	menus       []m.Menu
}

func (f *iamProjectionRepo) Permissions(context.Context, IAMTx) ([]m.Permission, error) {
	return f.permissions, nil
}
func (f *iamProjectionRepo) Menus(context.Context, IAMTx) ([]m.Menu, error) { return f.menus, nil }

func TestIAMSessionDisplayProjection(t *testing.T) {
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	c := authorization.Platform()
	all := authorization.Scope{Clauses: []authorization.Clause{{All: true}}}
	groups := authorization.Scope{Clauses: []authorization.Clause{{RoutingGroupIDs: []int64{11}}}}
	role := IAMRole{ID: 1, Context: c, Code: "operator", Name: "Operator", Status: "enabled", Grants: []IAMGrant{
		{Operation: "admin.console.enter", Effect: authorization.Allow, Scope: all},
		{Operation: "channel.channel.list", Effect: authorization.Allow, Scope: groups},
		{Operation: "channel.channel.secret.read", Effect: authorization.Allow, Scope: groups},
	}}
	denied := IAMRole{ID: 2, Context: c, Code: "deny", Status: "enabled", Grants: []IAMGrant{{Operation: "channel.channel.secret.read", Effect: authorization.Deny, Scope: all}}}
	fake := &iamProjectionRepo{iamSnapshotFake: &iamSnapshotFake{
		user:    User{ID: 42, Status: UserStatusEnabled},
		policy:  authorization.PolicyState{Mode: "iam", Cutover: "complete", BatchID: "isolated", VerifiedAt: &now, PolicyRevision: 4, CatalogRevision: 1},
		session: IAMSessionContext{SessionID: "jti", UserID: 42, Context: c, ExpiresAt: now.Add(time.Hour), ActivationState: "active", ActiveRoleIDs: []int64{1}, SessionRevision: 1, Revision: 2},
		state: IAMConstraintState{Context: c, Roles: []IAMRole{role, denied}, Assignments: []IAMAssignment{
			{ID: 1, UserID: 42, RoleID: 1, Context: c, Validity: authorization.Interval{StartsAt: now}, Boundary: all},
			{ID: 2, UserID: 42, RoleID: 2, Context: c, Validity: authorization.Interval{StartsAt: now}, Boundary: all},
		}},
	}, permissions: []m.Permission{
		{Code: "admin.console.enter", Status: "enabled"}, {Code: "channel.channel.list", Status: "enabled"},
		{Code: "channel.channel.secret.read", Status: "enabled"}, {Code: "channel.channel.delete", Status: "draft"}, {Code: "unknown.code", Status: "enabled"},
	}, menus: []m.Menu{
		{ID: 1, Enabled: true, RouteKey: "channels", RequiredAll: []string{"admin.console.enter"}, RequiredAny: []string{"channel.channel.list"}},
		{ID: 2, Enabled: true, RequiredAll: []string{"channel.channel.secret.read"}}, {ID: 3, Enabled: false},
	}}
	uc := NewIdentityUsecase(&mockIdentityRepo{}, nil)
	uc.SetIAMRuntime(fake, iamSnapshotRunner{})
	uc.now = func() time.Time { return now }
	actor := authorization.Actor{UserID: 42, SessionID: "jti", ExpiresAt: fake.session.ExpiresAt}
	snapshot, err := uc.readIAMAuthorization(context.Background(), actor, c)
	require.NoError(t, err)
	require.Equal(t, []string{"admin.console.enter", "channel.channel.list"}, snapshot.PermittedOperations, "inactive assigned denies remain mandatory")
	require.Len(t, snapshot.Menus, 1)
	require.EqualValues(t, 1, snapshot.Menus[0].ID)
	require.Len(t, snapshot.Roles, 2)
	for _, r := range snapshot.Roles {
		require.Empty(t, r.Grants, "session metadata must not expose another role's complete grant set")
	}
	fake.permissions[1].Status = "disabled"
	snapshot, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.NoError(t, err)
	require.Equal(t, []string{"admin.console.enter"}, snapshot.PermittedOperations)
	require.Empty(t, snapshot.Menus)
	fake.session.ActivationState = "selection_required"
	snapshot, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.NoError(t, err)
	require.Empty(t, snapshot.PermittedOperations)
	require.Empty(t, snapshot.Menus)
	fake.policy = authorization.PolicyState{Mode: "legacy", Cutover: "idle", PolicyRevision: 4, CatalogRevision: 1}
	snapshot = &IAMAuthorizationSnapshot{Policy: fake.policy, AuthorizedRoleIDs: []int64{1}, Session: fake.session}
	err = uc.projectSessionAuthorization(context.Background(), nil, snapshot, fake.state, now)
	require.NoError(t, err)
	require.Empty(t, snapshot.PermittedOperations)
}
