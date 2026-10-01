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
	v "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMB0B1OwnerAndUserBoundaries(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "legacy-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"admin-private","identity":"identity-private","relay":"relay-private","channel":"channel-private","billing":"billing-private","config":"config-private","log":"log-private","monitor":"monitor-private","notify":"notify-private"}`)
	t.Setenv("JWT_SECRET_KEY", "b1-integration-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "b1-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("ADMIN_TOKEN", "static-rescue")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, server, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "b1-member", "password123", "member@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "b1-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///b1", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	iamClient, userClient := v.NewIAMServiceClient(conn), v.NewIdentityServiceClient(conn)
	token := func(uid int64, jti string) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": jti, "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("b1-integration-jwt"))
		require.NoError(t, err)
		return raw
	}
	root, memberRaw := token(1, "b1-root"), token(member.ID, "b1-member")
	ctx := func(service, raw string) context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+service, "x-operator-authorization", "Bearer "+raw, "x-service-name", "identity", "x-operator-user-id", "1"))
	}
	for _, tt := range []struct {
		service, raw string
		code         codes.Code
	}{{"legacy-shared", root, codes.PermissionDenied}, {"relay-private", root, codes.PermissionDenied}, {"admin-private", "", codes.Unauthenticated}, {"admin-private", memberRaw, codes.PermissionDenied}, {"admin-private", root, codes.OK}} {
		_, err := userClient.ListUsers(ctx(tt.service, tt.raw), &v.ListUsersRequest{Page: 1, PageSize: 10})
		require.Equal(t, tt.code, status.Code(err))
	}
	request := &v.ResourceAuthorizationRequest{ExecutionPoint: "identity.users.read", Operation: "identity.user.read"}
	_, err = iamClient.GetResourceAuthorization(ctx("admin-private", root), request)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "admin cannot manufacture identity owner facts")
	_, err = iamClient.GetResourceAuthorization(ctx("identity-private", root), request)
	require.NoError(t, err)
	request.Operation = "billing.payment.read"
	_, err = iamClient.GetResourceAuthorization(ctx("identity-private", root), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	// Exercise the real owner-only policy RPC for completed B2-B4 slices.
	// These checks catch operation declarations that fake owner resolvers
	// could accept even though identity would reject them in production.
	for _, tc := range []struct{ owner, point, operation string }{
		{"channel", "channel.channels.update", "channel.channel.update"},
		{"channel", "channel.routing_groups.read", "channel.routing_group.members.read"},
		{"channel", "channel.routing_groups.write", "channel.routing_group.resource_override.update"},
		{"channel", "channel.routing_groups.write", "channel.routing_group.members.update"},
		{"billing", "billing.accounts.read", "billing.account.cost.read"},
		{"billing", "billing.payments.refund", "billing.payment.refund"},
		{"config", "system.options.update", "system.option.security.update"},
		{"log", "log.requests.read", "log.request.content.read"},
		{"monitor", "monitor.alert_rules", "monitor.alert_rule.update"},
		{"notify", "notify.notifications", "notify.notification.read"},
	} {
		probe := &v.ResourceAuthorizationRequest{ExecutionPoint: tc.point, Operation: tc.operation}
		reply, err := iamClient.GetResourceAuthorization(ctx(tc.owner+"-private", root), probe)
		require.NoError(t, err, tc.point+"/"+tc.operation)
		require.EqualValues(t, 1, reply.ActorUserId)
		require.NotEmpty(t, reply.Allow)
		_, err = iamClient.GetResourceAuthorization(ctx("admin-private", root), probe)
		require.Equal(t, codes.PermissionDenied, status.Code(err), "admin cannot impersonate "+tc.owner)
		_, err = iamClient.GetResourceAuthorization(ctx(tc.owner+"-private", memberRaw), probe)
		require.Equal(t, codes.PermissionDenied, status.Code(err), "numeric role does not grant "+tc.operation)
	}
	_, err = iamClient.GetResourceAuthorization(ctx("channel-private", root), &v.ResourceAuthorizationRequest{ExecutionPoint: "channel.routing_groups.write", Operation: "channel.routing_group.archive"})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "draft operations remain unavailable even to root")
	probe, err := iamClient.GetResourceAuthorization(ctx("billing-private", ""), &v.ResourceAuthorizationRequest{ExecutionPoint: "billing.self", ModeOnly: true})
	require.NoError(t, err)
	require.Equal(t, "iam", probe.AuthorizationMode)
	require.Zero(t, probe.ActorUserId)
	require.Empty(t, probe.Allow)
	require.Nil(t, probe.Decision)
	// Exercise the real admin guard, RPC adapter, identity service and owner tx.
	adminConn, err := grpc.NewClient("passthrough:///b1-admin", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		md = md.Copy()
		md.Set("authorization", "Bearer admin-private")
		return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminConn.Close() })
	admin := admintest.NewManagedUsersHTTP(v.NewIdentityServiceClient(adminConn), v.NewIAMServiceClient(adminConn))
	call := func(method, path, body, raw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/api/user/", "", root)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "usedAmount")
	require.NotContains(t, w.Body.String(), `"balance"`)
	require.Contains(t, w.Body.String(), `"authorizationRevision":"`)
	w = call("GET", "/api/user/", "", memberRaw)
	require.Equal(t, 403, w.Code, w.Body.String())
	w = call("GET", "/api/user/", "", "static-rescue")
	require.Equal(t, 401, w.Code, w.Body.String())
	w = call("GET", "/v1/channels", "", root)
	require.Equal(t, 403, w.Code, w.Body.String(), "unbound business paths must stay closed")
	var userRev, policyRev uint64
	require.NoError(t, db.Table("users").Select("authorization_revision").Where("id = ?", member.ID).Scan(&userRev).Error)
	require.NoError(t, db.Table("iam_policy_state").Select("policy_revision").Where("id = 1").Scan(&policyRev).Error)
	body := fmt.Sprintf(`{"userId":"%d","email":"","updateMask":"email","expectedRevision":"%d","expectedPolicyRevision":"%d","reason":"explicit email clear"}`, member.ID, userRev, policyRev)
	w = call("PUT", "/v1/users", body, root)
	require.Equal(t, 200, w.Code, w.Body.String())
	stored, err := identity.GetUser(context.Background(), member.ID)
	require.NoError(t, err)
	require.Empty(t, stored.Email)
	w = call("PUT", "/v1/users", body, root)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.NoError(t, db.Table("iam_policy_state").Select("policy_revision").Where("id = 1").Scan(&policyRev).Error)
	createBody := fmt.Sprintf(`{"username":"http-created","password":"http-created-password","expectedPolicyRevision":"%d","reason":"HTTP lifecycle acceptance"}`, policyRev)
	w = call("POST", "/api/user/", createBody, root)
	require.Equal(t, 200, w.Code, w.Body.String())
	var createdID int64
	require.NoError(t, db.Table("users").Select("id").Where("username = ?", "http-created").Scan(&createdID).Error)
	require.Positive(t, createdID)
	require.NoError(t, db.Table("users").Select("authorization_revision").Where("id = ?", createdID).Scan(&userRev).Error)
	require.NoError(t, db.Table("iam_policy_state").Select("policy_revision").Where("id = 1").Scan(&policyRev).Error)
	aliasBody := fmt.Sprintf(`{"id":"%d","display_name":"alias edit","update_mask":"displayName","expected_revision":"%d","expected_policy_revision":"%d","reason":"alias acceptance"}`, createdID, userRev, policyRev)
	w = call("PUT", "/api/user/", aliasBody, root)
	require.Equal(t, 200, w.Code, w.Body.String())
	aliasUser, err := identity.GetUser(context.Background(), createdID)
	require.NoError(t, err)
	require.Equal(t, "alias edit", aliasUser.DisplayName)
	w = call("PUT", "/api/user/", aliasBody, root)
	require.Equal(t, 409, w.Code, w.Body.String())
	// Unknown/sensitive compatibility fields cannot ride a profile write.
	w = call("PUT", "/api/user/", strings.TrimSuffix(aliasBody, "}")+`,"role":100}`, root)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.NoError(t, db.Table("users").Select("authorization_revision").Where("id = ?", createdID).Scan(&userRev).Error)
	require.NoError(t, db.Table("iam_policy_state").Select("policy_revision").Where("id = 1").Scan(&policyRev).Error)
	deletion := fmt.Sprintf("/api/user/%d?expected_revision=%d&expected_policy_revision=%d&reason=HTTP-delete", createdID, userRev, policyRev)
	w = call("DELETE", deletion, "", root)
	require.Equal(t, 200, w.Code, w.Body.String())
	_, err = identity.GetUser(context.Background(), createdID)
	require.Error(t, err)
	w = call("GET", "/api/not-registered", "", root)
	require.Equal(t, 404, w.Code)
}
