package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	billingtest "micro-one-api/app/billing/testutil"
	channeltest "micro-one-api/app/channel/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

// Opt-in browser test, with actual HTTP -> gRPC -> biz -> SQL. No production
// DSN, fake authorization responses or browser-side network fulfilment.
func TestIAMCRealBrowserMatrix(t *testing.T) {
	if os.Getenv("IAM_C_PLAYWRIGHT") != "1" {
		t.Skip("opt in with IAM_C_PLAYWRIGHT=1; requires local Chrome and web dependencies")
	}
	t.Setenv("SERVICE_TOKEN", "c-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"c-admin","channel":"c-channel","billing":"c-billing"}`)
	t.Setenv("JWT_SECRET_KEY", "c-isolated-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "c-isolated-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, selfHTTP := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	users := map[string]int64{"root": 1}
	for _, name := range []string{"alice", "bob", "finance", "auditor", "member", "selection"} {
		user, err := identity.Register(context.Background(), name, "password123", name+"@example.com", "default")
		require.NoError(t, err)
		users[name] = user.ID
	}
	require.NoError(t, db.Table("iam_policy_state").Where("id=1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "c-browser-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status=?", "draft").Update("status", "enabled").Error)
	all := `{"clauses":[{"all":true}]}`
	groups := `{"clauses":[{"routing_group_ids":[11]}]}`
	finances := fmt.Sprintf(`{"clauses":[{"user_ids":[%d,%d]}]}`, users["member"], users["selection"])
	addRole := func(id int64, code string, grants map[string]string) {
		require.NoError(t, db.Table("iam_roles").Create(map[string]any{"id": id, "context_type": "platform", "organization_id": 0, "context_key": "platform", "code": code, "name": code, "description": "C isolated browser acceptance", "status": "enabled"}).Error)
		for operation, scope := range grants {
			var permission int64
			require.NoError(t, db.Table("iam_permissions").Select("id").Where("code=?", operation).Scan(&permission).Error)
			require.Positive(t, permission)
			require.NoError(t, db.Table("iam_role_permissions").Create(map[string]any{"context_key": "platform", "role_id": id, "permission_id": permission, "effect": "allow", "scope_descriptor": scope}).Error)
		}
	}
	assign := func(name string, role int64) {
		require.NoError(t, db.Table("iam_user_roles").Create(map[string]any{"context_key": "platform", "user_id": users[name], "role_id": role, "allow_boundary": all, "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "status": "active", "origin": "explicit"}).Error)
	}
	addRole(51, "channel_reader", map[string]string{"admin.console.enter": all, "channel.channel.list": groups, "channel.channel.read": groups})
	addRole(52, "resource_operator_east", map[string]string{"channel.channel.test": groups, "channel.channel.enable": groups, "channel.channel.disable": groups, "channel.channel.secret.rotate": `{"clauses":[{"resource_ids":[7]}]}`})
	require.NoError(t, db.Table("iam_role_inheritance").Create(map[string]any{"context_key": "platform", "senior_role_id": 52, "junior_role_id": 51}).Error)
	addRole(53, "finance_reader", map[string]string{"admin.console.enter": all, "billing.payment.list": finances, "billing.payment.read": finances, "billing.account.ledger.read": finances, "billing.account.cost.read": finances})
	addRole(54, "bob_manager", map[string]string{"admin.console.enter": all, "identity.user_role.read": all, "identity.user_role.assign": all, "identity.user_role.revoke": all, "iam.role.list": all, "iam.role.read": all, "iam.authorization.simulate": all})
	addRole(55, "auditor", map[string]string{"admin.console.enter": all, "iam.audit.read": all})
	assign("alice", 52)
	assign("alice", 53)
	assign("finance", 53)
	assign("bob", 54)
	assign("auditor", 55)
	assign("selection", 55)
	assign("selection", 53)
	// A single delegation covers the target user's whole resulting maximum,
	// including the existing member role; unrelated delegation paths cannot pool.
	var ceilingRows []struct{ Code, ScopeDescriptor string }
	require.NoError(t, db.Table("iam_role_permissions AS rp").Select("p.code,rp.scope_descriptor").Joins("JOIN iam_permissions p ON p.id=rp.permission_id").Joins("JOIN iam_roles r ON r.id=rp.role_id").Where("r.code=? OR r.id=?", "member", 53).Scan(&ceilingRows).Error)
	ceiling := []map[string]any{}
	for _, entry := range ceilingRows {
		var scope map[string]any
		require.NoError(t, jsonx.Unmarshal([]byte(entry.ScopeDescriptor), &scope))
		ceiling = append(ceiling, map[string]any{"context": authorization.Platform(), "operation": entry.Code, "scope": scope})
	}
	ceilingJSON, err := jsonx.Marshal(ceiling)
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_role_constraints").Create(map[string]any{"id": 91, "context_key": "platform", "name": "C finance-audit DSD", "type": "DSD", "max_count": 1, "enabled": true}).Error)
	for _, roleID := range []int64{53, 55} {
		require.NoError(t, db.Table("iam_role_constraint_members").Create(map[string]any{"context_key": "platform", "constraint_id": 91, "role_id": roleID}).Error)
	}
	actions, _ := jsonx.Marshal([]string{"iam.role.read", "identity.user_role.read", "identity.user_role.assign", "identity.user_role.revoke"})
	require.NoError(t, db.Table("iam_delegations").Create(map[string]any{"context_key": "platform", "manager_role_id": 54, "target_role_id": 53, "target_kind": "role", "actions": string(actions), "target_user_scope": finances, "grant_ceiling": string(ceilingJSON), "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "can_redelegate": 0}).Error)
	tokens := map[string]string{}
	for name, uid := range users {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": "c-" + name, "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("c-isolated-jwt"))
		require.NoError(t, err)
		tokens[name] = raw
		snapshot, err := identity.GetSessionAuthorization(context.Background(), raw, authorization.Platform())
		require.NoError(t, err)
		active := map[string][]int64{"alice": {52, 53}, "bob": {54}, "finance": {53}, "auditor": {55}}
		if ids, ok := active[name]; ok {
			_, err = identity.ActivateSessionRoles(context.Background(), raw, authorization.Platform(), ids, snapshot.Session.Revision, "C browser setup")
			require.NoError(t, err)
		}
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = identityServer.Serve(listener) }()
	t.Cleanup(func() { identityServer.Server.Stop(); _ = listener.Close() })
	connection := func(owner string, target *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///c-"+owner, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer c-"+owner)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	chListener := bufconn.Listen(1 << 20)
	channelServer := channeltest.NewIAMStack(db, authz.NewClient("channel", identityv1.NewIAMServiceClient(connection("channel", listener))))
	go func() { _ = channelServer.Serve(chListener) }()
	t.Cleanup(func() { channelServer.Server.Stop(); _ = chListener.Close() })
	billListener := bufconn.Listen(1 << 20)
	billingServer := billingtest.NewIAMStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connection("billing", listener))))
	go func() { _ = billingServer.Serve(billListener) }()
	t.Cleanup(func() { billingServer.Server.Stop(); _ = billListener.Close() })
	adminConn := connection("admin", listener)
	admin := admintest.NewManagedResourcesHTTP(identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), channelv1.NewChannelServiceClient(connection("admin", chListener)), billingv1.NewBillingServiceClient(connection("admin", billListener)))
	for _, group := range []struct {
		id  int64
		key string
	}{{11, "east"}, {12, "west"}} {
		require.NoError(t, db.Table("routing_groups").Create(map[string]any{"id": group.id, "key": group.key, "display_name": group.key, "description": "", "status": "enabled", "access_mode": "restricted", "model_access_mode": "all", "revision": 1, "created_at": time.Now().Unix(), "updated_at": time.Now().Unix()}).Error)
	}
	for _, ch := range []struct {
		id    int64
		group string
	}{{7, "east"}, {8, "west"}, {9, "east,west"}} {
		require.NoError(t, db.Table("channels").Create(map[string]any{"id": ch.id, "type": 1, "name": fmt.Sprintf("channel-%d", ch.id), "key": "c-private-key", "group": ch.group, "models": "gpt-test", "status": 1}).Error)
		for _, key := range strings.Split(ch.group, ",") {
			id := 11
			if key == "west" {
				id = 12
			}
			require.NoError(t, db.Table("channel_routing_groups").Create(map[string]any{"channel_id": ch.id, "routing_group_id": id}).Error)
		}
	}
	for _, uid := range []int64{users["member"], users["bob"]} {
		require.NoError(t, db.Table("payment_orders").Create(map[string]any{"user_id": uid, "trade_no": fmt.Sprintf("c-order-%d", uid), "money_cents": 100, "channel": "epay", "asset_type": "balance", "asset_amount": 100, "status": "paid", "created_at": time.Now()}).Error)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/user/authorization") || strings.HasPrefix(r.URL.Path, "/api/user/session") || r.URL.Path == "/api/user/self" || r.URL.Path == "/api/user/dashboard" {
			selfHTTP.ServeHTTP(w, r)
		} else {
			admin.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(httpServer.Close)
	manifest, err := jsonx.Marshal(map[string]any{"users": users, "tokens": tokens})
	require.NoError(t, err)
	manifestPath := filepath.Join(t.TempDir(), "browser-fixture.json")
	require.NoError(t, os.WriteFile(manifestPath, manifest, 0600))
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "npm", "exec", "playwright", "test", "--", "--config=playwright.rbac.config.ts")
	command.Dir = filepath.Join(root, "web")
	command.Env = append(os.Environ(), "RBAC_C_BACKEND="+httpServer.URL, "RBAC_C_FIXTURE="+manifestPath)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	require.NoError(t, err, "real browser matrix")
}
