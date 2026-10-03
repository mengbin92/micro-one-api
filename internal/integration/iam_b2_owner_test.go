package integration

import (
	"context"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	channeltest "micro-one-api/app/channel/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This uses real owner HTTP adapters, dedicated owner credentials, identity's
// gRPC snapshot/session implementation, and SQL repositories in one scratch DB.
func TestIAMB2RealAdminHTTPRoleMatrix(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "b2-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"b2-admin","channel":"b2-channel","relay":"b2-relay"}`)
	t.Setenv("JWT_SECRET_KEY", "b2-integration-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "b2-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, server, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	users := map[string]int64{"root": 1}
	for _, name := range []string{"audit", "groupchannelops", "platformops", "finance", "member"} {
		u, err := identity.Register(context.Background(), "b2-"+name, "password123", name+"@example.com", "default")
		require.NoError(t, err)
		users[name] = u.ID
	}
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "b2-http-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	all := `{"clauses":[{"all":true}]}`
	addRole := func(id int64, name string, grants map[string]string) {
		require.NoError(t, db.Table("iam_roles").Create(map[string]any{"id": id, "context_type": "platform", "organization_id": 0, "context_key": "platform", "code": "b2-" + name, "name": name, "description": "isolated HTTP acceptance role", "status": "enabled"}).Error)
		for op, scope := range grants {
			var permission int64
			require.NoError(t, db.Table("iam_permissions").Select("id").Where("code = ?", op).Scan(&permission).Error)
			require.Positive(t, permission)
			require.NoError(t, db.Table("iam_role_permissions").Create(map[string]any{"context_key": "platform", "role_id": id, "permission_id": permission, "effect": "allow", "scope_descriptor": scope}).Error)
		}
		require.NoError(t, db.Table("iam_user_roles").Create(map[string]any{"context_key": "platform", "user_id": users[name], "role_id": id, "allow_boundary": all, "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "status": "active", "origin": "explicit"}).Error)
	}
	groups := `{"clauses":[{"routing_group_ids":[10]}]}`
	groupResources := `{"clauses":[{"resource_ids":[10]}]}`
	base := map[string]string{"admin.console.enter": all, "channel.channel.list": groups, "channel.channel.read": groups, "channel.account.list": groups, "channel.account.read": groups, "channel.model.list": all, "channel.model.read": all, "monitor.health.model.read": groups, "channel.model_mapping.read": groups, "channel.routing_group.list": groupResources, "channel.routing_group.read": groupResources}
	addRole(51, "audit", base)
	ops := map[string]string{}
	for op, scope := range base {
		ops[op] = scope
	}
	for _, op := range []string{"channel.routing_group.members.read", "channel.routing_group.members.update", "channel.routing_group.resource_override.update", "channel.routing_group.update", "channel.routing_group.enable", "channel.routing_group.disable", "channel.routing_group.archive"} {
		ops[op] = groupResources
	}
	ops["channel.channel.update"] = groups
	addRole(52, "groupchannelops", ops)
	platform := map[string]string{}
	for op := range ops {
		platform[op] = all
	}
	platform["channel.channel.secret.read"] = all
	addRole(54, "platformops", platform)
	addRole(53, "finance", map[string]string{"admin.console.enter": all, "billing.account.read": all})
	token := func(uid int64, jti string) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": jti, "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("b2-integration-jwt"))
		require.NoError(t, err)
		return raw
	}
	tokens := map[string]string{}
	for name, uid := range users {
		raw := token(uid, "b2-"+name)
		tokens[name] = raw
		roleIDs := map[string]int64{"audit": 51, "groupchannelops": 52, "finance": 53, "platformops": 54}
		if id, ok := roleIDs[name]; ok {
			snap, err := identity.GetSessionAuthorization(context.Background(), raw, authorization.Platform())
			require.NoError(t, err)
			_, err = identity.ActivateSessionRoles(context.Background(), raw, authorization.Platform(), []int64{id}, snap.Session.Revision, "isolated role activation")
			require.NoError(t, err)
		}
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
	connection := func(owner string, target *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///b2-"+owner, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer b2-"+owner)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}

	channelListener := bufconn.Listen(1 << 20)
	channelServer := channeltest.NewIAMStack(db, authz.NewClient("channel", identityv1.NewIAMServiceClient(connection("channel", listener))))
	go func() { _ = channelServer.Serve(channelListener) }()
	t.Cleanup(func() { channelServer.Server.Stop(); _ = channelListener.Close() })
	adminConn := connection("admin", listener)
	admin := admintest.NewManagedChannelHTTP(identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), channelv1.NewChannelServiceClient(connection("admin", channelListener)))

	for _, g := range []struct {
		id  int64
		key string
	}{{10, "ops"}, {11, "hidden"}} {
		require.NoError(t, db.Table("routing_groups").Create(map[string]any{"id": g.id, "key": g.key, "display_name": g.key, "description": "", "status": "enabled", "access_mode": "restricted", "model_access_mode": "all", "revision": 1, "created_at": time.Now().Unix(), "updated_at": time.Now().Unix()}).Error)
	}
	for _, ch := range []struct {
		id    int64
		group string
	}{{100, "ops"}, {101, "hidden"}, {102, "ops,hidden"}} {
		require.NoError(t, db.Table("channels").Create(map[string]any{"id": ch.id, "type": 1, "name": fmt.Sprint("channel-", ch.id), "key": "private-channel-key", "group": ch.group, "models": "gpt-test", "status": 1}).Error)
		for _, group := range strings.Split(ch.group, ",") {
			id := 10
			if group == "hidden" {
				id = 11
			}
			require.NoError(t, db.Table("channel_routing_groups").Create(map[string]any{"channel_id": ch.id, "routing_group_id": id}).Error)
		}
		require.NoError(t, db.Table("model_health_states").Create(map[string]any{"id": ch.id, "source_kind": "channel", "source_id": ch.id, "model_id": "gpt-test", "upstream_model_id": "upstream-test", "status": "healthy", "last_checked_at": time.Now().Unix()}).Error)
	}
	require.NoError(t, db.Table("models").Create(map[string]any{"id": 301, "model_id": "gpt-test", "display_name": "Test Model", "status": 1, "pricing_input": 99, "pricing_output": 199, "created_at": time.Now().Unix(), "updated_at": time.Now().Unix()}).Error)
	require.NoError(t, db.Table("model_channel_mapping").Create(map[string]any{"id": 401, "channel_id": 100, "model_id": 301, "upstream_model_id": "upstream-test", "enabled": 1, "priority": 1}).Error)
	require.NoError(t, db.Table("model_channel_mapping").Create(map[string]any{"id": 402, "channel_id": 101, "model_id": 301, "upstream_model_id": "hidden-upstream", "enabled": 1, "priority": 1}).Error)
	call := func(method, path, body, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokens[role])
		req.Header.Set("x-authorization-reason", "real B2 HTTP acceptance")
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, req)
		return w
	}
	for _, role := range []string{"audit", "groupchannelops", "platformops", "root", "member", "finance"} {
		allowed := role != "member" && role != "finance"
		w := call("GET", "/v1/channels?page=1&page_size=20", "", role)
		expected := 403
		if allowed {
			expected = 200
		}
		require.Equal(t, expected, w.Code, role+" channel list: "+w.Body.String())
		if role == "audit" {
			require.NotContains(t, w.Body.String(), "private-channel-key")
			require.Contains(t, w.Body.String(), "channel-100")
			require.NotContains(t, w.Body.String(), "channel-101")
		}
		w = call("GET", "/api/admin/models?page=1&page_size=20", "", role)
		require.Equal(t, expected, w.Code, role+" model list: "+w.Body.String())
		if role == "audit" {
			require.NotContains(t, w.Body.String(), `"pricing_input":99`)
			require.NotContains(t, w.Body.String(), `"pricingInput":99`)
		}
	}
	require.Equal(t, 403, call("GET", "/api/channel/101", "", "audit").Code)
	w := call("GET", "/api/channel/100", "", "platformops")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "private-channel-key")
	w = call("GET", "/api/admin/model-health?page_size=20", "", "audit")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"total":"2"`)
	w = call("GET", "/api/admin/channels/100/models", "", "audit")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "upstream-test")
	w = call("GET", "/api/admin/channels/101/models", "", "audit")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "hidden-upstream", "out-of-scope mapping rows must be absent")
	w = call("GET", "/api/v1/admin/routing-groups/10", "", "audit")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"members_visible":false`)
	require.Equal(t, 403, call("POST", "/api/v1/admin/routing-groups/10/archive", `{"expected_revision":"1","reason":"denied audit write"}`, "audit").Code)
	// A group-scoped operator cannot remove a shared source because the old
	// source facts also belong to a second group outside its authority.
	replacement := `{"expected_revision":"1","reason":"replace complete set","members":[{"source_kind":"channel","source_id":"100"}]}`
	require.Equal(t, 403, call("PUT", "/api/v1/admin/routing-groups/10/members", replacement, "groupchannelops").Code)
	w = call("PUT", "/api/v1/admin/routing-groups/10/members", replacement, "platformops")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 409, call("PUT", "/api/v1/admin/routing-groups/10/members", replacement, "platformops").Code)
	var revision int64
	require.NoError(t, db.Table("routing_groups").Select("revision").Where("id=10").Scan(&revision).Error)
	override := fmt.Sprintf(`{"expected_revision":"%d","reason":"priority override","source_kind":"channel","source_id":100,"priority_override":12}`, revision)
	w = call("PUT", "/api/v1/admin/routing-groups/10/resource-overrides", override, "groupchannelops")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 409, call("PUT", "/api/v1/admin/routing-groups/10/resource-overrides", override, "groupchannelops").Code)
	require.NoError(t, db.Table("routing_groups").Select("revision").Where("id=10").Scan(&revision).Error)
	w = call("POST", "/api/v1/admin/routing-groups/10/archive", fmt.Sprintf(`{"expected_revision":"%d","reason":"retire group"}`, revision), "platformops")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "archived")
	// Role revocation and session revocation are checked on subsequent live RPCs.
	require.NoError(t, db.Table("iam_user_roles").Where("user_id = ? AND role_id = 51", users["audit"]).Update("status", "revoked").Error)
	require.Equal(t, 403, call("GET", "/v1/channels", "", "audit").Code)
	require.NoError(t, db.Table("iam_sessions").Where("session_id = ?", "b2-platformops").Update("revoked_at", time.Now().UnixMilli()).Error)
	require.Equal(t, 401, call("GET", "/v1/channels", "", "platformops").Code)
	var audits int64
	require.NoError(t, db.Table("resource_write_audits").Where("result = ?", "success").Count(&audits).Error)
	require.GreaterOrEqual(t, audits, int64(3))
}
