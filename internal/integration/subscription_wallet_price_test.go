package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestLegacySubscriptionCNYPriceChargesRealWallet(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "wallet-price-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"wallet-price-admin","billing":"wallet-price-billing"}`)
	t.Setenv("JWT_SECRET_KEY", "wallet-price-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "wallet-price-password")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	identityListener := bufconn.Listen(1 << 20)
	go func() { _ = identityServer.Serve(identityListener) }()
	t.Cleanup(func() { identityServer.Server.Stop(); _ = identityListener.Close() })
	connect := func(caller string, listener *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///wallet-price-"+caller, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer wallet-price-"+caller)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	billingListener := bufconn.Listen(1 << 20)
	billingServer := billingtest.NewIAMStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connect("billing", identityListener))))
	go func() { _ = billingServer.Serve(billingListener) }()
	t.Cleanup(func() { billingServer.Server.Stop(); _ = billingListener.Close() })
	adminConn := connect("admin", identityListener)
	admin := admintest.NewLegacySubscriptionPurchaseHTTP(db, identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), billingv1.NewBillingServiceClient(connect("admin", billingListener)))
	session, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": 1, "token_type": "user_session", "pwd_epoch": 0, "jti": "wallet-price-root", "sub": "1", "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("wallet-price-jwt"))
	require.NoError(t, err)
	repo := subscriptiondata.NewRepository(db, nil)
	group := &subscriptionbiz.SubscriptionGroup{Name: "wallet-price-policy", RateMultiplier: 1, Status: 1, PriceQuota: 100, DurationDays: 30}
	require.NoError(t, repo.CreateGroup(context.Background(), group))
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "wallet-price-plan", PriceQuota: 100, ValidityDays: 30, ForSale: true}
	require.NoError(t, repo.CreatePlan(context.Background(), plan))
	upgrade := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "wallet-price-upgrade", PriceQuota: 200, ValidityDays: 30, ForSale: true}
	require.NoError(t, repo.CreatePlan(context.Background(), upgrade))
	for _, kind := range []string{"group", "plan", "upgrade"} {
		t.Run(kind, func(t *testing.T) {
			require.NoError(t, db.Exec("DELETE FROM user_subscriptions").Error)
			require.NoError(t, db.Table("users").Where("id = 1").Update("balance", 20_000_000).Error)
			path, body := "/api/v1/subscriptions/purchase", fmt.Sprintf(`{"group_id":%d}`, group.ID)
			if kind == "plan" {
				body = fmt.Sprintf(`{"plan_id":%d}`, plan.ID)
			}
			if kind == "upgrade" {
				now := time.Now().Unix()
				sub := &subscriptionbiz.UserSubscription{UserID: 1, GroupID: group.ID, Status: "active", StartsAt: now, ExpiresAt: now + 86400}
				require.NoError(t, repo.CreateSubscription(context.Background(), sub))
				path = "/api/v1/admin/subscriptions/change"
				body = fmt.Sprintf(`{"user_id":1,"from_subscription_id":%d,"to_plan_id":%d,"old_price_quota":100,"new_price_quota":200}`, sub.ID, upgrade.ID)
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+session)
			req.Header.Set("Idempotency-Key", "wallet-price-"+kind)
			rec := httptest.NewRecorder()
			admin.ServeHTTP(rec, req)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"success":true`)
			if kind == "upgrade" {
				require.Contains(t, rec.Body.String(), `"charged_quota":10000000`, "change response reports the actual wallet charge")
			}
			var balance int64
			require.NoError(t, db.Table("users").Select("balance").Where("id = 1").Scan(&balance).Error)
			require.EqualValues(t, 10_000_000, balance, "CNY 100 at multiplier 10 must deduct 10,000,000 fixed USD amount units")
			var amount int64
			require.NoError(t, db.Table("billing_ledgers").Select("amount").Where("type = ?", "subscription").Order("id DESC").Limit(1).Scan(&amount).Error)
			require.EqualValues(t, -10_000_000, amount)
			active, err := subscriptionbiz.NewSubscriptionUsecase(repo, repo).GetActiveSubscriptionForUser(context.Background(), 1)
			require.NoError(t, err)
			paidPrice := int64(100)
			if kind == "upgrade" {
				paidPrice = 200
			}
			require.Equal(t, paidPrice, active.PricePaid, "new paid subscriptions preserve their nominal CNY upgrade credit")
		})
	}
}
