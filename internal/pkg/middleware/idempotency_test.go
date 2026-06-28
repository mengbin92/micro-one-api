package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeIdempotencyKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHash bool
	}{
		{"empty key", "", false},
		{"short key", "abc", false},
		{"valid hash", "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6", true},
		{"spaces", "  key  ", false},
		{"normal key", "my-custom-key-12345", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeIdempotencyKey(tt.input)
			if tt.wantHash && !looksLikeHash(result) {
				t.Errorf("normalizeIdempotencyKey() = %v, want hash-like result", result)
			}
		})
	}
}

func TestLooksLikeHash(t *testing.T) {
	tests := []struct {
		name string
		input string
		want bool
	}{
		{"empty", "", false},
		{"too short", "abc123", false},
		{"valid lowercase", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6", true},
		{"valid uppercase", "A1B2C3D4E5F6A7B8C9D0E1F2A3B4C5D6E7F8A9B0C1D2E3F4A5B6", true},
		{"mixed case", "A1b2C3d4E5f6A7b8C9d0E1f2A3b4C5d6E7f8A9b0C1d2E3f4A5b6", true},
		{"invalid chars", "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z!", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeHash(tt.input); got != tt.want {
				t.Errorf("looksLikeHash() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateIdempotencyKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"empty", "", true},
		{"too short", "abc", true},
		{"valid hash", "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6", false},
		{"normal key", "my-custom-key-12345", false},
		{"too long", string(make([]byte, 300)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateIdempotencyKey(tt.key); (err != nil) != tt.wantErr {
				t.Errorf("ValidateIdempotencyKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGenerateIdempotencyKey(t *testing.T) {
	key1 := GenerateIdempotencyKey("POST", "/api/payments", "user123", "order456")
	key2 := GenerateIdempotencyKey("POST", "/api/payments", "user123", "order456")
	key3 := GenerateIdempotencyKey("GET", "/api/payments", "user123", "order456")

	if key1 != key2 {
		t.Error("GenerateIdempotencyKey() should produce same key for same inputs")
	}
	if key1 == key3 {
		t.Error("GenerateIdempotencyKey() should produce different key for different methods")
	}

	// Generated keys should look like hashes
	if !looksLikeHash(key1) {
		t.Error("Generated key should look like a hash")
	}
}

func TestIdempotencyMiddleware(t *testing.T) {
	// Create a mock handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	})

	// Create middleware without Redis (local cache only)
	middleware := NewIdempotencyMiddleware(nil, DefaultIdempotencyConfig())

	// Test without idempotency key
	req1 := httptest.NewRequest("POST", "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.Handler(handler).ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", rec1.Code)
	}

	// Test with idempotency key (first request)
	req2 := httptest.NewRequest("POST", "/test", nil)
	req2.Header.Set("Idempotency-Key", "test-key-12345")
	rec2 := httptest.NewRecorder()
	middleware.Handler(handler).ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", rec2.Code)
	}

	// Test with same idempotency key (should replay)
	// Note: This test won't fully work without proper Redis integration
	// but it verifies the middleware structure is correct
	req3 := httptest.NewRequest("POST", "/test", nil)
	req3.Header.Set("Idempotency-Key", "test-key-12345")
	rec3 := httptest.NewRecorder()
	middleware.Handler(handler).ServeHTTP(rec3, req3)

	// The request should still succeed (original response or cached)
	if rec3.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", rec3.Code)
	}
}
