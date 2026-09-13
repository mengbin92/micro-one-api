package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/routing"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
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
	}{{"GET", "/api/v1/admin/routing-groups", 200}, {"GET", "/api/v1/admin/routing-groups/2", 200}, {"GET", "/api/v1/admin/routing-groups/-1", 400}, {"GET", "/api/v1/admin/routing-groups?page_size=2147483648", 400}, {"GET", "/api/v1/admin/routing-groups?page_size=-1", 400}, {"POST", "/api/v1/admin/routing-groups", 400}, {"DELETE", "/api/v1/admin/routing-groups/2", 405}} {
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

func TestCreateRoutingGroupHTTP(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	fake := &routingAccessHTTPFake{}
	svc := service.NewAdminService(nil, nil, nil, nil)
	svc.SetRoutingAccessUsecase(fake)
	srv := NewHTTPServer(":0", svc, nil)
	call := func(body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/routing-groups", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}

	// Unauthenticated creation is rejected before the usecase is reached.
	w := call(`{"key":"vip"}`, "")
	require.Equal(t, 401, w.Code)
	require.Zero(t, fake.calls)

	// A created group is returned with the owner-decided shape (disabled,
	// revision 1) and the caller's key/label.
	w = call(`{"key":"vip","display_name":"VIP 客户","description":"付费","access_mode":"public"}`, "admin-token")
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"key":"vip"`)
	require.Contains(t, w.Body.String(), `"status":"disabled"`)
	require.Contains(t, w.Body.String(), `"success":true`)

	// A duplicate key surfaces as a distinct conflict message.
	fake.createErr = kratoserrors.Conflict(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_EXISTS.String(), "exists")
	w = call(`{"key":"vip"}`, "admin-token")
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "分组标识已存在")

	// Invalid operator input stays a 400 and never leaks upstream detail.
	fake.createErr = biz.ErrRoutingGroupInvalid
	w = call(`{"key":" vip "}`, "admin-token")
	require.Equal(t, 400, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "vip ")

	// Malformed JSON is rejected at the transport boundary.
	require.Equal(t, 400, call(`{"key":`, "admin-token").Code)
}
