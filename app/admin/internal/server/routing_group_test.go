package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/routing"
	"net/http"
	"net/http/httptest"
	"testing"
)

type routingGroupReaderFake struct {
	calls int
	err   error
}

func (f *routingGroupReaderFake) List(context.Context, routing.GroupListRequest) (*routing.GroupListResult, error) {
	f.calls++
	return &routing.GroupListResult{Groups: []*routing.Group{{ID: 2, Key: "vip", Status: "disabled"}}}, f.err
}
func (f *routingGroupReaderFake) Get(context.Context, int64) (*routing.GroupDetail, error) {
	f.calls++
	return &routing.GroupDetail{Group: &routing.Group{ID: 2, Key: "vip"}}, f.err
}
func TestRoutingGroupHTTPAuthorizationAndReadOnly(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	fake := &routingGroupReaderFake{}
	svc := service.NewAdminService(nil, nil, nil, nil)
	svc.SetRoutingGroupUsecase(fake)
	srv := NewHTTPServer(":0", svc, nil)
	for _, path := range []string{"/api/v1/admin/routing-groups", "/api/v1/admin/routing-groups/2"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code)
		require.Zero(t, fake.calls)
	}
	for _, tt := range []struct {
		method, path string
		want         int
	}{{"GET", "/api/v1/admin/routing-groups", 200}, {"GET", "/api/v1/admin/routing-groups/2", 200}, {"GET", "/api/v1/admin/routing-groups/-1", 400}, {"GET", "/api/v1/admin/routing-groups?page_size=2147483648", 400}, {"GET", "/api/v1/admin/routing-groups?page_size=-1", 400}, {"POST", "/api/v1/admin/routing-groups", 405}, {"DELETE", "/api/v1/admin/routing-groups/2", 405}} {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Equal(t, tt.want, w.Code, w.Body.String())
	}
	fake.err = status.Error(codes.Unavailable, "SECRET_SENTINEL_DSN")
	req := httptest.NewRequest("GET", "/api/v1/admin/routing-groups", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "SECRET_SENTINEL")
}
