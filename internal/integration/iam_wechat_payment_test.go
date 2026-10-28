package integration

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	admintest "micro-one-api/app/admin/testutil"
	billingtest "micro-one-api/app/billing/testutil"
	identitytest "micro-one-api/app/identity/testutil"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMWechatPaymentThroughRealOwners(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "wechat-shared")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"identity":"wechat-identity","billing":"wechat-billing"}`)
	t.Setenv("JWT_SECRET_KEY", "wechat-isolated-jwt")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "wechat-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, identityServer, _ := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "wechat-member", "password123", "wechat@example.com", "default")
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "wechat-isolated", "cutover_verified_at": time.Now().UnixMilli()}).Error)
	require.NoError(t, db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
	require.NoError(t, db.Table("users").Where("id = ?", member.ID).Update("balance", 0).Error)
	_, session, err := identity.Login(context.Background(), "wechat-member", "password123", "127.0.0.1")
	require.NoError(t, err)
	serve := func(server *kgrpc.Server) string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() { server.Server.Stop(); _ = listener.Close() })
		return listener.Addr().String()
	}
	connect := func(caller, endpoint string) *grpc.ClientConn {
		conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer wechat-"+caller)
			return invoke(metadata.NewOutgoingContext(ctx, md), method, req, reply, conn, opts...)
		}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	identityEndpoint := serve(identityServer)
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDER, err := x509.MarshalPKCS8PrivateKey(merchant)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	require.NoError(t, err)
	privatePath, publicPath := filepath.Join(t.TempDir(), "merchant.pem"), filepath.Join(t.TempDir(), "platform.pem")
	require.NoError(t, os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600))
	require.NoError(t, os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0600))
	sign := func(body, timestamp, nonce string) (string, error) {
		digest := sha256.Sum256([]byte(timestamp + "\n" + nonce + "\n" + body + "\n"))
		sig, err := rsa.SignPKCS1v15(rand.Reader, platform, crypto.SHA256, digest[:])
		return base64.StdEncoding.EncodeToString(sig), err
	}
	var creates atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/pay/transactions/native" {
			http.Error(w, "unexpected platform request", http.StatusBadRequest)
			return
		}
		creates.Add(1)
		body, timestamp, nonce := `{"code_url":"weixin://wxpay/bizpayurl?pr=fixture"}`, strconv.FormatInt(time.Now().Unix(), 10), "response-nonce"
		signature, err := sign(body, timestamp, nonce)
		if err != nil {
			http.Error(w, "fixture signing failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Wechatpay-Serial", "PUB_KEY_ID_TEST")
		w.Header().Set("Wechatpay-Timestamp", timestamp)
		w.Header().Set("Wechatpay-Nonce", nonce)
		w.Header().Set("Wechatpay-Signature", signature)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "api.mch.weixin.qq.com:443" {
			address = upstream.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	config := billingtest.WechatConfig{Enabled: true, AppID: "wx-test-app", MchID: "1900000001", CertificateSerial: "TEST_CERT_SERIAL", PrivateKeyPath: privatePath, PublicKeyID: "PUB_KEY_ID_TEST", PublicKeyPath: publicPath, APIv3Key: "0123456789abcdef0123456789abcdef", NotifyURL: "https://example.invalid/api/v1/user/payments/wechat/notify"}
	billingServer, callback, err := billingtest.NewIAMWechatStack(db, authz.NewClient("billing", identityv1.NewIAMServiceClient(connect("billing", identityEndpoint))), config)
	http.DefaultTransport = original
	require.NoError(t, err)
	billing := billingv1.NewBillingServiceClient(connect("identity", serve(billingServer)))
	identityHTTP := httptest.NewServer(identitytest.NewSelfBillingHTTP(identity, billing))
	t.Cleanup(identityHTTP.Close)
	callbackHTTP := httptest.NewServer(callback)
	t.Cleanup(callbackHTTP.Close)
	t.Setenv("BILLING_HTTP_ENDPOINT", callbackHTTP.URL)
	admin := admintest.NewPublicIdentityHTTP(identityHTTP.URL)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "wechat-recharge")
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusUnauthorized, request(http.MethodGet, "/api/user/payment/channels", "", "").Code)
	channels := request(http.MethodGet, "/api/user/payment/channels", "", session)
	require.Equal(t, http.StatusOK, channels.Code, channels.Body.String())
	require.Contains(t, channels.Body.String(), `"channel":"wechat"`)
	require.NotContains(t, channels.Body.String(), "api_v3_key")
	_, err = billing.ListPaymentChannels(context.Background(), &billingv1.ListPaymentChannelsRequest{})
	require.Error(t, err, "dedicated caller without user credential cannot list channels")
	created := request(http.MethodPost, "/api/user/pay", `{"amount":1.23,"payment_method":"wechat"}`, session)
	var reply struct {
		Success bool `json:"success"`
		Data    struct {
			Order *billingv1.PaymentOrder `json:"order"`
		} `json:"data"`
	}
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	require.NoError(t, jsonx.Unmarshal(created.Body.Bytes(), &reply))
	require.True(t, reply.Success, created.Body.String())
	require.Equal(t, "weixin://wxpay/bizpayurl?pr=fixture", reply.Data.Order.GetPayUrl())
	require.EqualValues(t, 123, reply.Data.Order.GetMoneyCents())
	require.Contains(t, request(http.MethodPost, "/api/user/pay", `{"amount":1.23,"payment_method":"wechat"}`, session).Body.String(), reply.Data.Order.GetTradeNo())
	require.EqualValues(t, 1, creates.Load())
	listed := request(http.MethodGet, "/api/user/payment/orders", "", session)
	require.Contains(t, listed.Body.String(), reply.Data.Order.GetTradeNo())
	plaintext, err := jsonx.Marshal(map[string]any{"appid": config.AppID, "mchid": config.MchID, "out_trade_no": reply.Data.Order.GetTradeNo(), "transaction_id": "4200000000001", "trade_type": "NATIVE", "trade_state": "SUCCESS", "amount": map[string]any{"total": 123, "currency": "CNY"}})
	require.NoError(t, err)
	block, err := aes.NewCipher([]byte(config.APIv3Key))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	encrypted := gcm.Seal(nil, []byte("123456789012"), plaintext, []byte("transaction"))
	body, err := jsonx.Marshal(map[string]any{"id": "wechat-event", "event_type": "TRANSACTION.SUCCESS", "resource_type": "encrypt-resource", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "original_type": "transaction", "nonce": "123456789012", "associated_data": "transaction", "ciphertext": base64.StdEncoding.EncodeToString(encrypted)}})
	require.NoError(t, err)
	for _, valid := range []bool{false, true, true} {
		timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), "callback-nonce"
		signature, err := sign(string(body), timestamp, nonce)
		require.NoError(t, err)
		if !valid {
			signature = "invalid-signature"
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/payments/wechat/notify", strings.NewReader(string(body)))
		for name, value := range map[string]string{"Wechatpay-Timestamp": timestamp, "Wechatpay-Nonce": nonce, "Wechatpay-Serial": config.PublicKeyID, "Wechatpay-Signature": signature} {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, req)
		var balance, count int64
		require.NoError(t, db.Table("users").Where("id = ?", member.ID).Pluck("balance", &balance).Error)
		require.NoError(t, db.Table("billing_ledgers").Where("user_id = ? AND type = ?", fmt.Sprint(member.ID), "recharge").Count(&count).Error)
		if valid {
			require.Equal(t, http.StatusNoContent, rec.Code)
			require.EqualValues(t, 123000, balance)
			require.EqualValues(t, 1, count)
		} else {
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, balance)
			require.Zero(t, count)
		}
	}
	paid := request(http.MethodGet, "/api/user/payment/orders/"+reply.Data.Order.GetTradeNo(), "", session)
	require.Equal(t, http.StatusOK, paid.Code, paid.Body.String())
	require.Contains(t, paid.Body.String(), `"status":"paid"`)
}
