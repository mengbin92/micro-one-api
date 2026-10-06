package integration

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	identitytest "micro-one-api/app/identity/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMDActualCutoverHTTPRPC(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "d-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"d-admin"}`)
	t.Setenv("JWT_SECRET_KEY", "d-scratch-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "d-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, server, self := identitytest.NewIAMStack(db)
	ctx := context.Background()
	root, err := identity.EnsureRootAdmin(ctx)
	require.NoError(t, err)
	var rootID int64
	require.NoError(t, db.Table("users").Select("id").Where("username = ?", root.Username).Scan(&rootID).Error)
	member, err := identity.Register(ctx, "old-session", "password123", "old@example.com", "default")
	require.NoError(t, err)
	token := func(uid int64) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": fmt.Sprint("old-jti-", uid), "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("d-scratch-jwt"))
		require.NoError(t, err)
		return raw
	}
	memberRaw := token(member.ID)
	rootRaw := token(rootID)
	httpCall := func(raw string) int {
		r := httptest.NewRequest("GET", "/api/user/authorization", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		self.ServeHTTP(w, r)
		return w.Code
	}
	require.Equal(t, 200, httpCall(memberRaw))
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///d-cutover", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := identityv1.NewIAMServiceClient(conn)
	migration := identitytest.NewIAMMigrationStack(db)
	manifest := identitytest.MigrationManifest{}
	evidence := &identitytest.CutoverEvidence{BatchID: "d-real", RootUserID: rootID, SourceDigest: fmt.Sprintf("%064d", 1), DatabaseIdentity: "scratch", ManifestDigest: identitytest.MigrationManifestDigest(manifest), CapturedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour), Barrier: "isolated ingress", Drained: "drained", OldWritersExited: "none running", OldDBChannelsRevoked: "isolated scratch", FinanceIsolation: "paused", FinanceReplay: "idempotent", Rollback: "compatible", Frontend: "C acceptance", Regression: "mandatory regressions", Instances: map[string]string{"admin": "a", "identity": "i", "channel": "c", "billing": "b", "config": "f", "log": "l", "monitor": "m", "notify": "n", "relay": "r"}}
	advance := func(command string) {
		status, err := migration.Execute(ctx, identitytest.MigrationRequest{Command: "status"})
		require.NoError(t, err)
		out, err := migration.Execute(ctx, identitytest.MigrationRequest{Command: command, BatchID: "d-real", ExpectedPolicyRevision: status.Policy.PolicyRevision, RequestID: "d-real-" + command, Reason: "real transport rehearsal", Evidence: evidence})
		require.NoError(t, err)
		require.NotEmpty(t, out.Policy.Cutover)
	}
	advance("apply")
	advance("block")
	require.NotEqual(t, 200, httpCall(memberRaw))
	_, err = identity.Register(ctx, "blocked-create", "password123", "b@example.com", "default")
	require.Error(t, err)
	advance("activate")
	require.NotEqual(t, 200, httpCall(memberRaw))
	_, err = identity.Register(ctx, "verified-create", "password123", "v@example.com", "default")
	require.Error(t, err)
	advance("complete")
	require.Equal(t, 200, httpCall(memberRaw))
	require.Equal(t, 200, httpCall(rootRaw))
	platform := &commonv1.AuthorizationContext{ContextType: "platform", ContextKey: "platform"}
	rpcCtx := func(service, raw string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+service, "x-operator-authorization", "Bearer "+raw))
	}
	_, err = client.ListRoles(rpcCtx("d-admin", rootRaw), &identityv1.IAMRequest{Context: platform})
	require.NoError(t, err)
	_, err = client.ListRoles(rpcCtx("d-admin", memberRaw), &identityv1.IAMRequest{Context: platform})
	require.Error(t, err, "forged JWT numeric root role cannot acquire management")
	_, err = client.ListRoles(rpcCtx("d-shared", rootRaw), &identityv1.IAMRequest{Context: platform})
	require.Error(t, err, "shared service token cannot prove admin capability")
	created, err := identity.Register(ctx, "after-cutover", "password123", "after@example.com", "default")
	require.NoError(t, err)
	var defaults int64
	require.NoError(t, db.Table("iam_user_roles AS a").Joins("JOIN iam_roles r ON r.id=a.role_id").Where("a.user_id=? AND a.origin=? AND r.code=?", created.ID, "default", "member").Count(&defaults).Error)
	require.EqualValues(t, 1, defaults)
}
