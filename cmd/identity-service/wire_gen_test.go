package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"micro-one-api/internal/identity/biz"
	identitycfg "micro-one-api/internal/identity/config"
	applogger "micro-one-api/internal/pkg/logger"
)

func TestRegistrationPolicyFromConfigDefaultsEnabled(t *testing.T) {
	policy := registrationPolicyFromConfig(&identitycfg.Config{})

	if !policy.Enabled {
		t.Fatal("registration should default to enabled")
	}
}

func TestRegistrationPolicyFromConfigSupportsRestrictionsAndExplicitDisable(t *testing.T) {
	policy := registrationPolicyFromConfig(&identitycfg.Config{
		Registration: identitycfg.RegistrationConfig{
			Disabled:                      true,
			EmailDomainRestrictionEnabled: true,
			EmailDomainWhitelist:          []string{"example.com"},
			TurnstileCheckEnabled:         true,
			TurnstileSecret:               "secret",
		},
	})

	if policy.Enabled {
		t.Fatal("registration should be disabled")
	}
	if !policy.EmailDomainRestrictionEnabled || policy.EmailDomainWhitelist[0] != "example.com" {
		t.Fatalf("email domain policy mismatch: %+v", policy)
	}
	if !policy.TurnstileCheckEnabled || policy.TurnstileSecret != "secret" {
		t.Fatalf("turnstile policy mismatch: %+v", policy)
	}
}

func TestLogGeneratedAdminPasswordWritesPrivateFileWithoutLoggingSecret(t *testing.T) {
	previous := applogger.Log
	t.Cleanup(func() {
		applogger.Log = previous
	})
	var logs bytes.Buffer
	if err := applogger.SetOutput(&logs); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "initial-admin-password.txt")
	t.Setenv("INITIAL_ADMIN_PASSWORD_FILE", path)

	logGeneratedAdminPassword(&biz.BootstrapResult{
		Username:      "admin",
		Email:         "admin@example.com",
		PlainPassword: "secret-pass",
		Generated:     true,
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "secret-pass" {
		t.Fatalf("password file content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("password file mode = %o, want 0600", got)
	}
	if strings.Contains(logs.String(), "secret-pass") {
		t.Fatalf("generated password was logged: %s", logs.String())
	}
}

func TestWriteGeneratedPasswordFileRejectsUnsafePaths(t *testing.T) {
	if err := writeGeneratedPasswordFile("relative-password.txt", "secret-pass"); err == nil {
		t.Fatal("writeGeneratedPasswordFile accepted a relative path")
	}
	if err := writeGeneratedPasswordFile(t.TempDir(), "secret-pass"); err == nil {
		t.Fatal("writeGeneratedPasswordFile accepted a directory path")
	}
}

func TestSetupOAuthRegistersOIDCWhenConfigured(t *testing.T) {
	registry := setupOAuth(&identitycfg.Config{
		OAuth: identitycfg.OAuthConfig{
			BaseURL: "https://one-api.example.com",
			OIDC: identitycfg.OIDCProviderConfig{
				Enabled:      true,
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				AuthorizeURL: "https://idp.example.com/oauth2/authorize",
				TokenURL:     "https://idp.example.com/oauth2/token",
				UserInfoURL:  "https://idp.example.com/oauth2/userinfo",
				Scopes:       []string{"openid", "email"},
			},
		},
	})

	provider, ok := registry.Get("oidc")
	if !ok {
		t.Fatal("oidc provider was not registered")
	}
	if got := provider.AuthURL("state-123"); !strings.Contains(got, "idp.example.com/oauth2/authorize") || !strings.Contains(got, "redirect_uri=https%3A%2F%2Fone-api.example.com%2Fv1%2Foauth%2Foidc%2Fcallback") {
		t.Fatalf("oidc auth url mismatch: %s", got)
	}
}

func TestSetupOAuthRegistersLarkAndWeChatWhenConfigured(t *testing.T) {
	registry := setupOAuth(&identitycfg.Config{
		OAuth: identitycfg.OAuthConfig{
			BaseURL: "https://one-api.example.com",
			Lark: identitycfg.OAuthProviderConfig{
				Enabled:      true,
				ClientID:     "lark-client",
				ClientSecret: "lark-secret",
			},
			WeChat: identitycfg.OAuthProviderConfig{
				Enabled:      true,
				ClientID:     "wechat-client",
				ClientSecret: "wechat-secret",
			},
		},
	})

	lark, ok := registry.Get("lark")
	if !ok {
		t.Fatal("lark provider was not registered")
	}
	if got := lark.AuthURL("state-123"); !strings.Contains(got, "passport.feishu.cn/suite/passport/oauth/authorize") || !strings.Contains(got, "redirect_uri=https%3A%2F%2Fone-api.example.com%2Fv1%2Foauth%2Flark%2Fcallback") {
		t.Fatalf("lark auth url mismatch: %s", got)
	}
	wechat, ok := registry.Get("wechat")
	if !ok {
		t.Fatal("wechat provider was not registered")
	}
	if got := wechat.AuthURL("state-123"); !strings.Contains(got, "open.weixin.qq.com/connect/qrconnect") || !strings.Contains(got, "redirect_uri=https%3A%2F%2Fone-api.example.com%2Fv1%2Foauth%2Fwechat%2Fcallback") {
		t.Fatalf("wechat auth url mismatch: %s", got)
	}
}
