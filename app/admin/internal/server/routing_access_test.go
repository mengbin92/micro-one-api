package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/routing"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type routingSessionFake struct {
	identityv1.IdentityServiceClient
}

func (routingSessionFake) ValidateToken(_ context.Context, r *identityv1.ValidateTokenRequest, _ ...grpc.CallOption) (*identityv1.ValidateTokenReply, error) {
	return &identityv1.ValidateTokenReply{Valid: r.Token == "user-session", UserId: 42}, nil
}
func (routingSessionFake) GetUser(context.Context, *identityv1.GetUserRequest, ...grpc.CallOption) (*identityv1.GetUserReply, error) {
	return &identityv1.GetUserReply{User: &commonv1.UserInfo{Id: 42, Role: 1}}, nil
}

type routingAccessHTTPFake struct {
	calls     int
	user      int64
	self      bool
	createErr error
}

// CreateGroup implements the optional creation capability the group POST
// handler type-asserts for.
func (f *routingAccessHTTPFake) CreateGroup(_ context.Context, key, displayName, description, accessMode string) (*routing.Group, error) {
	f.calls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &routing.Group{ID: 11, Key: key, DisplayName: displayName, Description: description, Status: "disabled", AccessMode: accessMode, ModelAccessMode: "all_authorized", Revision: 1}, nil
}

func (f *routingAccessHTTPFake) Facts(context.Context, int64) (*routing.SubjectFacts, error) {
	return &routing.SubjectFacts{DefaultGroupID: 1, AccessRevision: 2}, nil
}
func (f *routingAccessHTTPFake) Available(_ context.Context, u int64, _ routing.GroupListRequest) (*biz.AvailableRoutingGroups, error) {
	f.calls++
	f.user = u
	return &biz.AvailableRoutingGroups{Facts: &routing.SubjectFacts{DefaultGroupID: 1, AccessRevision: 2}, Groups: []biz.AvailableRoutingGroup{}}, nil
}
func (f *routingAccessHTTPFake) Change(_ context.Context, c biz.RoutingAccessChange, self bool) (*routing.SubjectFacts, error) {
	f.calls++
	f.user = c.UserID
	f.self = self
	return nil, biz.ErrRoutingAccessDenied
}
func (f *routingAccessHTTPFake) CreateToken(_ context.Context, u int64, n, m string, g int64, groupIDs []int64) (*biz.RoutingToken, error) {
	f.calls++
	f.user = u
	return &biz.RoutingToken{ID: 5, Name: n, Mode: m, GroupID: g, GroupIDs: groupIDs, Key: "created-once", Revision: 1}, nil
}
func (f *routingAccessHTTPFake) SetToken(context.Context, int64, int64, string, int64, int64, []int64) (int64, error) {
	return 0, biz.ErrRoutingAccessDenied
}
func TestRoutingAccessHTTPUsesAuthenticatedPrincipal(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-system")
	f := &routingAccessHTTPFake{}
	svc := service.NewAdminService(nil, routingSessionFake{}, nil, nil)
	svc.SetRoutingAccessUsecase(f)
	srv := NewHTTPServer(":0", svc, nil)
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/api/v1/routing-groups/available", "", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Zero(t, f.calls)
	w = call("POST", "/api/v1/routing-tokens", `{"name":"a","routing_mode":"fixed","routing_group_id":3,"user_id":999}`, "user-session")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.EqualValues(t, 42, f.user)
	w = call("PATCH", "/api/v1/routing-access", `{"operation":"grant","routing_group_id":3,"user_id":999}`, "user-session")
	require.Equal(t, 403, w.Code)
	require.True(t, f.self)
	require.EqualValues(t, 42, f.user)
	n := f.calls
	w = call("PATCH", "/api/v1/admin/routing-access/999", `{"operation":"grant"}`, "user-session")
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, n, f.calls)
	w = call("GET", "/api/v1/routing-groups/available?page_size=-1", "", "user-session")
	require.Equal(t, 400, w.Code)
}
