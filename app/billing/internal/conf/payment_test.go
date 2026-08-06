package conf

import (
	"testing"
)

func TestPaymentToPaymentConfig(t *testing.T) {
	// Test with minimal config
	t.Run("minimal config", func(t *testing.T) {
		p := &Payment{
			AmountPerUnit: 100,
		}
		cfg := p.ToPaymentConfig()

		if cfg.AmountPerUnit != 100 {
			t.Fatalf("AmountPerUnit = %d, want 100", cfg.AmountPerUnit)
		}
		if cfg.Alipay.Enabled {
			t.Fatal("Alipay should be disabled when not configured")
		}
	})

	// Test with full Alipay config
	t.Run("full alipay config", func(t *testing.T) {
		p := &Payment{
			AmountPerUnit: 50,
			Alipay: &Alipay{
				Enabled:              true,
				FormUrl:              "https://openapi.alipay.com/gateway.do",
				AppId:                "test-app-id",
				PrivateKey:           "test-private-key",
				PrivateKeyPath:       "/path/to/private.pem",
				PublicKey:            "test-public-key",
				PublicKeyPath:        "/path/to/public.pem",
				NotifyUrl:            "https://example.com/notify",
				ReturnUrl:            "https://example.com/return",
				AppCertPath:          "/path/to/appCert.pem",
				RootCertPath:         "/path/to/rootCert.pem",
				AlipayPublicCertPath: "/path/to/alipayPublicCert.pem",
			},
		}
		cfg := p.ToPaymentConfig()

		if cfg.AmountPerUnit != 50 {
			t.Fatalf("AmountPerUnit = %d, want 50", cfg.AmountPerUnit)
		}

		alipay := cfg.Alipay
		if !alipay.Enabled {
			t.Fatal("Alipay.Enabled = false, want true")
		}
		if alipay.FormURL != "https://openapi.alipay.com/gateway.do" {
			t.Fatalf("Alipay.FormURL = %q, want https://openapi.alipay.com/gateway.do", alipay.FormURL)
		}
		if alipay.AppID != "test-app-id" {
			t.Fatalf("Alipay.AppID = %q, want test-app-id", alipay.AppID)
		}
		if alipay.PrivateKey != "test-private-key" {
			t.Fatalf("Alipay.PrivateKey = %q, want test-private-key", alipay.PrivateKey)
		}
		if alipay.NotifyURL != "https://example.com/notify" {
			t.Fatalf("Alipay.NotifyURL = %q, want https://example.com/notify", alipay.NotifyURL)
		}
		if alipay.ReturnURL != "https://example.com/return" {
			t.Fatalf("Alipay.ReturnURL = %q, want https://example.com/return", alipay.ReturnURL)
		}
	})

	// Test with nil Alipay
	t.Run("nil Alipay", func(t *testing.T) {
		p := &Payment{
			AmountPerUnit: 100,
			Alipay:        nil,
		}
		cfg := p.ToPaymentConfig()

		if cfg.AmountPerUnit != 100 {
			t.Fatalf("AmountPerUnit = %d, want 100", cfg.AmountPerUnit)
		}
		if cfg.Alipay.Enabled {
			t.Fatal("Alipay should be disabled when nil")
		}
	})

	// Test field name mapping (proto snake_case to Go CamelCase)
	t.Run("field name mapping", func(t *testing.T) {
		p := &Payment{
			AmountPerUnit: 100,
			Alipay: &Alipay{
				AppId:     "test-id",  // proto: app_id → Go: AppID
				FormUrl:   "test-url", // proto: form_url → Go: FormURL
				NotifyUrl: "notify",   // proto: notify_url → Go: NotifyURL
				ReturnUrl: "return",   // proto: return_url → Go: ReturnURL
			},
		}
		cfg := p.ToPaymentConfig()

		// Verify field name mapping worked correctly
		if cfg.Alipay.AppID != "test-id" {
			t.Fatalf("AppID = %q, want test-id", cfg.Alipay.AppID)
		}
		if cfg.Alipay.FormURL != "test-url" {
			t.Fatalf("FormURL = %q, want test-url", cfg.Alipay.FormURL)
		}
		if cfg.Alipay.NotifyURL != "notify" {
			t.Fatalf("NotifyURL = %q, want notify", cfg.Alipay.NotifyURL)
		}
		if cfg.Alipay.ReturnURL != "return" {
			t.Fatalf("ReturnURL = %q, want return", cfg.Alipay.ReturnURL)
		}
	})
}

func TestPaymentConfigMatchesBizShape(t *testing.T) {
	// This test ensures the proto-generated Payment shape
	// matches biz.PaymentConfig struct layout.
	t.Run("shape compatibility", func(t *testing.T) {
		p := &Payment{
			AmountPerUnit: 100,
			Alipay: &Alipay{
				Enabled:              true,
				AppCertPath:          "app",
				AlipayPublicCertPath: "alipay",
				AppId:                "id",
				FormUrl:              "form",
				NotifyUrl:            "notify",
				PrivateKey:           "priv",
				PrivateKeyPath:       "privpath",
				PublicKey:            "pub",
				PublicKeyPath:        "pubpath",
				ReturnUrl:            "return",
				RootCertPath:         "root",
			},
		}

		cfg := p.ToPaymentConfig()

		// Verify all fields are transferred correctly
		// This catches any proto field rename or struct layout mismatch
		if cfg.AmountPerUnit != 100 {
			t.Errorf("AmountPerUnit mismatch")
		}
		if !cfg.Alipay.Enabled {
			t.Errorf("Enabled mismatch")
		}
		if cfg.Alipay.AppCertPath != "app" {
			t.Errorf("AppCertPath mismatch")
		}
		if cfg.Alipay.AlipayPublicCertPath != "alipay" {
			t.Errorf("AlipayPublicCertPath mismatch")
		}
		if cfg.Alipay.AppID != "id" {
			t.Errorf("AppID mismatch")
		}
		if cfg.Alipay.FormURL != "form" {
			t.Errorf("FormURL mismatch")
		}
		if cfg.Alipay.NotifyURL != "notify" {
			t.Errorf("NotifyURL mismatch")
		}
		if cfg.Alipay.PrivateKey != "priv" {
			t.Errorf("PrivateKey mismatch")
		}
		if cfg.Alipay.PrivateKeyPath != "privpath" {
			t.Errorf("PrivateKeyPath mismatch")
		}
		if cfg.Alipay.PublicKey != "pub" {
			t.Errorf("PublicKey mismatch")
		}
		if cfg.Alipay.PublicKeyPath != "pubpath" {
			t.Errorf("PublicKeyPath mismatch")
		}
		if cfg.Alipay.ReturnURL != "return" {
			t.Errorf("ReturnURL mismatch")
		}
		if cfg.Alipay.RootCertPath != "root" {
			t.Errorf("RootCertPath mismatch")
		}
	})
}
