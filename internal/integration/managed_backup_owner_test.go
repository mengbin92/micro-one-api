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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	v "micro-one-api/api/admin/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	d "micro-one-api/domain/backup/data"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestManagedBackupAdminHTTPAndRPC(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("SERVICE_TOKEN", "backup-owner-shared")
			t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"backup-owner-admin"}`)
			t.Setenv("JWT_SECRET_KEY", "backup-owner-jwt")
			t.Setenv("INITIAL_ADMIN_PASSWORD", "backup-root-password")
			t.Setenv("IDENTITY_ROUTING_V2", "false")
			db := dbtest.RoutingContextDB(t, driver)
			identity, identityRPC, _ := identitytest.NewIAMStack(db)
			_, err := identity.EnsureRootAdmin(context.Background())
			require.NoError(t, err)
			member, err := identity.Register(context.Background(), "backup-member", "password123", "member@backup.test", "default")
			require.NoError(t, err)
			rootSession := func(id int64) string {
				raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": id, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": fmt.Sprint("backup-", id), "sub": fmt.Sprint(id), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("backup-owner-jwt"))
				require.NoError(t, err)
				return raw
			}
			root, forged := rootSession(1), rootSession(member.ID)
			listener := bufconn.Listen(1 << 20)
			go func() { _ = identityRPC.Serve(listener) }()
			defer identityRPC.Server.Stop()
			conn, err := grpc.NewClient("passthrough:///backup-identity", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, c *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
				return invoke(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer backup-owner-admin"), method, req, reply, c, opts...)
			}))
			require.NoError(t, err)
			defer conn.Close()
			httpServer, rpcServer := admintest.NewManagedBackupStack(identityv1.NewIdentityServiceClient(conn), identityv1.NewIAMServiceClient(conn), d.NewRepo(&d.Data{DB: db}), "backup-owner-shared")
			defer rpcServer.Stop()
			rpcListener := bufconn.Listen(1 << 20)
			go func() { _ = rpcServer.Serve(rpcListener) }()
			clientConn, err := grpc.NewClient("passthrough:///backup-admin", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return rpcListener.Dial() }))
			require.NoError(t, err)
			defer clientConn.Close()
			client := v.NewAdminServiceClient(clientConn)
			call := func(method, path, body, credential string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if credential != "" {
					req.Header.Set("Authorization", "Bearer "+credential)
				}
				req.Header.Set("x-authorization-reason", "isolated owner test")
				w := httptest.NewRecorder()
				httpServer.ServeHTTP(w, req)
				return w
			}
			for _, mode := range []string{"legacy", "iam"} {
				if mode == "iam" {
					require.NoError(t, db.Table("iam_permissions").Where("code IN ? AND status = 'draft'", []string{"admin.console.enter", "system.option.read", "system.option.security.update"}).Update("status", "enabled").Error)
				}
				updates := map[string]any{"authorization_mode": mode, "cutover_state": "complete", "cutover_batch_id": "backup-owner", "cutover_verified_at": time.Now().UnixMilli()}
				if mode == "legacy" {
					updates["cutover_state"] = "idle"
					updates["cutover_verified_at"] = nil
					updates["cutover_batch_id"] = ""
				}
				require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(updates).Error)
				for _, token := range []string{"", forged} {
					w := call("GET", "/api/admin/backup-policy", "", token)
					require.Contains(t, []int{401, 403}, w.Code, w.Body.String())
				}
				w := call("GET", "/api/admin/backup-policy", "", root)
				require.Equal(t, 200, w.Code, w.Body.String())
				rpcContext := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer backup-owner-shared", "x-operator-authorization", "Bearer "+root, "x-authorization-reason", "isolated owner test"))
				policy, err := client.GetBackupPolicy(rpcContext, &v.GetBackupPolicyRequest{})
				require.NoError(t, err)
				w = call("PUT", "/api/admin/backup-policy", fmt.Sprintf(`{"policy":{"revision":%d,"enabled":false,"interval_hours":24,"retain":4,"rpo_hours":24,"rto_minutes":60}}`, policy.Revision), root)
				require.Equal(t, 200, w.Code, w.Body.String())
				_, err = client.UpdateBackupPolicy(rpcContext, &v.UpdateBackupPolicyRequest{Policy: policy})
				require.Error(t, err, "stale RPC revision must conflict")
				key := "backup-owner-request-" + mode
				w = call("POST", "/api/admin/backups", `{"request_id":"`+key+`"}`, root)
				require.Equal(t, 200, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), "object_key")
				w = call("POST", "/api/admin/backups", `{"request_id":"`+key+`"}`, root)
				require.Equal(t, 200, w.Code, w.Body.String())
				out, err := client.ListBackups(rpcContext, &v.ListBackupsRequest{})
				require.NoError(t, err)
				require.NotEmpty(t, out.Jobs)
				require.Equal(t, "1", out.Jobs[0].Actor)
				// A service token alone is never an operator, including legacy mode.
				_, err = client.GetBackupPolicy(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer backup-owner-shared")), &v.GetBackupPolicyRequest{})
				require.Error(t, err)
				require.NoError(t, db.Table("backup_jobs").Where("id = ?", key).Updates(map[string]any{"status": "failed", "error_code": "fixture_end"}).Error)
			}
		})
	}
}
