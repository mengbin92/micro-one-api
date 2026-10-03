package integration

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	c "micro-one-api/api/common/v1"
	v "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMA6RealAdminIdentityEndpoints(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "a6-service")
	t.Setenv("JWT_SECRET_KEY", "a6-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "a6-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("ADMIN_TOKEN", "a6-rescue")
	t.Setenv("IAM_RESCUE_SERVICE_TOKEN", "a6-rescue-service")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, grpcSrv, selfHTTP := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "member", "password123", "member@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "isolated-a6", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = grpcSrv.Serve(listener) }()
	t.Cleanup(func() { grpcSrv.Server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///a6", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := v.NewIAMServiceClient(conn)
	token := func(uid int64, jti string) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": jti, "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("a6-jwt"))
		require.NoError(t, err)
		return raw
	}
	rootRaw := token(1, "a6-root")
	memberRaw := token(member.ID, "a6-member")
	platform := &c.AuthorizationContext{ContextType: "platform", ContextKey: "platform"}
	outgoing := func(service, operator string) context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+service, "x-operator-authorization", "Bearer "+operator))
	}
	for _, tt := range []struct {
		service, operator string
		code              codes.Code
	}{{"wrong", rootRaw, codes.Unauthenticated}, {"a6-service", "", codes.Unauthenticated}, {"a6-service", "a6-rescue", codes.Unauthenticated}, {"a6-service", memberRaw, codes.PermissionDenied}} {
		_, err = client.ListRoles(outgoing(tt.service, tt.operator), &v.IAMRequest{Context: platform})
		require.Equal(t, tt.code, status.Code(err))
	}
	adminHTTP := admintest.NewIAMHTTP(client)
	// The RPC adapter relies on existing connection credentials for the service
	// token. For this test the connection interceptor injects the same credential.
	authenticatedConn, err := grpc.NewClient("passthrough:///a6-auth", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		md = md.Copy()
		md.Set("authorization", "Bearer a6-service")
		return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = authenticatedConn.Close() })
	adminHTTP = admintest.NewIAMHTTP(v.NewIAMServiceClient(authenticatedConn))
	call := func(method, path, body, raw string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if raw != "" {
			req.Header.Set("Authorization", "Bearer "+raw)
		}
		w := httptest.NewRecorder()
		adminHTTP.ServeHTTP(w, req)
		return w
	}
	for _, tt := range []struct {
		raw  string
		want int
	}{{"", 401}, {"a6-rescue", 401}, {memberRaw, 403}, {rootRaw, 200}} {
		w := call("GET", "/api/v1/admin/iam/roles", "", tt.raw)
		require.Equal(t, tt.want, w.Code, w.Body.String())
	}
	list, err := client.ListRoles(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform})
	require.NoError(t, err)
	body := fmt.Sprintf(`{"role":{"code":"http-role","name":"HTTP role"},"expectedPolicyRevision":"%d","reason":"acceptance","requestId":"http-create"}`, list.BasePolicyRevision)
	created := call("POST", "/api/v1/admin/iam/roles", body, rootRaw)
	require.Equal(t, 200, created.Code, created.Body.String())
	var reply v.IAMReply
	require.NoError(t, protojson.Unmarshal(created.Body.Bytes(), &reply))
	require.Len(t, reply.Roles, 1)
	id := reply.Roles[0].Id
	stale := call("PATCH", fmt.Sprint("/api/v1/admin/iam/roles/", id), fmt.Sprintf(`{"role":{"name":"stale"},"updateMask":"name","expectedRevision":"1","expectedPolicyRevision":"%d","reason":"CAS","requestId":"stale"}`, list.BasePolicyRevision), rootRaw)
	require.Equal(t, 409, stale.Code, stale.Body.String())
	invalid := call("POST", "/api/v1/admin/iam/roles", `{"actor_user_id":"1"}`, memberRaw)
	require.Equal(t, 400, invalid.Code, invalid.Body.String())
	org := call("GET", "/api/v1/admin/iam/roles?context.context_type=organization&context.organization_id=1&context.context_key=organization:1", "", rootRaw)
	require.Equal(t, 400, org.Code, org.Body.String())
	// Direct identity HTTP self route owns the verified JTI and rejects target IDs.
	for _, path := range []string{"/api/user/authorization", "/api/user/session/roles"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+memberRaw)
		w := httptest.NewRecorder()
		selfHTTP.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "password_hash")
	}
	forged := httptest.NewRequest("GET", "/api/user/authorization?user_id=1", nil)
	forged.Header.Set("Authorization", "Bearer "+memberRaw)
	w := httptest.NewRecorder()
	selfHTTP.ServeHTTP(w, forged)
	require.Equal(t, 400, w.Code, w.Body.String())
	// Dedicated rescue cannot be reached with the shared service token.
	_, err = client.RescueRootCredential(outgoing("a6-service", "a6-rescue"), &v.IAMRescueRequest{UserId: 1, Password: "newpassword", Reason: "recovery", ExpectedRevision: 1, ExpectedPolicyRevision: reply.BasePolicyRevision})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	// A CheckAuthorization selector is an owner endpoint, never a browser-supplied
	// business permission or object-fact capability.
	_, err = client.CheckAuthorization(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform, Id: id, Operation: "channel.channel.read"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	decision, err := client.CheckAuthorization(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform, Id: id, Operation: "GetRole"})
	require.NoError(t, err)
	require.True(t, decision.Decision.Allowed)
	sessions, err := client.ListUserSessions(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform, UserId: member.ID})
	require.NoError(t, err)
	require.NotEmpty(t, sessions.Sessions)
	var targetRevision uint64
	require.NoError(t, db.Table("users").Select("authorization_revision").Where("id = ?", member.ID).Scan(&targetRevision).Error)
	require.Equal(t, targetRevision, sessions.TargetRevision, "target CAS remains separate from actor authorization versions")
	// A bodyless DELETE resolves the role and boundary from the assignment row.
	published, err := client.SetRoleStatus(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform, Id: id, Role: &v.IAMRole{Status: "enabled"}, ExpectedRevision: reply.Roles[0].Revision, ExpectedPolicyRevision: reply.BasePolicyRevision, Reason: "enable", RequestId: "a6-enable"})
	require.NoError(t, err)
	assigned, err := client.AssignUserRole(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform, UserId: member.ID, Assignment: &v.IAMAssignment{UserId: member.ID, RoleId: id, Boundary: &c.AuthorizationScope{Clauses: []*c.AuthorizationScopeClause{{All: true}}}}, ExpectedPolicyRevision: published.BasePolicyRevision, Reason: "assign", RequestId: "a6-assign"})
	require.NoError(t, err)
	require.NotEmpty(t, assigned.Impacts)
	path := fmt.Sprintf("/api/v1/admin/iam/users/%d/roles/%d?expected_revision=%d&expected_policy_revision=%d&reason=revoke&request_id=a6-revoke", member.ID, assigned.Assignments[0].Id, assigned.Assignments[0].Revision, assigned.BasePolicyRevision)
	deleted := call("DELETE", path, "", rootRaw)
	require.Equal(t, 200, deleted.Code, deleted.Body.String())
	var revoked v.IAMReply
	require.NoError(t, protojson.Unmarshal(deleted.Body.Bytes(), &revoked))
	require.True(t, revoked.Assignments[0].Revoked)
	var target string
	require.NoError(t, db.Table("iam_audit_events").Select("target").Where("request_id = ? AND result = ?", "a6-revoke", "success").Scan(&target).Error)
	require.Equal(t, fmt.Sprint("role:", id), target)
	var userRevision, policyRevision uint64
	require.NoError(t, db.Table("users").Select("authorization_revision").Where("id = 1").Scan(&userRevision).Error)
	require.NoError(t, db.Table("iam_policy_state").Select("policy_revision").Where("id = 1").Scan(&policyRevision).Error)
	recovery := &v.IAMRescueRequest{UserId: 1, Password: "recovered-password", Reason: "isolated recovery acceptance", ExpectedRevision: userRevision, ExpectedPolicyRevision: policyRevision}
	_, err = client.RescueRootCredential(outgoing("a6-rescue-service", "a6-rescue"), recovery)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	t.Setenv("IAM_RESCUE_ENABLED", "true")
	_, err = client.RescueRootCredential(outgoing("a6-rescue-service", "a6-rescue"), recovery)
	require.NoError(t, err)
	_, err = client.GetSessionAuthorization(outgoing("a6-service", rootRaw), &v.IAMRequest{Context: platform})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

}
