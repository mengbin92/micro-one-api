package main

import (
	"bytes"
	"context"
	oauth "micro-one-api/platform/security"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	v "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	billingtest "micro-one-api/app/billing/testutil"
	channeltest "micro-one-api/app/channel/testutil"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/app/identity/internal/server"
	"micro-one-api/app/identity/internal/service"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/security/totp"
)

type securityOAuthProvider struct{}

func (securityOAuthProvider) Name() string { return "github" }
func (securityOAuthProvider) AuthURL(state string) string {
	return "https://identity.example.test/authorize?state=" + url.QueryEscape(state)
}
func (securityOAuthProvider) Exchange(context.Context, string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{Provider: "github", ProviderID: "mfa-owner-subject", Username: "mfa-owner"}, nil
}

// Real BFF -> independent owner clients -> SQL/Redis, plus the direct RPC seam.
func TestSecurityOwnerIntegration(t *testing.T) {
	for _, mode := range []string{"legacy", "iam"} {
		t.Run(mode, func(t *testing.T) {
			srv := miniredis.RunT(t)
			for k, value := range map[string]string{"REDIS_ADDR": srv.Addr(), "REDIS_PASSWORD": "", "JWT_SECRET_KEY": "a2-integration-jwt-only", "TOKEN_HASH_KEY": "a2-integration-token-only", "IDENTITY_MFA_ENCRYPTION_KEY": "01234567890123456789012345678901", "IDENTITY_ROUTING_V2": "false", "IDENTITY_SCHEMA": "", "DATABASE_SCHEMA": "", "ADMIN_SCHEMA": "", "SERVICE_TOKEN": "a2-shared", "SERVICE_CALLER_TOKENS": `{"admin":"a2-admin","channel":"a2-channel","billing":"a2-billing"}`, "IDENTITY_STEP_UP_ENABLED": "true", "IDENTITY_MFA_REQUIRE_ADMIN": "false", "ADMIN_TOKEN": "", "INITIAL_ADMIN_PASSWORD": "a2-root-test-password", "CHANNEL_ENCRYPTION_KEY": "01234567890123456789012345678901"} {
				t.Setenv(k, value)
			}
			db := dbtest.RoutingContextDB(t, "sqlite")
			require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
			var files []struct{ Name, File string }
			require.NoError(t, db.Raw("PRAGMA database_list").Scan(&files).Error)
			var dsn string
			for _, file := range files {
				if file.Name == "main" {
					dsn = file.File + "?_foreign_keys=on&_busy_timeout=5000"
				}
			}
			require.NotEmpty(t, dsn)
			var identities []*biz.IdentityUsecase
			var owners, fronts []*httptest.Server
			for range 2 {
				repo, err := data.NewRepositoryFromEnv("sqlite3", dsn, "")
				require.NoError(t, err)
				uc := newIdentityUsecase(repo, nil)
				registry := oauth.NewProviderRegistry()
				registry.Register(securityOAuthProvider{})
				owner := httptest.NewServer(server.NewHTTPServer(":0", uc, registry))
				t.Cleanup(owner.Close)
				front := httptest.NewServer(admintest.NewPublicIdentityHTTP(owner.URL))
				t.Cleanup(front.Close)
				identities = append(identities, uc)
				owners = append(owners, owner)
				fronts = append(fronts, front)
			}
			rootUser, err := identities[0].EnsureRootAdmin(context.Background())
			require.NoError(t, err)
			mfaUser, err := identities[0].Register(context.Background(), "mfa-owner", "password123", "mfa@example.test", "default")
			require.NoError(t, err)
			_, err = identities[0].BindOAuthIdentity(context.Background(), mfaUser.ID, "github", "mfa-owner-subject")
			require.NoError(t, err)
			if mode == "iam" {
				require.NoError(t, db.Table("iam_policy_state").Where("id=1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "a2-test", "cutover_verified_at": time.Now().UnixMilli()}).Error)
			}
			request := func(base, method, path, token string, body any) (int, map[string]any) {
				raw, e := jsonx.Marshal(body)
				require.NoError(t, e)
				req, e := http.NewRequest(method, base+path, bytes.NewReader(raw))
				require.NoError(t, e)
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				res, e := http.DefaultClient.Do(req)
				require.NoError(t, e)
				defer res.Body.Close()
				var out map[string]any
				require.NoError(t, jsonx.NewDecoder(res.Body).Decode(&out))
				return res.StatusCode, out
			}

			callback := func() *http.Response {
				req, e := http.NewRequest("GET", fronts[0].URL+"/v1/oauth/github/callback?code=local-test&state=local-test", nil)
				require.NoError(t, e)
				req.Header.Set("Accept", "text/html")
				req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "local-test"})
				client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				response, e := client.Do(req)
				require.NoError(t, e)
				t.Cleanup(func() { _ = response.Body.Close() })
				return response
			}
			oauthResponse := callback()
			require.Equal(t, 302, oauthResponse.StatusCode)
			require.Equal(t, "/login#oauth=1", oauthResponse.Header.Get("Location"))
			var handoff *http.Cookie
			for _, cookie := range oauthResponse.Cookies() {
				if cookie.Name == "oauth_login" {
					handoff = cookie
				}
			}
			require.NotNil(t, handoff)
			require.True(t, handoff.HttpOnly)
			require.Equal(t, 60, handoff.MaxAge)
			handoffReq, e := http.NewRequest("POST", fronts[0].URL+"/api/user/security/oauth", nil)
			require.NoError(t, e)
			handoffReq.AddCookie(handoff)
			handoffResponse, e := http.DefaultClient.Do(handoffReq)
			require.NoError(t, e)
			require.Equal(t, 200, handoffResponse.StatusCode)
			_ = handoffResponse.Body.Close()
			handoffCode, _ := request(fronts[0].URL, "POST", "/api/user/security/oauth", "", nil)
			require.Equal(t, 401, handoffCode)
			_, payload := request(fronts[0].URL, "POST", "/api/user/login", "", map[string]string{"username": "mfa-owner", "password": "password123"})
			raw := payload["data"].(map[string]any)["token"].(string)
			code, _ := request(owners[1].URL, "POST", "/api/token", raw, map[string]string{"name": "must-deny"})
			require.Equal(t, 403, code)
			code, payload = request(fronts[1].URL, "POST", "/api/user/security/verify", raw, map[string]string{"password": "password123"})
			require.Equal(t, 200, code)
			code, payload = request(fronts[0].URL, "POST", "/api/user/security/setup", raw, map[string]string{})
			require.Equal(t, 200, code)
			secret := payload["secret"].(string)
			otp, e := totp.Code(secret, time.Now().Unix()/30-1, 6)
			require.NoError(t, e)
			code, payload = request(fronts[1].URL, "POST", "/api/user/security/confirm", raw, map[string]string{"code": otp})
			require.Equal(t, 200, code)
			recovery := payload["recovery_codes"].([]any)
			require.Len(t, recovery, 10)
			oauthResponse = callback()
			require.Equal(t, 302, oauthResponse.StatusCode)
			location, e := url.Parse(oauthResponse.Header.Get("Location"))
			require.NoError(t, e)
			fragment, e := url.ParseQuery(location.Fragment)
			require.NoError(t, e)
			oauthChallenge := fragment.Get("mfa")
			require.NotEmpty(t, oauthChallenge)
			_, e = identities[1].ValidateSessionToken(context.Background(), oauthChallenge)
			require.Error(t, e)
			_, oauthSession, e := identities[1].CompleteMFALogin(context.Background(), oauthChallenge, recovery[9].(string))
			require.NoError(t, e)
			require.NotEmpty(t, oauthSession)

			code, _ = request(owners[0].URL, "GET", "/api/user/security", raw, nil)
			require.Equal(t, 401, code)
			_, payload = request(fronts[0].URL, "POST", "/api/user/login", "", map[string]string{"username": "mfa-owner", "password": "password123"})
			first := payload["data"].(map[string]any)
			require.True(t, first["mfa_required"].(bool))
			require.Nil(t, first["token"])
			challenge := first["challenge"].(string)
			code, _ = request(owners[0].URL, "GET", "/api/user/self", challenge, nil)
			require.Equal(t, 401, code)
			code, payload = request(fronts[1].URL, "POST", "/api/user/security/login", "", map[string]any{"challenge": challenge, "code": recovery[0]})
			require.Equal(t, 200, code)
			raw = payload["token"].(string)
			code, _ = request(fronts[0].URL, "POST", "/api/user/security/login", "", map[string]any{"challenge": challenge, "code": recovery[1]})
			require.Equal(t, 400, code)

			listener := bufconn.Listen(1 << 20)
			grpcServer := server.NewGRPCServer(":0", service.NewIdentityService(identities[0]), service.NewIAMService(nil, identities[0]))
			go func() { _ = grpcServer.Serve(listener) }()
			t.Cleanup(func() { grpcServer.Server.Stop(); _ = listener.Close() })
			conn, e := grpc.NewClient("passthrough:///a2-identity", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			require.NoError(t, e)
			t.Cleanup(func() { _ = conn.Close() })

			connection := func(owner string, target *bufconn.Listener) *grpc.ClientConn {
				c, e := grpc.NewClient("passthrough:///a2-"+owner, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
					md, _ := metadata.FromOutgoingContext(ctx)
					md = md.Copy()
					md.Set("authorization", "Bearer a2-"+owner)
					return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...)
				}))
				require.NoError(t, e)
				t.Cleanup(func() { _ = c.Close() })
				return c
			}
			adminConn := connection("admin", listener)
			client := v.NewIdentityServiceClient(conn)
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer a2-admin", "x-operator-authorization", "Bearer "+raw))
			_, e = client.CreateAccessToken(ctx, &v.CreateAccessTokenRequest{UserId: mfaUser.ID, Name: "direct-deny"})
			require.Equal(t, codes.PermissionDenied, status.Code(e))
			_, e = client.VerifyStepUp(ctx, &v.SecurityRequest{Code: recovery[1].(string)})
			require.NoError(t, e)
			code, payload = request(owners[1].URL, "POST", "/api/token", raw, map[string]string{"name": "verified-token"})
			require.Equal(t, 200, code)
			require.Equal(t, true, payload["success"])
			// Owner-authorized secret paths read the same JTI proof; another JTI cannot borrow it.
			channelCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer a2-channel", "x-operator-authorization", "Bearer "+raw))
			_, e = v.NewIAMServiceClient(conn).GetResourceAuthorization(channelCtx, &v.ResourceAuthorizationRequest{ExecutionPoint: "channel.channels.secret", Operation: "channel.channel.secret.read"})
			if mode == "legacy" {
				require.NoError(t, e)
			} // IAM scope can deny this ordinary member after MFA succeeds.
			srv.FastForward(5 * time.Minute)
			_, e = v.NewIAMServiceClient(conn).GetResourceAuthorization(channelCtx, &v.ResourceAuthorizationRequest{ExecutionPoint: "channel.channels.secret", Operation: "channel.channel.secret.read"})
			require.Equal(t, codes.PermissionDenied, status.Code(e))
			code, _ = request(owners[1].URL, "POST", "/api/user/security/verify", raw, map[string]string{"code": strings.Repeat("x", 5000)})
			require.Equal(t, 400, code)

			if mode == "iam" {
				// Dedicated data owners must enforce the proof on direct gRPC and BFF exports.
				require.NoError(t, db.Table("iam_permissions").Where("code IN ?", []string{"channel.channel.export", "billing.redemption.export"}).Updates(map[string]any{"status": "enabled", "binding_state": "bound"}).Error)
				_, rootToken, e := identities[0].Login(context.Background(), rootUser.Username, "a2-root-test-password", "")
				require.NoError(t, e)
				channelListener := bufconn.Listen(1 << 20)
				channelServer := channeltest.NewIAMStack(db, authz.NewClient("channel", v.NewIAMServiceClient(connection("channel", listener))))
				go func() { _ = channelServer.Serve(channelListener) }()
				t.Cleanup(func() { channelServer.Server.Stop(); _ = channelListener.Close() })
				billingListener := bufconn.Listen(1 << 20)
				billingServer := billingtest.NewIAMStack(db, authz.NewClient("billing", v.NewIAMServiceClient(connection("billing", listener))))
				go func() { _ = billingServer.Serve(billingListener) }()
				t.Cleanup(func() { billingServer.Server.Stop(); _ = billingListener.Close() })
				channelClient := channelv1.NewChannelServiceClient(connection("admin", channelListener))
				billingClient := billingv1.NewBillingServiceClient(connection("admin", billingListener))
				rootCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer "+rootToken))
				_, e = channelClient.ExportChannels(rootCtx, &channelv1.ListChannelsRequest{})
				require.Equal(t, codes.PermissionDenied, status.Code(e))
				_, e = billingClient.ExportRedeemCodes(rootCtx, &billingv1.ExportRedeemCodesRequest{})
				require.Equal(t, codes.PermissionDenied, status.Code(e))
				require.NoError(t, identities[1].VerifyStepUp(context.Background(), rootToken, "a2-root-test-password", ""))
				_, e = channelClient.ExportChannels(rootCtx, &channelv1.ListChannelsRequest{})
				require.NoError(t, e)
				_, e = billingClient.ExportRedeemCodes(rootCtx, &billingv1.ExportRedeemCodesRequest{})
				require.NoError(t, e)
			}
			if os.Getenv("A2_PLAYWRIGHT") == "1" && mode == "iam" {
				root, e := filepath.Abs("../../../..")
				require.NoError(t, e)
				t.Setenv("ADMIN_WEB_ROOT", filepath.Join(root, "web/dist"))
				browserFront := httptest.NewServer(admintest.NewPublicIdentityHTTP(owners[0].URL, v.NewIAMServiceClient(adminConn)))
				t.Cleanup(browserFront.Close)
				command := exec.Command("npm", "exec", "playwright", "test", "--", "--config=playwright.a2.config.ts")
				command.Dir = filepath.Join(root, "web")
				command.Env = append(os.Environ(), "A2_BASE_URL="+browserFront.URL)
				output, e := command.CombinedOutput()
				t.Log(string(output))
				require.NoError(t, e)
			}
		})
	}
}
