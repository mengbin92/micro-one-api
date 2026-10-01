package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
)

type iamSnapshotFake struct {
	IAMRuntimeRepo
	state   IAMConstraintState
	session IAMSessionContext
	user    User
	policy  authorization.PolicyState
}

func (f *iamSnapshotFake) Policy(context.Context, IAMTx) (authorization.PolicyState, error) {
	return f.policy, nil
}
func (f *iamSnapshotFake) User(context.Context, IAMTx, int64) (User, error)           { return f.user, nil }
func (f *iamSnapshotFake) UserRevision(context.Context, IAMTx, int64) (uint64, error) { return 3, nil }
func (f *iamSnapshotFake) Session(context.Context, IAMTx, string, authorization.Context) (IAMSessionContext, error) {
	return f.session, nil
}
func (f *iamSnapshotFake) ConstraintState(context.Context, IAMTx, authorization.Context) (IAMConstraintState, error) {
	return f.state, nil
}

type iamSnapshotRunner struct{}

func (iamSnapshotRunner) ReadIAMSnapshot(ctx context.Context, fn func(context.Context, IAMTx) error) error {
	return fn(ctx, nil)
}
func (iamSnapshotRunner) RunIAMWrite(context.Context, func(context.Context, IAMTx) error) error {
	return ErrIAMProtected
}

func TestIAMSnapshotExpiryWithoutCleanup(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := at.Add(time.Minute)
	future := end.Add(time.Minute)
	c := authorization.Platform()
	roles := []IAMRole{{ID: 1, Context: c, Status: "enabled", Grants: []IAMGrant{{Operation: "identity.user.read", Effect: authorization.Allow, Scope: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}}}}, {ID: 2, Context: c, Status: "enabled", Grants: []IAMGrant{{Operation: "identity.user.read", Effect: authorization.Deny, Scope: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}}}}}
	fake := &iamSnapshotFake{user: User{ID: 42, Status: UserStatusEnabled}, policy: authorization.PolicyState{Mode: "iam", Cutover: "complete", BatchID: "isolated", VerifiedAt: &at, PolicyRevision: 4, CatalogRevision: 1}, session: IAMSessionContext{SessionID: "real-jti", UserID: 42, Context: c, ExpiresAt: at.Add(time.Hour), ActivationState: "active", ActiveRoleIDs: []int64{1}, SessionRevision: 1, Revision: 2}}
	fake.state = IAMConstraintState{Context: c, Roles: roles, Assignments: []IAMAssignment{{ID: 1, UserID: 42, RoleID: 1, Context: c, Validity: authorization.Interval{StartsAt: at, ExpiresAt: &end}, Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}}, {ID: 2, UserID: 42, RoleID: 2, Context: c, Validity: authorization.Interval{StartsAt: future}, Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}}}}
	uc := NewIdentityUsecase(&mockIdentityRepo{}, nil)
	uc.SetIAMRuntime(fake, iamSnapshotRunner{})
	now := at
	uc.now = func() time.Time { return now }
	actor := authorization.Actor{UserID: 42, SessionID: "real-jti", ExpiresAt: fake.session.ExpiresAt}
	snap, err := uc.readIAMAuthorization(context.Background(), actor, c)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, snap.ActiveRoleIDs)
	require.Equal(t, end, snap.ValidUntil)
	// At the half-open endpoint, the stale session role has no effective source.
	now = end
	snap, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.NoError(t, err)
	require.Empty(t, snap.ActiveRoleIDs)
	require.Equal(t, future, snap.ValidUntil)
	now = future
	snap, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, snap.AuthorizedRoleIDs)
	require.False(t, snap.Sources[1].Active)
	require.Equal(t, authorization.Deny, snap.Sources[1].Effect)
	fake.user.Status = UserStatusDisabled
	_, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.ErrorIs(t, err, ErrUserDisabled)
	fake.user.Status = UserStatusEnabled
	fake.user.PasswordChangedAt = 1
	_, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.ErrorIs(t, err, ErrSessionRevoked)
	fake.user.PasswordChangedAt = 0
	now = actor.ExpiresAt
	_, err = uc.readIAMAuthorization(context.Background(), actor, c)
	require.ErrorIs(t, err, ErrInvalidToken)
}
