package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type profileTransport func(*http.Request) (*http.Response, error)

func (f profileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGoogleVerifiedEmailClaim(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(fmt.Sprint(verified), func(t *testing.T) {
			p := NewGoogleProvider(Config{}).(*googleProvider)
			p.httpClient = &http.Client{Transport: profileTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"access_token":"access"}`
				if r.URL.Path == "/oauth2/v2/userinfo" {
					body = fmt.Sprintf(`{"id":"123","email":"alice@example.com","verified_email":%t}`, verified)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			user, err := p.Exchange(context.Background(), "code")
			require.NoError(t, err)
			require.Equal(t, verified, user.EmailVerified)
		})
	}
}

func TestGitHubEmailRejectsErrorResponse(t *testing.T) {
	p := NewGitHubProvider(Config{}).(*githubProvider)
	p.httpClient = &http.Client{Transport: profileTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(`[{"email":"alice@example.com","primary":true,"verified":true}]`))}, nil
	})}
	require.Empty(t, p.fetchGitHubEmail(context.Background(), "access"))
}

func TestOAuthExchangeRejectsInvalidProviderResponses(t *testing.T) {
	providers := []struct {
		name string
		new  func(*http.Client) Provider
		user string
	}{
		{"github", func(c *http.Client) Provider {
			p := NewGitHubProvider(Config{}).(*githubProvider)
			p.httpClient = c
			return p
		}, `{"id":123,"login":"alice"}`},
		{"google", func(c *http.Client) Provider {
			p := NewGoogleProvider(Config{}).(*googleProvider)
			p.httpClient = c
			return p
		}, `{"id":"123","email":"alice@example.com"}`},
		{"oidc", func(c *http.Client) Provider {
			p := NewOIDCProvider(OIDCConfig{TokenURL: "https://idp.example/token", UserInfoURL: "https://idp.example/user"}).(*oidcProvider)
			p.httpClient = c
			return p
		}, `{"sub":"123"}`},
		{"lark", func(c *http.Client) Provider {
			p := NewLarkProvider(EndpointConfig{}).(*larkProvider)
			p.httpClient = c
			return p
		}, `{"union_id":"123"}`},
		{"wechat", func(c *http.Client) Provider {
			p := NewWeChatProvider(EndpointConfig{}).(*wechatProvider)
			p.httpClient = c
			return p
		}, `{"openid":"123"}`},
	}
	for _, provider := range providers {
		for _, scenario := range []string{"token_status", "missing_token", "user_status", "missing_identity", "success"} {
			t.Run(provider.name+"/"+scenario, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: profileTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					statusCode := http.StatusOK
					body := `{"access_token":"access","openid":"123"}`
					if calls == 1 {
						if scenario == "token_status" {
							statusCode = http.StatusUnauthorized
						}
						if scenario == "missing_token" {
							body = `{}`
						}
						if provider.name == "wechat" && scenario == "missing_identity" {
							body = `{"access_token":"access"}`
						}
					} else {
						body = provider.user
						if scenario == "user_status" {
							statusCode = http.StatusServiceUnavailable
						}
						if scenario == "missing_identity" {
							body = `{}`
						}
						if calls > 2 {
							body = `[]`
						}
					}
					return &http.Response{StatusCode: statusCode, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				user, err := provider.new(client).Exchange(context.Background(), "code")
				if scenario == "success" {
					require.NoError(t, err)
					require.Equal(t, "123", user.ProviderID)
					return
				}
				require.Error(t, err)
				require.Nil(t, user)
				if scenario == "token_status" || scenario == "missing_token" {
					require.Equal(t, 1, calls, "invalid token exchange must stop before fetching a profile")
				}
			})
		}
	}
}
