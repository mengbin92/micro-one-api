package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"micro-one-api/internal/channel/biz"
)

type fakeChannelUsecase struct {
	created *biz.SubscriptionAccount
}

func (f *fakeChannelUsecase) CreateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	account.ID = 42
	f.created = account
	return nil
}

func TestAuthURLBuildsCodexPKCESession(t *testing.T) {
	now := time.Unix(1000, 0)
	svc := NewService(&fakeChannelUsecase{}, WithNow(func() time.Time { return now }))

	result, err := svc.AuthURL(context.Background(), PlatformCodex, AuthURLRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, result.AuthURL)
	require.NotEmpty(t, result.SessionID)
	assert.Equal(t, now.Add(defaultSessionTTL).Unix(), result.ExpiresAt)

	u := mustParseURL(t, result.AuthURL)
	q := u.Query()
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, codexRedirectURI, q.Get("redirect_uri"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, "true", q.Get("codex_cli_simplified_flow"))
	assert.Equal(t, result.State, q.Get("state"))

	session, ok := svc.store.Pop(result.SessionID, now)
	require.True(t, ok)
	assert.Equal(t, PlatformCodex, session.Platform)
	assert.Len(t, session.CodeVerifier, 128)
}

func TestExchangeRejectsInvalidState(t *testing.T) {
	now := time.Unix(1000, 0)
	svc := NewService(&fakeChannelUsecase{}, WithNow(func() time.Time { return now }))
	result, err := svc.AuthURL(context.Background(), PlatformClaude, AuthURLRequest{})
	require.NoError(t, err)

	_, err = svc.Exchange(context.Background(), PlatformClaude, ExchangeRequest{
		SessionID: result.SessionID,
		State:     "wrong",
		Code:      "code",
	})
	require.ErrorIs(t, err, ErrInvalidSession)
}

func TestExchangeCodexCreatesAccountAndBestEffortPrivacy(t *testing.T) {
	now := time.Unix(1000, 0)
	var tokenHit, accountHit, privacyHit bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenHit = true
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "authorization_code", r.PostForm.Get("grant_type"))
			assert.Equal(t, "code-123", r.PostForm.Get("code"))
			assert.NotEmpty(t, r.PostForm.Get("code_verifier"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600,"id_token":"` + fakeIDToken("acct-id", "") + `"}`))
		case "/accounts/check/v4-2023-04-27":
			accountHit = true
			assert.Equal(t, "Bearer access", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accounts":{"acct-id":{"plan_type":"plus","account":{"email":"a@example.com","is_default":true},"entitlement":{"expires_at":"2026-12-31T00:00:00Z"}}}}`))
		case "/settings/user":
			privacyHit = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	oldAccountsURL := chatGPTAccountsCheckURL
	oldPrivacyURL := chatGPTPrivacyURL
	chatGPTAccountsCheckURL = ts.URL + "/accounts/check/v4-2023-04-27"
	chatGPTPrivacyURL = ts.URL + "/settings/user"
	t.Cleanup(func() {
		chatGPTAccountsCheckURL = oldAccountsURL
		chatGPTPrivacyURL = oldPrivacyURL
	})

	uc := &fakeChannelUsecase{}
	svc := NewService(
		uc,
		WithNow(func() time.Time { return now }),
		WithHTTPClient(ts.Client()),
		WithTokenURL(PlatformCodex, ts.URL+"/token"),
	)
	auth, err := svc.AuthURL(context.Background(), PlatformCodex, AuthURLRequest{})
	require.NoError(t, err)

	result, err := svc.Exchange(context.Background(), PlatformCodex, ExchangeRequest{
		SessionID: auth.SessionID,
		State:     auth.State,
		Code:      "code-123",
		Name:      "codex-pro",
		Group:     "vip",
		Models:    "gpt-5",
		Priority:  10,
		Metadata:  `{"source":"oauth"}`,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 42, result.AccountID)
	require.NotNil(t, uc.created)
	assert.Equal(t, "codex-pro", uc.created.Name)
	assert.Equal(t, "codex", uc.created.Platform)
	assert.Equal(t, "oauth", uc.created.AccountType)
	assert.Equal(t, "vip", uc.created.Group)
	assert.Equal(t, []string{"gpt-5"}, uc.created.Models)
	assert.Equal(t, "access", uc.created.AccessToken)
	assert.Equal(t, "refresh", uc.created.RefreshToken)
	assert.Equal(t, now.Add(time.Hour).Unix(), uc.created.ExpiresAt)
	assert.Equal(t, "acct-id", uc.created.AccountID)
	assert.True(t, tokenHit)
	assert.True(t, accountHit)
	assert.True(t, privacyHit)
	assert.Contains(t, uc.created.Metadata, `"plan_type":"plus"`)
	assert.Contains(t, uc.created.Metadata, `"source":"oauth"`)
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func fakeIDToken(accountID, planType string) string {
	claims := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  planType,
		},
	}
	payload, _ := json.Marshal(claims)
	return strings.Join([]string{
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)),
		base64.RawURLEncoding.EncodeToString(payload),
		"",
	}, ".")
}
