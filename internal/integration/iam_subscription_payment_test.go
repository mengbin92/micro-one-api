package integration

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
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
	"gorm.io/gorm"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	billingtest "micro-one-api/app/billing/testutil"
	channeltest "micro-one-api/app/channel/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	relayserver "micro-one-api/internal/server"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

// The public payment route must reach billing's real coverage interceptor,
// session resolver, plan snapshotter and order repository under IAM mode.
func TestIAMSubscriptionPaymentThroughRealOwners(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "payment-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"admin":"payment-admin","billing":"payment-billing","channel":"payment-channel","relay":"payment-relay","monitor":"payment-monitor"}`)
	t.Setenv("JWT_SECRET_KEY", "payment-isolated-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "payment-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
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
	channelListener := bufconn.Listen(1 << 20)
	channelServer := channeltest.NewIAMStack(db, authz.NewClient("channel", identityv1.NewIAMServiceClient(connect("channel", identityListener))))
	go func() { _ = channelServer.Serve(channelListener) }()
	t.Cleanup(func() { channelServer.Server.Stop(); _ = channelListener.Close() })
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	billingListener := bufconn.Listen(1 << 20)
	billingServer, notifyHTTP := billingtest.NewIAMAlipayStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connect("billing", identityListener))), billingtest.AlipayConfig{
		Enabled: true, AppID: "payment-app", FormURL: "https://alipay.invalid/gateway.do", NotifyURL: "https://billing.invalid/api/v1/user/payments/alipay/notify",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})), PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
	}, channelv1.NewChannelServiceClient(connect("billing", channelListener)))
	go func() { _ = billingServer.Serve(billingListener) }()
	t.Cleanup(func() { billingServer.Server.Stop(); _ = billingListener.Close() })
	adminConn := connect("admin", identityListener)
	billingClient := billingv1.NewBillingServiceClient(connect("admin", billingListener))
	admin := admintest.NewManagedBillingHTTP(identityv1.NewIdentityServiceClient(adminConn), identityv1.NewIAMServiceClient(adminConn), billingClient)
	repo := subscriptiondata.NewRepository(db, nil)
	limit := 10.0
	group := &subscriptionbiz.SubscriptionGroup{Name: "payment-policy", Status: subscriptionbiz.SubscriptionGroupStatusEnabled, RateMultiplier: 2, DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, MonthlyLimitUSD: &limit}
	require.NoError(t, repo.CreateGroup(context.Background(), group))
	require.NoError(t, db.Table("routing_groups").Create(map[string]any{"id": 501, "key": "payment-route", "display_name": "Payment route", "description": "", "status": "enabled", "access_mode": "restricted", "model_access_mode": "all_authorized", "created_at": time.Now().Unix(), "updated_at": time.Now().Unix()}).Error)
	contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 501, GroupKey: "payment-route", GrantsAccess: true}})
	require.NoError(t, err)
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "Payment plan", PriceQuota: 100, ValidityDays: 30, ValidityUnit: "day", ForSale: true, Contract: contract}
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

	// Validate the real RSA2 HTTP callback, then the same owner's transaction
	// and the user's HTTP/API-key read paths. No external provider is contacted.
	notify := func(appID, amount string, invalidSignature bool) *httptest.ResponseRecorder {
		t.Helper()
		params := url.Values{"app_id": {appID}, "out_trade_no": {reply.Data.Payment.TradeNo}, "trade_no": {"isolated-provider-trade"}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {amount}, "sign_type": {"RSA2"}}
		var keys, parts []string
		for key := range params {
			if key != "sign_type" && params.Get(key) != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+"="+params.Get(key))
		}
		digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
		signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
		require.NoError(t, err)
		params.Set("sign", base64.StdEncoding.EncodeToString(signature))
		if invalidSignature {
			params.Set("total_amount", "0.01")
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/user/payments/alipay/notify", strings.NewReader(params.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		notifyHTTP.ServeHTTP(w, r)
		return w
	}
	assertPending := func() {
		t.Helper()
		var order struct{ Status, AssetIssueStatus string }
		require.NoError(t, db.Table("payment_orders").Where("trade_no = ?", reply.Data.Payment.TradeNo).Take(&order).Error)
		require.Equal(t, "pending", order.Status)
		require.Equal(t, "pending", order.AssetIssueStatus)
		require.NoError(t, db.Table("user_subscriptions").Where("user_id = ?", member.ID).Count(&count).Error)
		require.Zero(t, count)
		require.NoError(t, db.Table("subscription_routing_entitlements").Count(&count).Error)
		require.Zero(t, count)
	}
	for _, invalid := range []struct {
		appID, amount string
		signature     bool
	}{{"payment-app", "100.00", true}, {"other-app", "100.00", false}, {"", "100.00", false}, {"payment-app", "0.01", false}, {"payment-app", "0.00", false}, {"payment-app", "invalid", false}, {"payment-app", "", false}} {
		require.Equal(t, "fail", notify(invalid.appID, invalid.amount, invalid.signature).Body.String(), "app=%s amount=%s tampered=%v", invalid.appID, invalid.amount, invalid.signature)
		assertPending()
	}
	// Inject failure after the subscription insert: the order, entitlement
	// and subscription must all roll back before the same callback recovers.
	failIssue := true
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("payment:test-issuance-failure", func(tx *gorm.DB) {
		if failIssue && tx.Statement.Table == "user_subscriptions" {
			tx.AddError(errors.New("isolated subscription issuance failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("payment:test-issuance-failure") })
	require.Equal(t, "fail", notify("payment-app", "100.00", false).Body.String())
	assertPending()
	failIssue = false
	require.Equal(t, "success", notify("payment-app", "100.00", false).Body.String())
	var paid struct {
		Status, AssetIssueStatus, PlanSnapshot string
		SubscriptionID                         int64
	}
	require.NoError(t, db.Table("payment_orders").Where("trade_no = ?", reply.Data.Payment.TradeNo).Take(&paid).Error)
	require.Equal(t, "paid", paid.Status)
	require.Equal(t, "issued", paid.AssetIssueStatus)
	sub, err := repo.GetSubscriptionByID(context.Background(), paid.SubscriptionID)
	require.NoError(t, err)
	require.EqualValues(t, 100, sub.PricePaid)
	require.Equal(t, contract.Digest, sub.Contract.Digest)
	require.Equal(t, reply.Data.Payment.TradeNo, sub.SourceOrder)
	require.EqualValues(t, 30*86400, sub.ExpiresAt-sub.StartsAt)
	require.Len(t, sub.RoutingGrants(time.Now().Unix()), 1)
	require.Equal(t, "success", notify("payment-app", "100.00", false).Body.String())
	current, err := repo.GetSubscriptionByID(context.Background(), sub.ID)
	require.NoError(t, err)
	require.Equal(t, sub.ExpiresAt, current.ExpiresAt, "duplicate callback cannot extend the subscription")
	for _, table := range []string{"user_subscriptions", "subscription_routing_entitlements"} {
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.EqualValues(t, 1, count, table)
	}

	key, err := identity.CreateAccessToken(context.Background(), member.ID, "paid-subscription-key", nil, 0)
	require.NoError(t, err)
	relay := relayserver.NewHTTPServer(identityv1.NewIdentityServiceClient(connect("relay", identityListener)), nil, billingv1.NewBillingServiceClient(connect("relay", billingListener)), nil, nil)
	relay.SetSubscriptionUsecase(subscriptionbiz.NewSubscriptionUsecase(nil, nil))
	relayHTTP := khttp.NewServer()
	relay.RegisterRoutes(relayHTTP)
	for _, route := range []struct {
		handler     http.Handler
		path, token string
	}{{admin, "/api/v1/subscriptions/progress", session}, {relayHTTP, "/v1/subscription/usage?user_id=1", key.Key}} {
		r := httptest.NewRequest(http.MethodGet, route.path, nil)
		r.Header.Set("Authorization", "Bearer "+route.token)
		w := httptest.NewRecorder()
		route.handler.ServeHTTP(w, r)
		var progress struct {
			Success bool                                  `json:"success"`
			Data    *subscriptionbiz.SubscriptionProgress `json:"data"`
		}
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NoError(t, jsonx.Unmarshal(w.Body.Bytes(), &progress))
		require.True(t, progress.Success, w.Body.String())
		require.NotNil(t, progress.Data)
		require.Equal(t, sub.ID, progress.Data.ID)
		require.Equal(t, "billing", progress.Data.UsageSource)
		require.Equal(t, contract.Digest, progress.Data.Contract.Digest)
		require.InDelta(t, 10, *progress.Data.DailyUsed.Available, 1e-9)
	}

	// The wallet transport must use the same authenticated buyer and owner
	// price, and replay the persisted commerce claim without a second debit.
	row.ForSale = true
	require.NoError(t, repo.UpdatePlan(context.Background(), row))
	require.NoError(t, db.Table("users").Where("id = ?", member.ID).Update("balance", 1000).Error)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/purchase", strings.NewReader(fmt.Sprintf(`{"plan_id":%d,"user_id":1,"price_amount":1}`, plan.ID)))
		r.Header.Set("Authorization", "Bearer "+session)
		r.Header.Set("Idempotency-Key", "wallet-renewal")
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"success":true`)
	}
	var balance int64
	require.NoError(t, db.Table("users").Select("balance").Where("id = ?", member.ID).Scan(&balance).Error)
	require.EqualValues(t, 900, balance)
	current, err = repo.GetSubscriptionByID(context.Background(), sub.ID)
	require.NoError(t, err)
	require.EqualValues(t, sub.ExpiresAt+30*86400, current.ExpiresAt)
	require.NoError(t, db.Table("billing_ledgers").Where("ledger_dedupe_key = ?", fmt.Sprintf("subscription-commerce:%d:wallet-renewal", member.ID)).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Table("subscription_commerce_receipts").Where("user_id = ? AND request_id = ?", member.ID, "wallet-renewal").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
