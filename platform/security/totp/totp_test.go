package totp

import (
	"encoding/base32"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRFC6238SHA1Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		unix int64
		code string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"}} {
		code, err := Code(secret, tc.unix/30, 8)
		require.NoError(t, err)
		require.Equal(t, tc.code, code)
	}
	code, err := Code(secret, 100, 6)
	require.NoError(t, err)
	step, ok := Match(secret, code, time.Unix(3000, 0), 99)
	require.True(t, ok)
	require.EqualValues(t, 100, step)
	_, ok = Match(secret, code, time.Unix(3000, 0), step)
	require.False(t, ok)
	_, ok = Match(secret, "12345x", time.Unix(3000, 0), 0)
	require.False(t, ok)
}
