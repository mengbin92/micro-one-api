package oauth

import (
	"net/url"
	"strings"
	"testing"
)

func TestOIDCProviderAuthURL(t *testing.T) {
	provider := NewOIDCProvider(OIDCConfig{
		Config: Config{
			ClientID:    "client-id",
			RedirectURL: "https://one-api.example.com/v1/oauth/oidc/callback",
		},
		AuthorizeURL: "https://idp.example.com/oauth2/authorize",
		Scopes:       []string{"openid", "email", "profile"},
	})

	if provider.Name() != "oidc" {
		t.Fatalf("provider name = %q, want oidc", provider.Name())
	}
	authURL := provider.AuthURL("state-123")
	if !strings.HasPrefix(authURL, "https://idp.example.com/oauth2/authorize?") {
		t.Fatalf("auth url prefix mismatch: %s", authURL)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"client_id":     "client-id",
		"redirect_uri":  "https://one-api.example.com/v1/oauth/oidc/callback",
		"response_type": "code",
		"scope":         "openid email profile",
		"state":         "state-123",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("query %s = %q, want %q; url=%s", key, got, want, authURL)
		}
	}
}
