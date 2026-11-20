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

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/credential"
)

type fakeChannelUsecase struct {
	created       *biz.SubscriptionAccount
	createAuthErr error
}

const registeredKimiClientID = "micro-one-registered-client"

type kimiDeviceTestTransport http.HandlerFunc

func (handle kimiDeviceTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	defer r.Body.Close()
	w := httptest.NewRecorder()
	handle(w, r)
	return w.Result(), nil
}

func (f *fakeChannelUsecase) CreateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	account.ID = 42
	f.created = account
	return nil
}

func (f *fakeChannelUsecase) AuthorizeSubscriptionAccountCreate(ctx context.Context, _ string) (context.Context, error) {
	return ctx, f.createAuthErr
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

func TestKimiDeviceAuthorizationPollsOnceAndBindsToOriginalGroup(t *testing.T) {
	now := time.Unix(1000, 0)
	ctx := context.Background()
	attempts := 0
	client := &http.Client{Transport: kimiDeviceTestTransport(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, registeredKimiClientID, r.PostForm.Get("client_id"))
		switch r.URL.Path {
		case "/authorize-device":
			_, _ = w.Write([]byte(`{"device_code":"server-secret","user_code":"VISIBLE","verification_uri_complete":"https://auth.kimi.com/device?code=VISIBLE","expires_in":120,"interval":5}`))
		case "/token":
			attempts++
			require.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", r.PostForm.Get("grant_type"))
			require.Equal(t, "server-secret", r.PostForm.Get("device_code"))
			if attempts == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"slow_down"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-fixture","refresh_token":"refresh-fixture","expires_in":3600}`))
		default:
			t.Errorf("unexpected OAuth request path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})}

	accounts := &fakeChannelUsecase{}
	svc := NewService(accounts, WithKimiDeviceClientID(registeredKimiClientID), WithNow(func() time.Time { return now }), WithTokenURL(PlatformKimi, "https://oauth.example/token"), WithHTTPClient(client))
	svc.deviceURL = "https://oauth.example/authorize-device"
	reply, err := svc.AuthURL(ctx, PlatformKimi, AuthURLRequest{Group: " coding "})
	require.NoError(t, err)
	require.Equal(t, "VISIBLE", reply.UserCode)
	require.NotContains(t, reply.AuthURL, "server-secret")
	require.Equal(t, now.Add(120*time.Second).Unix(), reply.ExpiresAt)

	req := ExchangeRequest{SessionID: reply.SessionID, State: reply.State, Group: "other"}
	_, err = svc.Exchange(ctx, PlatformKimi, req)
	require.ErrorIs(t, err, ErrInvalidSession)
	req.Group = "coding"
	_, err = svc.Exchange(ctx, PlatformKimi, req)
	require.ErrorIs(t, err, ErrDevicePending)
	require.Zero(t, attempts)

	now = now.Add(5 * time.Second)
	accounts.createAuthErr = authorization.ErrDenied
	_, err = svc.Exchange(ctx, PlatformKimi, req)
	require.ErrorIs(t, err, authorization.ErrDenied)
	require.Zero(t, attempts, "a caller without account creation permission cannot consume the grant")
	accounts.createAuthErr = nil
	_, err = svc.Exchange(ctx, PlatformKimi, req)
	require.ErrorIs(t, err, ErrDevicePending)
	require.Equal(t, 1, attempts)
	now = now.Add(9 * time.Second)
	_, err = svc.Exchange(ctx, PlatformKimi, req)
	require.ErrorIs(t, err, ErrDevicePending)
	require.Equal(t, 1, attempts, "slow_down extends the next polling interval")
	require.Nil(t, accounts.created)

	now = now.Add(time.Second)
	result, err := svc.Exchange(ctx, PlatformKimi, req)
	require.NoError(t, err)
	require.EqualValues(t, 42, result.AccountID)
	require.Equal(t, PlatformKimi, accounts.created.Platform)
	require.Equal(t, "oauth", accounts.created.AccountType)
	require.Equal(t, "coding", accounts.created.Group)
	require.Equal(t, credential.KimiCodingBaseURL, accounts.created.BaseURL)
	require.Equal(t, "refresh-fixture", accounts.created.RefreshToken)
	require.Equal(t, now.Add(time.Hour).Unix(), accounts.created.ExpiresAt)
	_, err = svc.Exchange(ctx, PlatformKimi, req)
	require.ErrorIs(t, err, ErrInvalidSession)
	require.Equal(t, 2, attempts)
}

func TestKimiDeviceVerificationAllowsManualEntryLink(t *testing.T) {
	client := &http.Client{Transport: kimiDeviceTestTransport(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"private","user_code":"VISIBLE","verification_uri":"https://www.kimi.com/code/authorize_device","expires_in":120}`))
	})}
	svc := NewService(&fakeChannelUsecase{}, WithKimiDeviceClientID(registeredKimiClientID), WithHTTPClient(client))
	result, err := svc.AuthURL(t.Context(), PlatformKimi, AuthURLRequest{})
	require.NoError(t, err)
	require.Equal(t, "https://www.kimi.com/code/authorize_device", result.AuthURL)
	require.Equal(t, "VISIBLE", result.UserCode)
}

func TestKimiDeviceConcurrentPollRemainsPending(t *testing.T) {
	now := time.Unix(1000, 0)
	started := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{Transport: kimiDeviceTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/oauth/device_authorization" {
			_, _ = w.Write([]byte(`{"device_code":"private","user_code":"VISIBLE","verification_uri_complete":"https://www.kimi.com/code/authorize_device","expires_in":120,"interval":1}`))
			return
		}
		close(started)
		<-release
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	})}
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	svc := NewService(&fakeChannelUsecase{}, WithKimiDeviceClientID(registeredKimiClientID), WithNow(func() time.Time { return now }), WithHTTPClient(client))
	result, err := svc.AuthURL(t.Context(), PlatformKimi, AuthURLRequest{})
	require.NoError(t, err)
	now = now.Add(time.Second)
	req := ExchangeRequest{SessionID: result.SessionID, State: result.State}
	polled := make(chan error, 1)
	go func() {
		_, err := svc.Exchange(t.Context(), PlatformKimi, req)
		polled <- err
	}()
	<-started
	_, err = svc.Exchange(t.Context(), PlatformKimi, req)
	require.ErrorIs(t, err, ErrDevicePending)
	close(release)
	released = true
	require.ErrorIs(t, <-polled, ErrDevicePending)
}

func TestKimiDeviceRequiresRegisteredClient(t *testing.T) {
	for _, id := range []string{"", "17e5f671-d194-4dfb-9706-5516cb48c098", "kimi-coding-cli"} {
		client := &http.Client{Transport: kimiDeviceTestTransport(func(http.ResponseWriter, *http.Request) {
			t.Fatal("unregistered client reached the provider")
		})}
		svc := NewService(&fakeChannelUsecase{}, WithKimiDeviceClientID(id), WithHTTPClient(client))
		_, err := svc.AuthURL(t.Context(), PlatformKimi, AuthURLRequest{})
		require.ErrorIs(t, err, ErrKimiDeviceUnavailable)
	}
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

func TestExchangeAcceptsFullCallbackURL(t *testing.T) {
	now := time.Unix(1000, 0)
	var exchangedCode string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		exchangedCode = r.PostForm.Get("code")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
	}))
	defer ts.Close()

	uc := &fakeChannelUsecase{}
	svc := NewService(
		uc,
		WithNow(func() time.Time { return now }),
		WithHTTPClient(ts.Client()),
		WithTokenURL(PlatformClaude, ts.URL),
	)
	auth, err := svc.AuthURL(context.Background(), PlatformClaude, AuthURLRequest{})
	require.NoError(t, err)

	_, err = svc.Exchange(context.Background(), PlatformClaude, ExchangeRequest{
		SessionID: auth.SessionID,
		State:     auth.State,
		Code:      "http://localhost:1455/auth/callback?code=code-123&state=" + url.QueryEscape(auth.State),
		Name:      "claude-pro",
	})
	require.NoError(t, err)
	assert.Equal(t, "code-123", exchangedCode)
	require.NotNil(t, uc.created)
}

func TestExchangeDoesNotExposeTokenEndpointResponse(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		status int
		body   string
	}{
		{"rejected", http.StatusBadRequest, `{"error_description":"rejected secret-refresh-token"}`},
		{"malformed", http.StatusOK, `{"access_token":"secret-refresh-token",`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(fixture.status)
				_, _ = w.Write([]byte(fixture.body))
			}))
			defer server.Close()
			uc := &fakeChannelUsecase{}
			svc := NewService(uc, WithHTTPClient(server.Client()), WithTokenURL(PlatformClaude, server.URL))
			auth, err := svc.AuthURL(context.Background(), PlatformClaude, AuthURLRequest{})
			require.NoError(t, err)
			_, err = svc.Exchange(context.Background(), PlatformClaude, ExchangeRequest{
				SessionID: auth.SessionID,
				State:     auth.State,
				Code:      "code",
			})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret-refresh-token")
			assert.Nil(t, uc.created)
		})
	}
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
