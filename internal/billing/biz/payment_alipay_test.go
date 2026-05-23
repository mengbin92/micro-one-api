package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAlipayProviderReadsKeyFiles(t *testing.T) {
	dir := t.TempDir()
	privateKeyPath := filepath.Join(dir, "app_private_key.pem")
	publicKeyPath := filepath.Join(dir, "alipay_public_key.pem")
	if err := os.WriteFile(privateKeyPath, []byte(" private-key-from-file \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(" public-key-from-file \n"), 0o600); err != nil {
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
	if privateKey != "private-key-from-file" {
		t.Fatalf("private key = %q", privateKey)
	}

	publicKey, err := provider.publicKey()
	if err != nil {
		t.Fatal(err)
	}
	if publicKey != "public-key-from-file" {
		t.Fatalf("public key = %q", publicKey)
	}
}

func TestAlipayProviderQueryOrderPaid(t *testing.T) {
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
		PrivateKey: "test-private-key",
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
