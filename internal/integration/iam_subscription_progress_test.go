package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	billingtest "micro-one-api/app/billing/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	relayserver "micro-one-api/internal/server"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

// Unlike the adapter stubs, this reaches the billing receiver's fixed caller
// registry, identity's real session resolver and the locked subscription read.
func TestIAMSelfSubscriptionProgressThroughRealOwners(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "progress-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"progress-admin","billing":"progress-billing","relay":"progress-relay","monitor":"progress-monitor"}`)
	t.Setenv("JWT_SECRET_KEY", "progress-isolated-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "progress-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "progress-member", "password123", "progress@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "progress-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = identityServer.Serve(listener) }()
	t.Cleanup(func() { identityServer.Server.Stop(); _ = listener.Close() })
	connect := func(caller string, target *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///progress-"+caller, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer progress-"+caller)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	billingListener := bufconn.Listen(1 << 20)
	billingServer := billingtest.NewIAMStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connect("billing", listener))))
	go func() { _ = billingServer.Serve(billingListener) }()
	t.Cleanup(func() { billingServer.Server.Stop(); _ = billingListener.Close() })
	adminConn := connect("admin", listener)
	adminBilling := billingv1.NewBillingServiceClient(connect("admin", billingListener))
	admin := admintest.NewManagedBillingHTTP(identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), adminBilling)
	relay := relayserver.NewHTTPServer(identityv1.NewIdentityServiceClient(connect("relay", listener)), nil, billingv1.NewBillingServiceClient(connect("relay", billingListener)), nil, nil)
	relay.SetSubscriptionUsecase(subscriptionbiz.NewSubscriptionUsecase(nil, nil))
	relayHTTP := khttp.NewServer()
	relay.RegisterRoutes(relayHTTP)
	repo := subscriptiondata.NewRepository(db, nil)
	limit := 10.0
	group := &subscriptionbiz.SubscriptionGroup{Name: "progress-policy", DisplayName: "Progress policy", RateMultiplier: 2, DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, MonthlyLimitUSD: &limit, Status: 1}
	require.NoError(t, repo.CreateGroup(context.Background(), group))
	now := time.Now().Unix()
	sub := &subscriptionbiz.UserSubscription{UserID: member.ID, GroupID: group.ID, SubscriptionName: "Progress subscription", Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 86400, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now, DailyUsageUSD: 1.25, WeeklyUsageUSD: 1.25, MonthlyUsageUSD: 1.25, CreatedAt: now}
	require.NoError(t, repo.CreateSubscription(context.Background(), sub))
	require.NoError(t, db.Table("billing_reservations").Create(map[string]any{"reservation_id": "progress-frozen", "user_id": fmt.Sprint(member.ID), "request_id": "progress-frozen", "amount": 2500, "status": "reserved", "model": "fixture", "subscription_id": sub.ID, "subscription_amount_usd": 0.25, "subscription_accounting_usd": 0.5, "subscription_daily_window_start": now, "subscription_weekly_window_start": now, "subscription_monthly_window_start": now, "created_at": time.Now(), "updated_at": time.Now(), "expired_at": time.Now().Add(time.Hour)}).Error)
	session, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": member.ID, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": "progress-member", "sub": fmt.Sprint(member.ID), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": now + 3600}).SignedString([]byte("progress-isolated-jwt"))
	require.NoError(t, err)
	key, err := identity.CreateAccessToken(context.Background(), member.ID, "progress-key", nil, 0)
	require.NoError(t, err)
	call := func(handler http.Handler, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	assertProgress := func(w *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, 200, w.Code, w.Body.String())
		var reply struct {
			Success bool                                  `json:"success"`
			Data    *subscriptionbiz.SubscriptionProgress `json:"data"`
		}
		require.NoError(t, jsonx.Unmarshal(w.Body.Bytes(), &reply))
		require.True(t, reply.Success, w.Body.String())
		require.NotNil(t, reply.Data)
		require.Equal(t, sub.ID, reply.Data.ID)
		require.Equal(t, "billing", reply.Data.UsageSource)
		require.InDelta(t, 1.25, reply.Data.DailyUsed.Settled, 1e-9)
		require.InDelta(t, 0.5, *reply.Data.DailyUsed.Frozen, 1e-9)
		require.InDelta(t, 8.25, *reply.Data.DailyUsed.Available, 1e-9)
	}
	assertProgress(call(admin, "/api/v1/subscriptions/progress", session))
	assertProgress(call(relayHTTP, "/v1/subscription/usage?user_id=1", key.Key))
	rootKey, err := identity.CreateAccessToken(context.Background(), 1, "progress-no-subscription", nil, 0)
	require.NoError(t, err)
	noSubscription := call(relayHTTP, "/v1/subscription/usage", rootKey.Key)
	require.Equal(t, 200, noSubscription.Code, noSubscription.Body.String())
	require.Contains(t, noSubscription.Body.String(), `"success":false`)
	require.Equal(t, 403, call(admin, "/api/v1/subscriptions/progress?user_id=1", session).Code)
	for _, token := range []string{"", "invalid-session"} {
		require.Equal(t, 401, call(admin, "/api/v1/subscriptions/progress", token).Code)
		require.Equal(t, 401, call(relayHTTP, "/v1/subscription/usage", token).Code)
	}
	_, err = adminBilling.GetSubscriptionUsage(context.Background(), &billingv1.GetSubscriptionUsageRequest{UserId: member.ID, SelfRequest: true})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "dedicated admin alone cannot impersonate self")
	_, err = billingv1.NewBillingServiceClient(connect("monitor", billingListener)).GetSubscriptionUsage(context.Background(), &billingv1.GetSubscriptionUsageRequest{UserId: member.ID})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": member.ID, "token_type": "user_session", "pwd_epoch": 0, "jti": "progress-expired", "sub": fmt.Sprint(member.ID), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": now - 1}).SignedString([]byte("progress-isolated-jwt"))
	require.NoError(t, err)
	require.Equal(t, 401, call(admin, "/api/v1/subscriptions/progress", expired).Code)
	require.NoError(t, db.Table("tokens").Where("id = ?", key.ID).Update("status", identitytest.TokenStatusDisabled).Error)
	require.Equal(t, 403, call(relayHTTP, "/v1/subscription/usage", key.Key).Code)
	require.NoError(t, db.Table("users").Where("id = ?", member.ID).Update("password_changed_at", time.Now().UnixMilli()).Error)
	require.Equal(t, 401, call(admin, "/api/v1/subscriptions/progress", session).Code)
}
