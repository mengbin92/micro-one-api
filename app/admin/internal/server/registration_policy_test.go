package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func TestAdminHTTPProxiesPublicMailFlowToIdentity(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"success":false,"message":"rate limited"}`))
	}))
	defer upstream.Close()
	srv := NewHTTPServer(":0", nil, nil, upstream.URL)
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/verification?email=alice%2Btest%40example.com", ""},
		{http.MethodGet, "/api/reset_password?email=alice%40example.com", ""},
		{http.MethodPost, "/api/user/reset", `{"email":"alice@example.com","token":"secret","password":"new-password"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			require.Equal(t, tc.method, gotMethod)
			require.Equal(t, req.URL.Path, gotPath)
			require.Equal(t, req.URL.RawQuery, gotQuery)
			require.Equal(t, tc.body, gotBody)
			require.Equal(t, http.StatusTooManyRequests, rec.Code)
			require.JSONEq(t, `{"success":false,"message":"rate limited"}`, rec.Body.String())
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestAdminHTTPStatusReadsIdentityPolicyAndExcludesSecrets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/registration/policy", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		_, _ = w.Write([]byte(`{"success":true,"data":{"registration_enabled":true,"email_verification":true,"email_domain_restriction_enabled":true,"email_domain_whitelist":["example.com"],"turnstile_check":true,"turnstile_site_key":"public-site-key","email_delivery_configured":true,"turnstile_secret":"private-turnstile","smtp_password":"private-mail"}}`))
	}))
	defer upstream.Close()
	srv := NewHTTPServer(":0", nil, nil, upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	req.Header.Set("Cookie", "session=browser-cookie")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var got struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &got))
	require.True(t, got.Success)
	for _, key := range []string{"registration_enabled", "email_verification", "email_domain_restriction_enabled", "turnstile_check", "email_delivery_configured", "registration_policy_available"} {
		require.Equal(t, true, got.Data[key], key)
	}
	require.Equal(t, "public-site-key", got.Data["turnstile_site_key"])
	require.Equal(t, []any{"example.com"}, got.Data["email_domain_whitelist"])
	require.NotContains(t, rec.Body.String(), "private-")
	require.NotContains(t, rec.Body.String(), "smtp_password")
	require.NotContains(t, rec.Body.String(), "turnstile_secret")
}

func TestAdminHTTPStatusPolicyFailureDisablesRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unavailable", "upstream down", http.StatusServiceUnavailable},
		{"invalid json", "<html>wrong service</html>", http.StatusOK},
		{"owner failure", `{"success":false,"data":{"registration_enabled":true}}`, http.StatusOK},
		{"missing data", `{"success":true}`, http.StatusOK},
		{"oversized", strings.Repeat(" ", 32*1024+1), http.StatusOK},
		{"redirect", ``, http.StatusFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			srv := NewHTTPServer(":0", nil, nil, upstream.URL)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), `"success":true`)
			require.Contains(t, rec.Body.String(), `"registration_enabled":false`)
			require.Contains(t, rec.Body.String(), `"email_delivery_configured":false`)
			require.Contains(t, rec.Body.String(), `"registration_policy_available":false`)
		})
	}
}

func TestRegistrationPolicyReaderMissingOrCancelled(t *testing.T) {
	policy, ok := newRegistrationPolicyReader("").read(context.Background())
	require.False(t, ok)
	require.False(t, policy.RegistrationEnabled)
	require.Empty(t, policy.EmailDomainWhitelist)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"registration_enabled":true}}`))
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	policy, ok = newRegistrationPolicyReader(upstream.URL).read(ctx)
	require.False(t, ok)
	require.False(t, policy.RegistrationEnabled)
}
