package service

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("TOKEN_HASH_KEY") == "" && os.Getenv("JWT_SECRET_KEY") == "" {
		_ = os.Setenv("TOKEN_HASH_KEY", "identity-test-only-token-hash-key")
	}
	os.Exit(m.Run())
}
