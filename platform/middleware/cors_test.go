package middleware

import (
	"net/http"
	"net/http/httptest"
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
