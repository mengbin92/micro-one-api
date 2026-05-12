package main

import (
	"strings"
	"testing"

	identitycfg "micro-one-api/internal/identity/config"
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
