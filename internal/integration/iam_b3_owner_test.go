package integration

import (
	"context"
	"encoding/csv"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	billingtest "micro-one-api/app/billing/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMB3RealAdminHTTPRoleMatrix(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "b3-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"b3-admin","billing":"b3-billing","relay":"b3-relay"}`)
	t.Setenv("JWT_SECRET_KEY", "b3-integration-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "b3-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, server, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	users := map[string]int64{"root": 1}
	for _, name := range []string{"audit", "channelops", "platformops", "finance", "member"} {
		u, err := identity.Register(context.Background(), "b3-"+name, "password123", name+"@example.com", "default")
		require.NoError(t, err)
		users[name] = u.ID
	}
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "b3-http-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	all := `{"clauses":[{"all":true}]}`
	usersScope := fmt.Sprintf(`{"clauses":[{"user_ids":[%d]}]}`, users["member"])
	grants := map[string]map[string]string{
		"audit":       {"admin.console.enter": all, "billing.account.read": usersScope, "billing.account.ledger.read": usersScope, "billing.report.export": usersScope},
		"finance":     {"admin.console.enter": all, "billing.account.read": usersScope, "billing.account.ledger.read": usersScope, "billing.account.cost.read": usersScope, "billing.report.export": usersScope, "billing.account.balance.reset": usersScope},
		"channelops":  {"admin.console.enter": all, "channel.channel.list": all},
		"platformops": {"admin.console.enter": all, "channel.channel.list": all},
	}
	roleIDs := map[string]int64{"audit": 51, "channelops": 52, "finance": 53, "platformops": 54}
	for name, operations := range grants {
		id := roleIDs[name]
		require.NoError(t, db.Table("iam_roles").Create(map[string]any{"id": id, "context_type": "platform", "organization_id": 0, "context_key": "platform", "code": "b3-" + name, "name": name, "description": "isolated HTTP acceptance role", "status": "enabled"}).Error)
		for operation, scope := range operations {
			var permission int64
			require.NoError(t, db.Table("iam_permissions").Select("id").Where("code = ?", operation).Scan(&permission).Error)
			require.Positive(t, permission)
			require.NoError(t, db.Table("iam_role_permissions").Create(map[string]any{"context_key": "platform", "role_id": id, "permission_id": permission, "effect": "allow", "scope_descriptor": scope}).Error)
		}
		require.NoError(t, db.Table("iam_user_roles").Create(map[string]any{"context_key": "platform", "user_id": users[name], "role_id": id, "allow_boundary": all, "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "status": "active", "origin": "explicit"}).Error)
	}
	tokens := map[string]string{}
	for name, uid := range users {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": "b3-" + name, "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("b3-integration-jwt"))
		require.NoError(t, err)
		tokens[name] = raw
		if id, ok := roleIDs[name]; ok {
			snapshot, err := identity.GetSessionAuthorization(context.Background(), raw, authorization.Platform())
			require.NoError(t, err)
			_, err = identity.ActivateSessionRoles(context.Background(), raw, authorization.Platform(), []int64{id}, snapshot.Session.Revision, "isolated role activation")
			require.NoError(t, err)
		}
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
	connection := func(owner string, target *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///b3-"+owner, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer b3-"+owner)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	billingListener := bufconn.Listen(1 << 20)
	billingServer := billingtest.NewIAMStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connection("billing", listener))))
	go func() { _ = billingServer.Serve(billingListener) }()
	t.Cleanup(func() { billingServer.Server.Stop(); _ = billingListener.Close() })
	adminConn := connection("admin", listener)
	admin := admintest.NewManagedBillingHTTP(identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), billingv1.NewBillingServiceClient(connection("admin", billingListener)))
	for _, uid := range []int64{users["member"], users["channelops"]} {
		require.NoError(t, db.Table("users").Where("id = ?", uid).Update("balance", 100).Error)
		require.NoError(t, db.Table("billing_ledgers").Create(map[string]any{"user_id": fmt.Sprint(uid), "amount": -100, "balance_after": 0, "quota": 100, "upstream_cost": 30, "type": "consume", "reference_id": fmt.Sprint("acceptance-", uid), "ledger_dedupe_key": fmt.Sprint("b3-seed-", uid), "created_at": time.Now()}).Error)
	}
	call := func(method, path, body, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokens[role])
		req.Header.Set("x-authorization-reason", "real B3 HTTP acceptance")
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, req)
		return w
	}
	for _, role := range []string{"audit", "finance", "channelops", "platformops", "member", "root"} {
		expected := 403
		if role == "audit" || role == "finance" || role == "root" {
			expected = 200
		}
		w := call("GET", fmt.Sprintf("/v1/account?user_id=%d", users["member"]), "", role)
		require.Equal(t, expected, w.Code, role+" account: "+w.Body.String())
		w = call("GET", "/api/v1/admin/reports/cost:export?group_by=user&include_costs=false", "", role)
		require.Equal(t, expected, w.Code, role+" report: "+w.Body.String())
		if role == "audit" || role == "finance" {
			rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
			require.NoError(t, err)
			require.Len(t, rows, 2, "scope must apply before report aggregation")
			require.Equal(t, fmt.Sprint(users["member"]), rows[1][0])
			require.NotContains(t, rows[0], "upstream_cost")
		}
	}
	w := call("GET", "/api/v1/admin/reports/cost:export?group_by=user", "", "audit")
	require.Equal(t, 403, w.Code, w.Body.String())
	w = call("GET", "/api/v1/admin/reports/cost:export?group_by=user", "", "finance")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "upstream_cost")
	path := fmt.Sprintf("/api/v1/admin/accounts/%d/balance:reset", users["member"])
	body := `{"target_balance":300,"expected_balance":100,"reason":"reviewed correction","request_id":"b3-reset-one"}`
	require.Equal(t, 403, call("POST", path, body, "audit").Code)
	w = call("POST", path, body, "finance")
	require.Equal(t, 200, w.Code, w.Body.String())
	body = `{"target_balance":400,"expected_balance":100,"reason":"stale correction","request_id":"b3-reset-two"}`
	w = call("POST", path, body, "finance")
	require.Equal(t, 409, w.Code, w.Body.String())
	var balance int64
	require.NoError(t, db.Table("users").Select("balance").Where("id = ?", users["member"]).Scan(&balance).Error)
	require.EqualValues(t, 300, balance)
	require.NoError(t, db.Table("iam_user_roles").Where("user_id = ? AND role_id = 53", users["finance"]).Update("status", "revoked").Error)
	require.Equal(t, 403, call("POST", path, body, "finance").Code)
}
