package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
)

type loginDependencyRuntime struct {
	biz.IAMRuntimeRepo
	user biz.User
}

func (r loginDependencyRuntime) User(context.Context, biz.IAMTx, int64) (biz.User, error) {
	return r.user, nil
}

type loginDependencyRunner struct{ biz.IAMTxRunner }

func (loginDependencyRunner) ReadIAMSnapshot(ctx context.Context, fn func(context.Context, biz.IAMTx) error) error {
	return fn(ctx, nil)
}

type loginDependencySecurity struct {
	biz.SecurityRepo
	err error
}

func (r loginDependencySecurity) State(context.Context, biz.IAMTx, int64) (biz.MFAState, error) {
	return biz.MFAState{}, r.err
}

func TestLoginDistinguishesSecurityDependenciesFromCredentials(t *testing.T) {
	for _, dependency := range []error{biz.ErrIAMDependencyUnavailable, biz.ErrMFAUnavailable, biz.ErrChallengeUnavailable} {
		t.Run(dependency.Error(), func(t *testing.T) {
			uc := biz.NewIdentityUsecase(data.NewMemoryRepositoryForTest(), nil)
			user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.test", "default")
			require.NoError(t, err)
			uc.SetIAMRuntime(loginDependencyRuntime{user: *user}, loginDependencyRunner{})
			uc.SetSecurity(loginDependencySecurity{err: fmt.Errorf("private storage detail: %w", dependency)}, nil, nil)
			s := NewIdentityService(uc)
			out, err := s.Login(context.Background(), &v.LoginRequest{Username: "alice", Password: "password123"})
			require.Equal(t, codes.Unavailable, status.Code(err))
			require.Nil(t, out)
			require.NotContains(t, err.Error(), "private storage detail")
			out, err = s.Login(context.Background(), &v.LoginRequest{Username: "alice", Password: "wrong"})
			require.NoError(t, err)
			require.False(t, out.Success)
			require.Equal(t, "invalid credentials", out.Message)
		})
	}
}
