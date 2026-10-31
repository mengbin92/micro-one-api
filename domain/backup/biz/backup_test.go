package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	"testing"
	"time"
)

type fakeRepo struct {
	Repo
	calls int
}

func (r *fakeRepo) Policy(context.Context) (Policy, error) { r.calls++; return DefaultPolicy(), nil }
func (r *fakeRepo) Enqueue(context.Context, string, string, string, time.Time) (Job, error) {
	r.calls++
	return Job{Status: "queued"}, nil
}

type resolver struct {
	operation string
	deny      bool
}

func (r *resolver) Query(_ context.Context, point, op, cred string) (authorization.ResourceAuthorization, error) {
	r.operation = op
	if r.deny {
		return authorization.ResourceAuthorization{}, authorization.ErrDenied
	}
	return authorization.ResourceAuthorization{Mode: "iam", Query: authorization.QueryScope{ActorID: 7, ValidUntil: time.Now().Add(time.Minute), Allow: []authorization.Scope{{Clauses: []authorization.Clause{{All: true}}}}}}, nil
}
func TestBackupBoundaryRequiresAuthorizationAndReason(t *testing.T) {
	var u *Usecase
	_, err := u.Policy(context.Background())
	require.ErrorIs(t, err, ErrUnavailable)
	p := DefaultPolicy()
	require.NoError(t, p.Validate())
	p.IntervalHours = 48
	require.ErrorIs(t, p.Validate(), ErrInvalid)
	r := &fakeRepo{}
	a := &resolver{deny: true}
	u = NewUsecase(r, a)
	ctx := authorization.WithCredential(authorization.WithExternal(context.Background()), "session")
	_, err = u.Policy(ctx)
	require.Error(t, err)
	require.Zero(t, r.calls)
	a.deny = false
	_, err = u.Policy(ctx)
	require.NoError(t, err)
	require.Equal(t, "system.option.read", a.operation)
	_, err = u.Create(ctx, "valid-request-1234", "user")
	require.ErrorIs(t, err, ErrInvalid)
	ctx = authorization.WithWriteReason(ctx, "isolated recovery drill")
	_, err = u.Create(ctx, "valid-request-1234", "user")
	require.NoError(t, err)
	require.Equal(t, "system.option.security.update", a.operation)
}
