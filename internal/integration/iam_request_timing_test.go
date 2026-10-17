package integration

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	admintest "micro-one-api/app/admin/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	logtest "micro-one-api/app/log/testutil"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestRequestTimingRealIAMAdminToOwnerWithoutBillingOrContentPermission(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "timing-legacy")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"timing-admin","log":"timing-log","relay":"timing-relay","identity":"timing-identity","channel":"timing-channel","billing":"timing-billing","config":"timing-config","monitor":"timing-monitor","notify":"timing-notify"}`)
	t.Setenv("JWT_SECRET_KEY", "timing-isolated-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "timing-isolated-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	auditor, err := identity.Register(context.Background(), "timing-auditor", "password123", "timing@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "timing-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	all := `{"clauses":[{"all":true}]}`
	own := fmt.Sprintf(`{"clauses":[{"user_ids":[%d]}]}`, auditor.ID)
	require.NoError(t, db.Table("iam_roles").Create(map[string]any{"id": 6117, "context_type": "platform", "organization_id": 0, "context_key": "platform", "code": "timing-auditor", "name": "timing auditor", "description": "isolated timing permission", "status": "enabled"}).Error)
	for operation, scope := range map[string]string{"admin.console.enter": all, "log.request.list": own, "log.request.read": own} {
		var permission int64
		require.NoError(t, db.Table("iam_permissions").Select("id").Where("code = ?", operation).Scan(&permission).Error)
		require.Positive(t, permission)
		require.NoError(t, db.Table("iam_role_permissions").Create(map[string]any{"context_key": "platform", "role_id": 6117, "permission_id": permission, "effect": "allow", "scope_descriptor": scope}).Error)
	}
	require.NoError(t, db.Table("iam_user_roles").Create(map[string]any{"context_key": "platform", "user_id": auditor.ID, "role_id": 6117, "allow_boundary": all, "starts_at": time.Now().Add(-time.Hour).UnixMilli(), "status": "active", "origin": "explicit"}).Error)
	token := func(id int64) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": id, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": fmt.Sprintf("timing-%d", id), "sub": fmt.Sprint(id), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("timing-isolated-jwt"))
		require.NoError(t, err)
		return raw
	}
	userToken := token(auditor.ID)
	session, err := identity.GetSessionAuthorization(context.Background(), userToken, authorization.Platform())
	require.NoError(t, err)
	_, err = identity.ActivateSessionRoles(context.Background(), userToken, authorization.Platform(), []int64{6117}, session.Session.Revision, "isolated timing role")
	require.NoError(t, err)
	identityListener := bufconn.Listen(1 << 20)
	go func() { _ = identityServer.Serve(identityListener) }()
	t.Cleanup(func() { identityServer.Server.Stop(); _ = identityListener.Close() })
	connection := func(caller string, target *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///timing-iam", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer timing-"+caller)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, options...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	ownerConnection := connection("log", identityListener)
	logServer := logtest.NewIAMGRPC(db, authz.NewClient("log", identityv1.NewIAMServiceClient(ownerConnection)))
	logListener := bufconn.Listen(1 << 20)
	go func() { _ = logServer.Serve(logListener) }()
	t.Cleanup(func() { logServer.Server.Stop(); _ = logListener.Close() })
	producer := logv1.NewLogServiceClient(connection("relay", logListener))
	ids := map[int64]string{}
	for _, owner := range []int64{auditor.ID, 1, 0} {
		id := uuid.NewString()
		ids[owner] = id
		_, err := producer.IngestLog(context.Background(), &logv1.IngestLogRequest{Source: requesttrace.TimingSource, UserId: owner, RequestTiming: &logv1.RequestTimingSummary{Version: 1, ObservationId: id, ObservationKind: "http", Protocol: "http", Endpoint: "/v1/responses", ExecutionPath: "raw", UserId: owner, StartedAtMs: time.Now().UnixMilli(), Outcome: "rejected", CollectionState: "not_recorded"}})
		require.NoError(t, err)
	}
	_, err = producer.ListRequestTraces(context.Background(), &logv1.ListRequestTracesRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	adminIdentity := connection("admin", identityListener)
	adminLogs := logv1.NewLogServiceClient(connection("admin", logListener))
	admin := admintest.NewManagedResourcesHTTP(identityv1.NewIdentityServiceClient(adminIdentity), identityv1.NewIAMServiceClient(adminIdentity), nil, nil, adminLogs)
	call := func(path, raw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	w := call("/api/log/request-traces", userToken)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), ids[auditor.ID])
	require.NotContains(t, w.Body.String(), ids[1])
	require.NotContains(t, w.Body.String(), ids[0])
	w = call("/api/log/request-traces/"+ids[auditor.ID], userToken)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "not_recorded")
	w = call("/api/log/request-traces/"+ids[1], userToken)
	require.NotEqual(t, 200, w.Code)
	w = call("/api/log/request-traces?user_id=0", token(1))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), ids[0])
	w = call("/api/log/request-traces?outcome=unbounded", userToken)
	require.Equal(t, 400, w.Code, w.Body.String())
}
