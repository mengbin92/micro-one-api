package biz

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentConfigUnmarshalAmountPerUnit(t *testing.T) {
	var cfg PaymentConfig
	if err := json.Unmarshal([]byte(`{"amount_per_unit":10000,"quota_per_unit":5000}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AmountPerUnit != 10000 {
		t.Fatalf("amount_per_unit = %d", cfg.AmountPerUnit)
	}
}

func TestPaymentConfigUnmarshalLegacyQuotaPerUnit(t *testing.T) {
	var cfg PaymentConfig
	if err := json.Unmarshal([]byte(`{"quota_per_unit":5000}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AmountPerUnit != 5000 {
		t.Fatalf("amount_per_unit = %d", cfg.AmountPerUnit)
	}
}

func TestAlipayProviderReadsKeyFiles(t *testing.T) {
	dir := t.TempDir()
	privateKeyPath := filepath.Join(dir, "app_private_key.pem")
	publicKeyPath := filepath.Join(dir, "alipay_public_key.pem")
	privateKeyPEM, publicKeyPEM := testAlipayKeyPairPEM(t)
	if err := os.WriteFile(privateKeyPath, []byte(" "+privateKeyPEM+" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(" "+publicKeyPEM+" \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	provider := NewAlipayPaymentProvider(AlipayConfig{
		PrivateKeyPath: privateKeyPath,
		PublicKeyPath:  publicKeyPath,
	})

	privateKey, err := provider.privateKey()
	if err != nil {
		t.Fatal(err)
	}
	if privateKey != strings.TrimSpace(privateKeyPEM) {
		t.Fatalf("private key = %q", privateKey)
	}

	publicKey, err := provider.publicKey()
	if err != nil {
		t.Fatal(err)
	}
	if publicKey != strings.TrimSpace(publicKeyPEM) {
		t.Fatalf("public key = %q", publicKey)
	}
}

func TestReadPaymentSecretFileRejectsDirectory(t *testing.T) {
	_, err := ReadPaymentSecretFile("@"+t.TempDir(), "private key")
	if err == nil {
		t.Fatal("expected directory read to fail")
	}
}

func TestAlipayProviderQueryOrderPaid(t *testing.T) {
	privateKeyPEM, publicKeyPEM := testAlipayKeyPairPEM(t)
	const raw = `{"code":"10000","msg":"Success","out_trade_no":"PAY-1","trade_no":"ALI-1","trade_status":"TRADE_SUCCESS","total_amount":"1.23"}`
	signature, err := signRSA2(raw, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("method"); got != "alipay.trade.query" {
			t.Fatalf("method = %q", got)
		}
		if got := r.URL.Query().Get("app_id"); got != "app-1" {
			t.Fatalf("app_id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"alipay_trade_query_response":%s,"sign":%q}`, raw, signature)
	}))
	defer server.Close()

	provider := NewAlipayPaymentProvider(AlipayConfig{
		Enabled:    true,
		FormURL:    server.URL,
		AppID:      "app-1",
		PrivateKey: privateKeyPEM, PublicKey: publicKeyPEM,
	})
	status, err := provider.QueryOrder(context.Background(), &PaymentOrder{TradeNo: "PAY-1"})
	if err != nil {
		t.Fatal(err)
	}
	if status.TotalAmount != 123 {
		t.Fatalf("total amount = %d", status.TotalAmount)
	}
	if !status.Paid {
		t.Fatalf("paid = false")
	}
	if status.ProviderTradeNo != "ALI-1" {
		t.Fatalf("provider_trade_no = %q", status.ProviderTradeNo)
	}
}

func TestAlipayQueryRequiresAuthenticatedResponse(t *testing.T) {
	privateKey, publicKey := testAlipayKeyPairPEM(t)
	const raw = `{"code":"10000"}`
	signature, err := signRSA2(raw, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, key, signature string }{
		{"missing signature", publicKey, ""},
		{"missing verification key", "", signature},
		{"invalid signature", publicKey, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewAlipayPaymentProvider(AlipayConfig{PublicKey: tc.key})
			body := fmt.Sprintf(`{"alipay_trade_query_response":%s,"sign":%q}`, raw, tc.signature)
			if _, err := provider.parseTradeQueryResponse([]byte(body)); err == nil {
				t.Fatal("expected response verification error")
			}
		})
	}
}

func TestAlipayPlatformCertificateOverridesDefaultPublicKeyPath(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	path := filepath.Join(dir, "platform.crt")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	provider := NewAlipayPaymentProvider(AlipayConfig{AlipayPublicCertPath: path, PublicKeyPath: filepath.Join(dir, "alipay_public_key.pem")})
	payload := `{"code":"10000","trade_status":"TRADE_SUCCESS","total_amount":"1.00"}`
	signature, err := signRSA2(payload, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})))
	require.NoError(t, err)
	body := []byte(fmt.Sprintf(`{"alipay_trade_query_response":%s,"sign":%q}`, payload, signature))
	order, err := provider.parseTradeQueryResponse(body)
	require.NoError(t, err)
	require.True(t, order.Paid)
	// An explicitly selected but unreadable certificate must not fall back to a bare key.
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	provider.config.PublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	provider.config.AlipayPublicCertPath = filepath.Join(dir, "missing.crt")
	_, err = provider.parseTradeQueryResponse(body)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAlipayOrderPreservesCentAmount(t *testing.T) {
	privateKey, _ := testAlipayKeyPairPEM(t)
	provider := NewAlipayPaymentProvider(AlipayConfig{Enabled: true, FormURL: "https://example.test", AppID: "test", PrivateKey: privateKey, NotifyURL: "https://example.test/notify"})
	order, err := provider.CreateOrder(context.Background(), &PaymentOrder{TradeNo: "PAY-1", MoneyCents: 9007199254740993})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(order.PayURL)
	if err != nil {
		t.Fatal(err)
	}
	var content struct {
		TotalAmount string `json:"total_amount"`
	}
	if err := json.Unmarshal([]byte(u.Query().Get("biz_content")), &content); err != nil {
		t.Fatal(err)
	}
	if content.TotalAmount != "90071992547409.93" {
		t.Fatalf("total amount = %s", content.TotalAmount)
	}
}

func TestAlipayProviderRejectsNonPEMPrivateKey(t *testing.T) {
	provider := NewAlipayPaymentProvider(AlipayConfig{
		Enabled:    true,
		FormURL:    "https://openapi-sandbox.dl.alipaydev.com/gateway.do",
		AppID:      "app-1",
		PrivateKey: "test-private-key",
	})
	_, err := provider.QueryOrder(context.Background(), &PaymentOrder{TradeNo: "PAY-1"})
	if err == nil {
		t.Fatal("expected non-PEM private key error")
	}
}

func TestAlipayRejectsNonPEMPublicKey(t *testing.T) {
	err := verifyRSA2("content", "signature", "test-public-key")
	if err == nil {
		t.Fatal("expected non-PEM public key error")
	}
}

func TestAlipayRSA2WithPEM(t *testing.T) {
	privateKeyPEM, publicKeyPEM := testAlipayKeyPairPEM(t)
	signature, err := signRSA2("content", strings.ReplaceAll(privateKeyPEM, "\n", `\n`))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyRSA2("content", signature, strings.ReplaceAll(publicKeyPEM, "\n", `\n`)); err != nil {
		t.Fatal(err)
	}
}

func testAlipayKeyPairPEM(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER})
	return string(privateKeyPEM), string(publicKeyPEM)
}

// TestParseAlipayTotalAmount covers the code-review 2026-07-30 billing-L5
// helper that converts Alipay's decimal-yuan "total_amount" notify field into
// minor units (cents). The result drives the amount cross-check in
// HandleAlipayNotify.
func TestParseAlipayTotalAmount(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"exact yuan", "1.00", 100},
		{"fractional cents round", "0.015", 2}, // math.Round(1.5) = 2
		{"sub-cent truncates via round", "0.004", 0},
		{"large amount", "99.99", 9999},
		{"empty", "", 0},
		{"garbage", "not-a-number", 0},
		{"overflow", "92233720368547758.08", 0},
		{"exact large cents", "90071992547409.91", 9007199254740991},
		{"non-finite", "NaN", 0},
		{"infinite", "Inf", 0},
		{"scientific notation", "1e6", 0},
		{"negative rejected", "-1.00", 0},
		{"whitespace trimmed", "  0.50  ", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAlipayTotalAmount(tc.raw); got != tc.want {
				t.Fatalf("parseAlipayTotalAmount(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
