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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	billingtest "micro-one-api/app/billing/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/security/serviceidentity"
)

func TestIAMSelfUsageThroughDedicatedIdentityCaller(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "usage-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"identity":"usage-identity","billing":"usage-billing"}`)
	t.Setenv("JWT_SECRET_KEY", "usage-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "usage-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "usage-member", "password123", "usage@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "self-usage", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	identityListener := bufconn.Listen(1 << 20)
	go func() { _ = identityServer.Serve(identityListener) }()
	t.Cleanup(func() { identityServer.Server.Stop(); _ = identityListener.Close() })
	connect := func(caller string, listener *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///usage-"+caller, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer usage-"+caller)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	billingServer := billingtest.NewIAMStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connect("billing", identityListener))))
	billingListener := bufconn.Listen(1 << 20)
	go func() { _ = billingServer.Serve(billingListener) }()
	t.Cleanup(func() { billingServer.Server.Stop(); _ = billingListener.Close() })
	billing := billingv1.NewBillingServiceClient(connect("identity", billingListener))
	http := identitytest.NewSelfBillingHTTP(identity, billing)
	for _, uid := range []int64{1, member.ID} {
		require.NoError(t, db.Table("users").Where("id = ?", uid).Updates(map[string]any{"used_amount": uid * 100, "request_count": uid * 10}).Error)
		require.NoError(t, db.Table("billing_ledgers").Create(map[string]any{"user_id": fmt.Sprint(uid), "amount": -uid * 100, "balance_after": 0, "quota": uid * 100, "upstream_cost": 30, "type": "consume", "model_name": "usage-model", "ledger_dedupe_key": fmt.Sprint("self-usage-", uid), "created_at": time.Now().Add(-time.Hour).UTC()}).Error)
	}
	session := func(uid int64) string {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": uid, "role": 100, "token_type": "user_session", "pwd_epoch": 0, "jti": fmt.Sprint("usage-", uid), "sub": fmt.Sprint(uid), "iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("usage-jwt"))
		require.NoError(t, err)
		return raw
	}
	for _, uid := range []int64{1, member.ID} {
		t.Run(fmt.Sprint("user-", uid), func(t *testing.T) {
			for _, path := range []string{"/api/user/dashboard", "/api/user/logs?user_id=99999"} {
				req := httptest.NewRequest("GET", path, nil)
				req.Header.Set("Authorization", "Bearer "+session(uid))
				w := httptest.NewRecorder()
				http.ServeHTTP(w, req)
				require.Equal(t, 200, w.Code, w.Body.String())
				var response struct {
					Success bool `json:"success"`
					Data    struct {
						UsedAmount   int64 `json:"used_amount"`
						RequestCount int64 `json:"request_count"`
						Usage        []struct {
							Amount int64 `json:"amount"`
							Count  int64 `json:"count"`
						} `json:"usage"`
						Logs []struct {
							Amount int64 `json:"amount"`
						} `json:"items"`
						Total int64 `json:"total"`
					} `json:"data"`
				}
				require.NoError(t, jsonx.Unmarshal(w.Body.Bytes(), &response))
				require.True(t, response.Success, w.Body.String())
				if path == "/api/user/dashboard" {
					require.Equal(t, uid*100, response.Data.UsedAmount)
					require.Equal(t, uid*10, response.Data.RequestCount)
					var amount, count int64
					for _, day := range response.Data.Usage {
						amount += day.Amount
						count += day.Count
					}
					require.Equal(t, uid*100, amount, w.Body.String())
					require.EqualValues(t, 1, count)
				} else {
					require.EqualValues(t, 1, response.Data.Total)
					require.Len(t, response.Data.Logs, 1)
					require.Equal(t, -uid*100, response.Data.Logs[0].Amount)
				}
			}
		})
	}
	// Dedicated identity is a user caller, never a sessionless system bypass.
	for _, method := range []string{"ListLedger", "AggregateLedgerByDate"} {
		full := "/api.billing.v1.BillingService/" + method
		require.False(t, (serviceidentity.Principal{Name: "identity", Dedicated: true}).SystemCapability(full))
	}
	_, err = billing.ListLedger(context.Background(), &billingv1.ListLedgerRequest{UserId: fmt.Sprint(member.ID)})
	require.Error(t, err, "service credential alone must not expose ledger")
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer "+session(member.ID)))
	other, err := billing.ListLedger(ctx, &billingv1.ListLedgerRequest{UserId: "1"})
	require.NoError(t, err)
	require.Empty(t, other.Entries)
	require.Zero(t, other.Total)
	otherDays, err := billing.AggregateLedgerByDate(ctx, &billingv1.AggregateLedgerByDateRequest{UserId: "1"})
	require.NoError(t, err)
	require.Empty(t, otherDays.Daily)
	_, err = billing.AggregateUsage(ctx, &billingv1.AggregateUsageRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "identity must not gain managed financial reports")
}
