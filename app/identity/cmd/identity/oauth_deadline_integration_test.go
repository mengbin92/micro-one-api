package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	admintest "micro-one-api/app/admin/testutil"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/app/identity/internal/server"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"
	oauth "micro-one-api/platform/security"
)

type deadlineOAuthProvider struct {
	delay        time.Duration
	beforeReturn func()
	returned     chan error
}

func (*deadlineOAuthProvider) Name() string { return "github" }
func (*deadlineOAuthProvider) AuthURL(state string) string {
	return "https://identity.example.test/authorize?state=" + url.QueryEscape(state)
}
func (p *deadlineOAuthProvider) Exchange(ctx context.Context, _ string) (*oauth.UserInfo, error) {
	// A successful identity lookup can finish after an optional lookup used the request budget.
	time.Sleep(p.delay)
	if p.beforeReturn != nil {
		p.beforeReturn()
	}
	p.returned <- ctx.Err()
	return &oauth.UserInfo{Provider: "github", ProviderID: "deadline-subject", Username: "deadline-user"}, nil
}

func TestOAuthBindCallbackDeadline(t *testing.T) {
	for _, tc := range []struct {
		name         string
		delay        time.Duration
		cancelCaller bool
	}{
		{"slow successful provider", 1200 * time.Millisecond, false},
		// A consented exchange retains its bounded write phase after its caller expires.
		{"request context canceled at exchange completion", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redis := miniredis.RunT(t)
			for key, value := range map[string]string{
				"REDIS_ADDR": redis.Addr(), "REDIS_PASSWORD": "", "JWT_SECRET_KEY": "oauth-deadline-jwt-only",
				"TOKEN_HASH_KEY": "oauth-deadline-token-only", "IDENTITY_MFA_ENCRYPTION_KEY": "01234567890123456789012345678901",
				"IDENTITY_SCHEMA": "", "DATABASE_SCHEMA": "", "ADMIN_SCHEMA": "", "SERVICE_TOKEN": "oauth-deadline-shared",
				"ADMIN_TOKEN": "", "INITIAL_ADMIN_USERNAME": "deadline-admin", "INITIAL_ADMIN_PASSWORD": "oauth-deadline-password",
				"IDENTITY_STEP_UP_ENABLED": "false", "IDENTITY_MFA_REQUIRE_ADMIN": "false",
			} {
				t.Setenv(key, value)
			}
			db := dbtest.RoutingContextDB(t, "sqlite")
			var files []struct{ Name, File string }
			require.NoError(t, db.Raw("PRAGMA database_list").Scan(&files).Error)
			var dsn string
			for _, file := range files {
				if file.Name == "main" {
					dsn = file.File + "?_foreign_keys=on&_busy_timeout=5000"
				}
			}
			require.NotEmpty(t, dsn)
			repo, err := data.NewRepositoryFromEnv("sqlite3", dsn, "")
			require.NoError(t, err)
			uc := newIdentityUsecase(repo, nil)
			root, err := uc.EnsureRootAdmin(context.Background())
			require.NoError(t, err)
			require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{
				"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "oauth-deadline-test", "cutover_verified_at": time.Now().UnixMilli(),
			}).Error)
			user, token, err := uc.Login(context.Background(), root.Username, "oauth-deadline-password", "")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := &deadlineOAuthProvider{delay: tc.delay, returned: make(chan error, 1)}
			if tc.cancelCaller {
				provider.beforeReturn = cancel
			}
			registry := oauth.NewProviderRegistry()
			registry.Register(provider)
			ownerHandler := server.NewHTTPServer(":0", uc, registry)
			owner := httptest.NewServer(ownerHandler)
			t.Cleanup(owner.Close)
			front := httptest.NewServer(admintest.NewPublicIdentityHTTP(owner.URL))
			t.Cleanup(front.Close)
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			start, err := http.NewRequest(http.MethodGet, front.URL+"/api/oauth/github/bind", nil)
			require.NoError(t, err)
			start.Header.Set("Authorization", "Bearer "+token)
			started, err := client.Do(start)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, started.StatusCode)
			cookies := started.Cookies()
			_ = started.Body.Close()
			require.NotEmpty(t, cookies)
			callback, err := http.NewRequestWithContext(ctx, http.MethodGet, front.URL+"/v1/oauth/github/callback?code=synthetic&state="+url.QueryEscape(cookies[0].Value), nil)
			require.NoError(t, err)
			callback.AddCookie(cookies[0])
			callback.Header.Set("Accept", "text/html")
			var status int
			var location string
			var failure struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			if tc.cancelCaller {
				recorded := httptest.NewRecorder()
				ownerHandler.ServeHTTP(recorded, callback)
				status, location = recorded.Code, recorded.Header().Get("Location")
				if status != http.StatusFound {
					require.NoError(t, jsonx.Unmarshal(recorded.Body.Bytes(), &failure))
				}
			} else {
				response, err := client.Do(callback)
				require.NoError(t, err)
				defer response.Body.Close()
				status, location = response.StatusCode, response.Header.Get("Location")
				if status != http.StatusFound {
					require.NoError(t, jsonx.NewDecoder(response.Body).Decode(&failure))
				}
			}
			var exchangeContextErr error
			select {
			case exchangeContextErr = <-provider.returned:
			case <-time.After(5 * time.Second):
				t.Fatal("provider exchange was not called")
			}
			var bindings, audits int64
			require.NoError(t, db.Table("user_oauth_identities").Where("user_id = ? AND provider = ?", user.ID, "github").Count(&bindings).Error)
			require.NoError(t, db.Table("iam_audit_events").Where("action = ? AND result = ?", "account.oauth.bind", "success").Count(&audits).Error)
			t.Logf("exchange_ctx_err=%v callback_status=%d callback_message=%q bound_rows=%d successful_bind_audits=%d", exchangeContextErr, status, failure.Message, bindings, audits)
			if tc.cancelCaller {
				require.ErrorIs(t, exchangeContextErr, context.Canceled)
				require.Equal(t, http.StatusFound, status, "consented binding must retain its bounded write phase: %s", failure.Message)
				require.Equal(t, "/profile?oauth_bind=success&provider=github", location)
				require.EqualValues(t, 1, bindings)
				require.EqualValues(t, 1, audits)
				return
			}
			require.NoError(t, exchangeContextErr, "a 1.2s successful lookup must fit the OAuth callback request budget")
			require.Equal(t, http.StatusFound, status, "callback message: %s", failure.Message)
			require.Equal(t, "/profile?oauth_bind=success&provider=github", location)
			require.EqualValues(t, 1, bindings)
			require.EqualValues(t, 1, audits)
		})
	}
}
