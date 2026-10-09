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
