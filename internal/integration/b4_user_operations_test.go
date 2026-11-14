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
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	channeltest "micro-one-api/app/channel/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

func b4Dial(t *testing.T, server interface{ Serve(net.Listener) error }, credential string) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///b4-owner", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, c *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if credential != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+credential)
		}
		return invoke(ctx, method, req, reply, c, opts...)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func TestB4UserOperationsHTTPAndRPC(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("SERVICE_TOKEN", "b4-owner-shared")
			t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"b4-owner-admin","channel":"b4-owner-channel"}`)
			t.Setenv("JWT_SECRET_KEY", "b4-owner-jwt")
			t.Setenv("INITIAL_ADMIN_PASSWORD", "b4-fixture-root-password")
			t.Setenv("IDENTITY_ROUTING_V2", "false")
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
			db := dbtest.RoutingContextDB(t, driver)
			identity, identityRPC, _ := identitytest.NewIAMStack(db)
			_, err := identity.EnsureRootAdmin(context.Background())
			require.NoError(t, err)
			member, err := identity.Register(context.Background(), "b4-member", "password123", "b4-member@fixture.test", "default")
			require.NoError(t, err)
			for _, group := range []struct {
				id  int64
				key string
			}{{7101, "b4-default"}, {7102, "b4-private"}} {
				require.NoError(t, db.Table("routing_groups").Create(map[string]any{"id": group.id, "key": group.key, "display_name": group.key, "description": "fixture", "status": "enabled", "access_mode": "public", "model_access_mode": "all_authorized", "revision": 1, "created_at": 1, "updated_at": 1}).Error)
			}
			require.NoError(t, db.Table("users").Where("id IN ?", []int64{1, member.ID}).Updates(map[string]any{"default_routing_group_id": 7101, "routing_access_revision": 1, "public_group_access": "all"}).Error)
			for i, name := range []string{"b4-visible", "b4-hidden"} {
				id := int64(7101 + i)
				group := []string{"b4-default", "b4-private"}[i]
				require.NoError(t, db.Table("channels").Create(map[string]any{"id": id, "name": "hidden supplier", "type": 1, "status": 1, "group": group, "models": name, "key": ""}).Error)
				require.NoError(t, db.Table("models").Create(map[string]any{"model_id": name, "display_name": name, "provider": "", "model_type": "chat", "status": 1, "is_public": true}).Error)
				var modelPK int64
				require.NoError(t, db.Table("models").Select("id").Where("model_id = ?", name).Scan(&modelPK).Error)
				require.NoError(t, db.Table("model_channel_mapping").Create(map[string]any{"channel_id": id, "model_id": modelPK, "enabled": true, "priority": 0, "upstream_model_id": name}).Error)
				require.NoError(t, db.Table("channel_routing_groups").Create(map[string]any{"channel_id": id, "routing_group_id": id}).Error)
			}
			t.Setenv("IDENTITY_ROUTING_V2", "true")
			session := func(id int64) string {
				raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": id, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": fmt.Sprint("b4-", id), "sub": fmt.Sprint(id), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("b4-owner-jwt"))
				require.NoError(t, err)
				return raw
			}
			root, memberToken := session(1), session(member.ID)
			identityConn := b4Dial(t, identityRPC, "b4-owner-admin")
			defer identityRPC.Server.Stop()
			channelAuthConn := b4Dial(t, identityRPC, "b4-owner-channel")
			channelRPC := channeltest.NewIAMStack(db, authz.NewClient("channel", identityv1.NewIAMServiceClient(channelAuthConn)))
			channelConn := b4Dial(t, channelRPC, "b4-owner-admin")
			defer channelRPC.Server.Stop()
			httpServer, rpcServer := admintest.NewUserOperationsStack(db, identityv1.NewIdentityServiceClient(identityConn), identityv1.NewIAMServiceClient(identityConn), channelv1.NewChannelServiceClient(channelConn))
			defer rpcServer.Stop()
			conn := b4Dial(t, rpcServer, "")
			client := v.NewAdminServiceClient(conn)
			rpcContext := func(token string) context.Context {
				return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer b4-owner-shared", "x-operator-authorization", "Bearer "+token, "x-authorization-reason", "b4 owner test"))
			}
			call := func(method, path, body, token string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				httpServer.ServeHTTP(w, req)
				return w
			}
			for _, mode := range []string{"legacy", "iam"} {
				if mode == "iam" {
					require.NoError(t, db.Table("iam_permissions").Where("code IN ? AND status = 'draft'", []string{"admin.console.enter", "system.content.notice.update"}).Update("status", "enabled").Error)
				}
				updates := map[string]any{"authorization_mode": mode, "cutover_state": "complete", "cutover_batch_id": "b4-owner", "cutover_verified_at": time.Now().UnixMilli()}
				if mode == "legacy" {
					updates["cutover_state"] = "idle"
					updates["cutover_batch_id"] = ""
					updates["cutover_verified_at"] = nil
				}
				require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(updates).Error)
				w := call("GET", "/api/admin/announcements", "", memberToken)
				require.Contains(t, []int{401, 403}, w.Code, w.Body.String())
				_, err = client.ListManagedAnnouncements(rpcContext(memberToken), &v.ListAnnouncementsRequest{})
				require.Error(t, err)
				_, err = client.ListManagedAnnouncements(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer b4-owner-shared")), &v.ListAnnouncementsRequest{})
				require.Error(t, err)
				a, err := client.SaveAnnouncement(rpcContext(root), &v.SaveAnnouncementRequest{Announcement: &v.Announcement{Title: "notice-" + mode, Body: "plain <script>text</script>", Audience: "users", Targets: []string{fmt.Sprint(member.ID)}, Status: "published"}, Reason: "运营定向发布"})
				require.NoError(t, err)
				w = call("GET", "/api/user/announcements?user_id=1&group_id=7102", "", memberToken)
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				require.Contains(t, w.Body.String(), "notice-"+mode)
				require.Positive(t, a.PublishAt)
				require.NotContains(t, w.Body.String(), "targets")
				require.NotContains(t, w.Body.String(), "audience")
				_, err = client.ReadAnnouncement(rpcContext(root), &v.ReadAnnouncementRequest{Id: a.Id, Revision: a.Revision})
				require.Error(t, err)
				w = call("POST", "/api/user/announcements/read", fmt.Sprintf(`{"id":%q,"revision":%d}`, a.Id, a.Revision), memberToken)
				require.Equal(t, 200, w.Code, w.Body.String())
				mine, err := client.ListMyAnnouncements(rpcContext(memberToken), &v.ListAnnouncementsRequest{})
				require.NoError(t, err)
				var found bool
				for _, item := range mine.Items {
					if item.Id == a.Id {
						found = item.Read
					}
				}
				require.True(t, found)
				// HTTP save uses exactly the same CAS as RPC.
				payload, err := jsonx.Marshal(map[string]any{"announcement": a, "reason": "更新运营公告"})
				require.NoError(t, err)
				w = call("PUT", "/api/admin/announcements", string(payload), root)
				require.Equal(t, 200, w.Code, w.Body.String())
				_, err = client.SaveAnnouncement(rpcContext(root), &v.SaveAnnouncementRequest{Announcement: a, Reason: "过期版本保存"})
				require.Error(t, err)
				models, err := client.GetMyModelAvailability(rpcContext(memberToken), &v.GetMyModelAvailabilityRequest{})
				require.NoError(t, err)
				require.Equal(t, []string{"b4-visible"}, models.Models)
				w = call("GET", "/api/user/model-availability?group=private", "", memberToken)
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "b4-visible")
				require.NotContains(t, w.Body.String(), "b4-hidden")
				require.NotContains(t, w.Body.String(), "supplier")
				require.NotContains(t, w.Body.String(), "channel")
				w = call("GET", "/api/user/model-availability", "", "")
				require.Equal(t, 401, w.Code)
			}
			// Moving the user to another group changes the audience on the next request.
			a, err := client.SaveAnnouncement(rpcContext(root), &v.SaveAnnouncementRequest{Announcement: &v.Announcement{Title: "group-only", Body: "group notice", Audience: "groups", Targets: []string{"7101"}, Status: "published"}, Reason: "分组公告发布"})
			require.NoError(t, err)
			mine, err := client.ListMyAnnouncements(rpcContext(memberToken), &v.ListAnnouncementsRequest{})
			require.NoError(t, err)
			var found bool
			for _, item := range mine.Items {
				if item.Id == a.Id {
					found = true
				}
			}
			require.True(t, found)
			require.NoError(t, db.Table("users").Where("id = ?", member.ID).Update("default_routing_group_id", 7102).Error)
			_, err = client.ReadAnnouncement(rpcContext(memberToken), &v.ReadAnnouncementRequest{Id: a.Id, Revision: a.Revision})
			require.Error(t, err)
			require.NoError(t, db.Table("iam_sessions").Where("user_id = ?", member.ID).Update("revoked_at", time.Now().UnixMilli()).Error)
			_, err = client.ListMyAnnouncements(rpcContext(memberToken), &v.ListAnnouncementsRequest{})
			require.Error(t, err)
			w := call("GET", "/api/user/announcements", "", memberToken)
			require.Contains(t, []int{401, 403}, w.Code, w.Body.String())
		})
	}
}
