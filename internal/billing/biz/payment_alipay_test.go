package biz

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	privateKeyPEM, _ := testAlipayKeyPairPEM(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("method"); got != "alipay.trade.query" {
			t.Fatalf("method = %q", got)
		}
		if got := r.URL.Query().Get("app_id"); got != "app-1" {
			t.Fatalf("app_id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"alipay_trade_query_response":{"code":"10000","msg":"Success","out_trade_no":"PAY-1","trade_no":"ALI-1","trade_status":"TRADE_SUCCESS"}}`))
	}))
	defer server.Close()

	provider := NewAlipayPaymentProvider(AlipayConfig{
		Enabled:    true,
		FormURL:    server.URL,
		AppID:      "app-1",
		PrivateKey: privateKeyPEM,
	})
	status, err := provider.QueryOrder(context.Background(), &PaymentOrder{TradeNo: "PAY-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Paid {
		t.Fatalf("paid = false")
	}
	if status.ProviderTradeNo != "ALI-1" {
		t.Fatalf("provider_trade_no = %q", status.ProviderTradeNo)
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
