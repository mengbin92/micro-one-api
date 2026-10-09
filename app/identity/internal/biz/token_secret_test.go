package biz

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHashTokenKeyRequiresConfiguredSecret(t *testing.T) {
	t.Setenv("TOKEN_HASH_KEY", "")
	t.Setenv("JWT_SECRET_KEY", "")
	require.Panics(t, func() { HashTokenKey("private") })
}
