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
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

// The public payment route must reach billing's real coverage interceptor,
// session resolver, plan snapshotter and order repository under IAM mode.
func TestIAMSubscriptionPaymentThroughRealOwners(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "payment-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"payment-admin","billing":"payment-billing","monitor":"payment-monitor"}`)
	t.Setenv("JWT_SECRET_KEY", "payment-isolated-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "payment-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "payment-member", "password123", "payment@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "payment-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	identityListener := bufconn.Listen(1 << 20)
	go func() { _ = identityServer.Serve(identityListener) }()
	t.Cleanup(func() { identityServer.Server.Stop(); _ = identityListener.Close() })
	connect := func(caller string, target *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///payment-"+caller, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return target.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer payment-"+caller)
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
	billingClient := billingv1.NewBillingServiceClient(connect("admin", billingListener))
	admin := admintest.NewManagedBillingHTTP(identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), billingClient)
	repo := subscriptiondata.NewRepository(db, nil)
	group := &subscriptionbiz.SubscriptionGroup{Name: "payment-policy", Status: subscriptionbiz.SubscriptionGroupStatusEnabled}
	require.NoError(t, repo.CreateGroup(context.Background(), group))
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "Payment plan", PriceQuota: 100, ValidityDays: 30, ValidityUnit: "day", ForSale: true}
	require.NoError(t, repo.CreatePlan(context.Background(), plan))
	session, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": member.ID, "role": 1, "token_type": "user_session", "pwd_epoch": 0, "jti": "payment-member", "sub": fmt.Sprint(member.ID), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Unix() + 3600}).SignedString([]byte("payment-isolated-jwt"))
	require.NoError(t, err)
	purchase := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/subscriptions/purchase/payment", strings.NewReader(fmt.Sprintf(`{"plan_id":%d,"user_id":1,"money_cents":1}`, plan.ID)))
		r.Header.Set("Authorization", "Bearer "+session)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	w := purchase("payment-renewal")
	require.Equal(t, 200, w.Code, w.Body.String())
	var reply struct {
		Success bool `json:"success"`
		Data    struct {
			Payment struct {
				TradeNo    string `json:"trade_no"`
				MoneyCents int64  `json:"money_cents"`
				PayURL     string `json:"pay_url"`
			} `json:"payment"`
		} `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(w.Body.Bytes(), &reply))
	require.True(t, reply.Success, w.Body.String())
	require.NotEmpty(t, reply.Data.Payment.TradeNo)
	require.EqualValues(t, 10000, reply.Data.Payment.MoneyCents, "billing owns the price; caller cannot underpay")
	require.NotEmpty(t, reply.Data.Payment.PayURL)
	replay := purchase("payment-renewal")
	require.Contains(t, replay.Body.String(), `"success":true`, replay.Body.String())
	var count int64
	require.NoError(t, db.Table("payment_orders").Where("user_id = ?", fmt.Sprint(member.ID)).Count(&count).Error)
	require.EqualValues(t, 1, count, "same request must not create a second order")
	row, err := repo.GetPlanByID(context.Background(), plan.ID)
	require.NoError(t, err)
	row.ForSale = false
	require.NoError(t, repo.UpdatePlan(context.Background(), row))
	require.Contains(t, purchase("payment-renewal").Body.String(), `"success":true`, "idempotent replay survives plan retirement")
	require.Contains(t, purchase("new-retired-plan-order").Body.String(), `"success":false`)
	operator := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer "+session))
	foreignReq := &billingv1.CreatePaymentOrderRequest{UserId: "1", Channel: "alipay", AssetType: "subscription", AssetAmount: 1, MoneyCents: 1, PlanId: plan.ID, RequestId: "foreign-user"}
	_, err = billingClient.CreatePaymentOrder(operator, foreignReq)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "valid user session cannot purchase for a foreign account")
	_, err = billingClient.CreatePaymentOrder(context.Background(), foreignReq)
	require.Equal(t, codes.Unauthenticated, status.Code(err), "dedicated admin caller alone cannot create a self order")
	_, err = billingv1.NewBillingServiceClient(connect("monitor", billingListener)).CreatePaymentOrder(operator, foreignReq)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "unclassified caller remains blocked")
}
