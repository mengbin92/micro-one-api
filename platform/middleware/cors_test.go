package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultCORSConfigFailsClosedWhenOriginsAreUnset(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	config := DefaultCORSConfig()
	if len(config.AllowedOrigins) != 0 {
		t.Fatalf("AllowedOrigins = %v, want empty fail-closed default", config.AllowedOrigins)
	}

	handler := CORS(config)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func TestDefaultCORSConfigParsesConfiguredOrigins(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://app.example , https://admin.example ")
	config := DefaultCORSConfig()
	if len(config.AllowedOrigins) != 2 || config.AllowedOrigins[0] != "https://app.example" || config.AllowedOrigins[1] != "https://admin.example" {
		t.Fatalf("AllowedOrigins = %v", config.AllowedOrigins)
	}
}

func TestRelayCORSConfigIsCredentialFree(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://console.example.com")
	config := RelayCORSConfig()

	if config.AllowCredentials {
		t.Fatal("Relay CORS must not allow browser credentials")
	}
	if len(config.AllowedMethods) != 3 || config.AllowedMethods[0] != http.MethodGet || config.AllowedMethods[1] != http.MethodPost || config.AllowedMethods[2] != http.MethodOptions {
		t.Fatalf("Relay allowed methods = %v", config.AllowedMethods)
	}
	if !contains(config.ExposedHeaders, "X-RateLimit-Remaining") {
		t.Fatalf("Relay exposed headers = %v", config.ExposedHeaders)
	}
}

func TestRelayCORSPreflightAllowsAuthorization(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://console.example.com")
	handler := CORS(RelayCORSConfig())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight must not enter the business handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://console.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,x-request-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("allow headers = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow credentials = %q, want empty", got)
	}
}

func TestRelayCORSRejectsUnconfiguredOrigin(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://console.example.com")
	called := false
	handler := CORS(RelayCORSConfig())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("rejected preflight entered the business handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow origin = %q, want empty", got)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
