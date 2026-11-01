package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/admin/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
	b "micro-one-api/domain/backup/biz"
	"net/http/httptest"
	"strings"
	"testing"
)

type backupRepo struct {
	b.Repo
	writes int
}

func (r *backupRepo) Policy(context.Context) (b.Policy, error) { return b.DefaultPolicy(), nil }
func (r *backupRepo) UpdatePolicy(_ context.Context, p b.Policy, _, _ string) (b.Policy, error) {
	r.writes++
	return p, nil
}
func TestManagedBackupHTTPErrorCodesAndClosedRoutes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{b.ErrInvalid, 400}, {b.ErrConflict, 409}, {b.ErrUnavailable, 503}, {status.Error(codes.PermissionDenied, "denied"), 403}, {authorization.ErrDenied, 403}} {
		w := httptest.NewRecorder()
		writeBackupResponse(w, nil, tc.err)
		require.Equal(t, tc.code, w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/admin/backup-policy", "/api/admin/backups"} {
		require.True(t, iamUserRouteReady(httptest.NewRequest("GET", path, nil)))
		require.False(t, iamUserRouteReady(httptest.NewRequest("DELETE", path, nil)))
		require.False(t, iamUserRouteReady(httptest.NewRequest("GET", path+"/download", nil)))
	}
	svc := service.NewAdminService(nil, nil, nil, nil)
	w := httptest.NewRecorder()
	handleBackupPolicy(w, httptest.NewRequest("GET", "/api/admin/backup-policy", nil), svc)
	require.Equal(t, 503, w.Code)
	r := &backupRepo{}
	svc.SetBackupUsecase(b.NewUsecase(r, nil))
	w = httptest.NewRecorder()
	handleBackupPolicy(w, httptest.NewRequest("PUT", "/api/admin/backup-policy", strings.NewReader(`{"policy":{"revision":1,"interval_hours":24,"retain":1,"rpo_hours":24,"rto_minutes":60}}`)), svc)
	require.Equal(t, 400, w.Code)
	require.Zero(t, r.writes)
	_, err := svc.UpdateBackupPolicy(context.Background(), &v.UpdateBackupPolicyRequest{})
	require.ErrorIs(t, err, b.ErrInvalid)
}
