package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	identitydata "micro-one-api/app/identity/internal/data"
)

type loginErrorRuntime struct {
	biz.IAMRuntimeRepo
	user biz.User
}

func (r loginErrorRuntime) User(context.Context, biz.IAMTx, int64) (biz.User, error) {
	return r.user, nil
}

type loginErrorRunner struct{ biz.IAMTxRunner }

func (loginErrorRunner) ReadIAMSnapshot(ctx context.Context, read func(context.Context, biz.IAMTx) error) error {
	return read(ctx, nil)
}

type loginErrorSecurity struct {
	biz.SecurityRepo
	err error
}

func (r loginErrorSecurity) State(context.Context, biz.IAMTx, int64) (biz.MFAState, error) {
	return biz.MFAState{}, r.err
}

func TestIdentityHTTPLoginSeparatesDependencyFailuresFromCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, username, password string
		dependency               error
		status                   int
		message                  string
	}{
		{"iam storage", "alice", "password123", biz.ErrIAMDependencyUnavailable, http.StatusServiceUnavailable, "login temporarily unavailable"},
		{"mfa storage", "alice", "password123", biz.ErrMFAUnavailable, http.StatusServiceUnavailable, "login temporarily unavailable"},
		{"challenge storage", "alice", "password123", biz.ErrChallengeUnavailable, http.StatusServiceUnavailable, "login temporarily unavailable"},
		{"wrong password", "alice", "incorrect", biz.ErrIAMDependencyUnavailable, http.StatusOK, "invalid credentials"},
		{"unknown account", "unknown", "password123", biz.ErrIAMDependencyUnavailable, http.StatusOK, "invalid credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
			user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.test", "default")
			require.NoError(t, err)
			uc.SetIAMRuntime(loginErrorRuntime{user: *user}, loginErrorRunner{})
			uc.SetSecurity(loginErrorSecurity{err: fmt.Errorf("private storage detail: %w", tc.dependency)}, nil, nil)
			srv := NewHTTPServer(":0", uc, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/user/login", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, tc.username, tc.password)))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.JSONEq(t, fmt.Sprintf(`{"success":false,"message":%q}`, tc.message), rec.Body.String())
		})
	}
}
