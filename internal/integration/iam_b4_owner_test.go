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
	identityv1 "micro-one-api/api/identity/v1"
	configtest "micro-one-api/app/config/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	logtest "micro-one-api/app/log/testutil"
	monitortest "micro-one-api/app/monitor/testutil"
	notifytest "micro-one-api/app/notify/testutil"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This uses real owner HTTP adapters, dedicated owner credentials, identity's
// gRPC snapshot/session implementation, and SQL repositories in one scratch DB.
func TestIAMB4RealOwnerHTTPRoleMatrix(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "b4-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"b4-admin","log":"b4-log","notify":"b4-notify","monitor":"b4-monitor","config":"b4-config","relay":"b4-relay"}`)
	t.Setenv("JWT_SECRET_KEY", "b4-integration-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "b4-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, server, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	users := map[string]int64{"root": 1}
	for _, name := range []string{"audit", "notification_ops", "finance", "member"} {
		u, err := identity.Register(context.Background(), "b4-"+name, "password123", name+"@example.com", "default")
		require.NoError(t, err)
		users[name] = u.ID
	}
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "b4-http-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	all := `{"clauses":[{"all":true}]}`
	addRole := func(id int64, name string, grants map[string]string) {
		require.NoError(t, db.Table("iam_roles").Create(map[string]any{"id": id, "context_type": "platform", "organization_id": 0, "context_key": "platform", "code": "b4-" + name, "name": name, "description": "isolated HTTP acceptance role", "status": "enabled"}).Error)
		for op, scope := range grants {
			var permission int64
			require.NoError(t, db.Table("iam_permissions").Select("id").Where("code = ?", op).Scan(&permission).Error)
			require.Positive(t, permission)
			require.NoError(t, db.Table("iam_role_permissions").Create(map[string]any{"context_key": "platform", "role_id": id, "permission_id": permission, "effect": "allow", "scope_descriptor": scope}).Error)
		}
		require.NoError(t, db.Table("iam_user_roles").Create(map[string]any{"context_key": "platform", "user_id": users[name], "role_id": id, "allow_boundary": all, "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "status": "active", "origin": "explicit"}).Error)
	}
	logScope := `{"clauses":[{"resource_ids":[100]}]}`
	addRole(51, "audit", map[string]string{"log.request.list": logScope, "log.request.read": logScope, "log.request.export": logScope, "notify.notification.list": `{"clauses":[{"resource_ids":[501]}]}`, "notify.notification.read": `{"clauses":[{"resource_ids":[501]}]}`, "monitor.health.service.read": `{"clauses":[{"resource_ids":[200]}]}`})
	addRole(52, "notification_ops", map[string]string{"notify.notification.rules.update": all, "notify.notification.test": `{"clauses":[{"resource_ids":[41]}]}`, "notify.notification.acknowledge": `{"clauses":[{"resource_ids":[501]}]}`, "system.option.update": all, "system.content.notice.update": all})
	addRole(53, "finance", map[string]string{"billing.account.read": all})
	token := func(uid int64, jti string) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": jti, "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("b4-integration-jwt"))
		require.NoError(t, err)
		return raw
	}
	tokens := map[string]string{}
	for name, uid := range users {
		raw := token(uid, "b4-"+name)
		tokens[name] = raw
		if name == "audit" || name == "notification_ops" || name == "finance" {
			snap, err := identity.GetSessionAuthorization(context.Background(), raw, authorization.Platform())
			require.NoError(t, err)
			id := int64(51)
			if name == "notification_ops" {
				id = 52
			}
			if name == "finance" {
				id = 53
			}
			_, err = identity.ActivateSessionRoles(context.Background(), raw, authorization.Platform(), []int64{id}, snap.Session.Revision, "isolated role activation")
			require.NoError(t, err)
		}
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
	changeModeOnProbe := false
	ownerClient := func(owner string) *authz.Client {
		conn, err := grpc.NewClient("passthrough:///b4-"+owner, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			if probe, ok := req.(*identityv1.ResourceAuthorizationRequest); ok && probe.ModeOnly && owner == "log" && changeModeOnProbe {
				require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "legacy", "cutover_state": "idle", "cutover_batch_id": "", "cutover_verified_at": nil}).Error)
			}
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer b4-"+owner)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return authz.NewClient(owner, identityv1.NewIAMServiceClient(conn))
	}
	logs := logtest.NewIAMHTTP(db, ownerClient("log"))
	notifications := notifytest.NewIAMHTTP(db, ownerClient("notify"))
	health := monitortest.NewIAMHTTP(db, ownerClient("monitor"))
	configs := configtest.NewIAMHTTP(db, ownerClient("config"))
	for _, id := range []int64{100, 101} {
		require.NoError(t, db.Table("logs").Create(map[string]any{"id": id, "user_id": users["member"], "message": "private request body", "level": "info", "source": "relay", "created_at": time.Now().Add(-time.Hour).Unix()}).Error)
	}
	for _, id := range []int64{501, 502} {
		require.NoError(t, db.Table("notifications").Create(map[string]any{"id": id, "type": "event", "recipient": "", "subject": "alert", "content": "alert body", "status": "pending", "created_at": time.Now().Unix()}).Error)
	}
	require.NoError(t, db.Table("health_checks").Create(map[string]any{"id": 200, "service_name": "relay", "status": "healthy", "response_time": 1, "checked_at": time.Now().Unix()}).Error)
	call := func(h http.Handler, method, path, body, raw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+raw)
		r.Header.Set("x-authorization-reason", "HTTP role acceptance")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, name := range []string{"audit", "finance", "member", "root"} {
		w := call(logs, http.MethodGet, "/v1/logs/export", "", tokens[name])
		expected := 403
		if name == "audit" || name == "root" {
			expected = 200
		}
		require.Equal(t, expected, w.Code, name+" export: "+w.Body.String())
		if name == "audit" {
			var out struct {
				Total string           `json:"total"`
				Items []map[string]any `json:"items"`
			}
			require.NoError(t, jsonx.Unmarshal(w.Body.Bytes(), &out))
			require.Equal(t, "1", out.Total)
			require.Len(t, out.Items, 1)
			require.NotContains(t, w.Body.String(), "private request body")
		}
	}
	require.Equal(t, 403, call(logs, http.MethodGet, "/v1/logs/101", "", tokens["audit"]).Code)
	require.Equal(t, 403, call(logs, http.MethodDelete, fmt.Sprintf("/v1/logs?end_time=%d", time.Now().Unix()), "", tokens["audit"]).Code, "HTTP delete preserves owner authorization status")
	require.Equal(t, 403, call(logs, http.MethodPost, "/v1/logs/purge", fmt.Sprintf(`{"before":%d,"reason":"denied purge"}`, time.Now().Unix()), tokens["audit"]).Code)
	require.Equal(t, 200, call(health, http.MethodGet, "/v1/health-checks/latest?service_name=relay", "", tokens["audit"]).Code)
	require.Equal(t, 403, call(health, http.MethodPost, "/v1/health-checks", `{"service_name":"forged","status":"healthy"}`, tokens["root"]).Code)
	require.Equal(t, 200, call(notifications, http.MethodGet, "/v1/notifications/501", "", tokens["audit"]).Code)
	require.Equal(t, 403, call(notifications, http.MethodGet, "/v1/notifications/502", "", tokens["audit"]).Code)
	require.Equal(t, 403, call(notifications, http.MethodPost, "/v1/notifications/501/acknowledge", `{"expected_revision":"1","reason":"no write"}`, tokens["audit"]).Code)
	ruleBody := `{"rule":{"name":"alerts","event":"alertmanager","type":"event","enabled":true,"revision":"0"},"reason":"install event route"}`
	w := call(notifications, http.MethodPut, "/v1/notification-rules/41", ruleBody, tokens["notification_ops"])
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"revision":"1"`)
	w = call(notifications, http.MethodPost, "/v1/notification-rules/41/test", `{"reason":"probe routing"}`, tokens["notification_ops"])
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 403, call(notifications, http.MethodPost, "/v1/notification-rules/42/test", `{"reason":"hidden rule"}`, tokens["notification_ops"]).Code)
	w = call(notifications, http.MethodPost, "/v1/notifications/501/acknowledge", `{"expected_revision":"1","reason":"operator reviewed"}`, tokens["notification_ops"])
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"pending"`)
	require.Equal(t, 409, call(notifications, http.MethodPost, "/v1/notifications/501/acknowledge", `{"expected_revision":"1","reason":"stale ack"}`, tokens["notification_ops"]).Code)
	_, probeErr := ownerClient("notify").Query(context.Background(), "notify.notifications", "notify.notification.read", tokens["root"])
	require.NoError(t, probeErr)
	require.Equal(t, 403, call(notifications, http.MethodPost, "/v1/notifications", `{"type":"event","subject":"not send capability"}`, tokens["root"]).Code)
	w = call(configs, http.MethodPut, "/v1/configs/system/notice", `{"value":"public operational notice"}`, tokens["notification_ops"])
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 403, call(configs, http.MethodPut, "/v1/configs/system/StripeSecret", `{"value":"must remain denied"}`, tokens["notification_ops"]).Code)
	require.Equal(t, 401, call(logs, http.MethodGet, "/v1/logs/export", "", "b4-shared").Code)
	require.Equal(t, 401, call(logs, http.MethodGet, "/v1/logs/export", "", "b4-admin").Code, "dedicated caller still needs operator")
	require.Equal(t, 403, call(notifications, http.MethodPut, "/v1/notifications/501/status", `{"status":"sent"}`, tokens["root"]).Code, "user read/ack never changes worker delivery state")
	require.Equal(t, 200, call(notifications, http.MethodPut, "/v1/notifications/501/status", `{"status":"sent"}`, "b4-notify").Code)
	require.Equal(t, 404, call(notifications, http.MethodPut, "/v1/notifications/999999/status", `{"status":"sent"}`, "b4-notify").Code, "missing worker target is not a successful delivery mutation")
	require.Equal(t, 403, call(notifications, http.MethodPost, "/v1/notifications/501/acknowledge", `{"expected_revision":"3","reason":"forged operator"}`, "b4-notify").Code)
	require.Equal(t, 201, call(health, http.MethodPost, "/v1/health-checks", `{"service_name":"monitor-self","status":"healthy"}`, "b4-monitor").Code)
	require.Equal(t, 403, call(health, http.MethodPost, "/v1/health-checks", `{"service_name":"forged","status":"healthy"}`, "b4-notify").Code)
	require.Equal(t, 201, call(logs, http.MethodPost, "/v1/logs", `{"message":"worker ingest","level":"info","source":"relay"}`, "b4-relay").Code)
	require.Equal(t, 403, call(logs, http.MethodPost, "/v1/logs", `{"message":"forged intake","level":"info"}`, tokens["root"]).Code)
	var audits int64
	require.NoError(t, db.Table("resource_write_audits").Where("result = ?", "success").Count(&audits).Error)
	require.GreaterOrEqual(t, audits, int64(4))
	// A denied optional field keeps the real verified actor but never gains a grant.
	optionalClient := ownerClient("log")
	optional, err := optionalClient.OptionalQuery(context.Background(), "log.requests.read", "log.request.content.read", tokens["audit"])
	require.NoError(t, err)
	require.Equal(t, users["audit"], optional.Query.ActorID)
	require.Empty(t, optional.Query.Allow)
	require.False(t, optional.Query.ValidUntil.IsZero())
	require.NoError(t, db.Table("iam_user_roles").Where("user_id = ? AND role_id = ?", users["audit"], 51).Update("status", "revoked").Error)
	require.Equal(t, 403, call(logs, http.MethodGet, "/v1/logs/export", "", tokens["audit"]).Code, "role revocation stops the next owner decision")
	require.NoError(t, db.Table("iam_user_roles").Where("user_id = ? AND role_id = ?", users["audit"], 51).Update("status", "active").Error)
	changeModeOnProbe = true
	_, err = optionalClient.OptionalQuery(context.Background(), "log.requests.read", "log.request.content.read", tokens["audit"])
	require.Error(t, err, "mode changes during optional reauthentication fail closed")
	changeModeOnProbe = false
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "b4-http-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_sessions").Where("session_id = ?", "b4-audit").Update("revoked_at", time.Now().UnixMilli()).Error)
	_, err = optionalClient.OptionalQuery(context.Background(), "log.requests.read", "log.request.content.read", tokens["audit"])
	require.Error(t, err, "revoked session must not be hidden by optional field redaction")
	// Session revocation independently invalidates the same user JWT.
	require.Equal(t, 401, call(logs, http.MethodGet, "/v1/logs/export", "", tokens["audit"]).Code)
}
