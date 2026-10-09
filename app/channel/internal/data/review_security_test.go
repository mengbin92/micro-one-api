package data

import (
	"context"
	"encoding/base64"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	channeloauth "micro-one-api/app/channel/internal/biz/oauth"
	"strings"
	"testing"
	"time"
)

func TestCorruptCredentialFailsClosed(t *testing.T) {
	repo := &Repository{encKey: []byte("01234567890123456789012345678901")}
	corrupt := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 40)))
	_, err := repo.decryptKey(corrupt)
	require.Error(t, err)
	_, err = (&Repository{}).decryptKey(corrupt)
	require.Error(t, err)
	_, err = repo.decryptKey("sk-legacy-secret")
	require.Error(t, err)
	plain, err := (&Repository{}).decryptKey("sk-legacy-secret")
	require.NoError(t, err)
	require.Equal(t, "sk-legacy-secret", plain)
}

func TestMalformedCiphertextDoesNotBecomePlaintext(t *testing.T) {
	repo := &Repository{encKey: []byte("01234567890123456789012345678901")}
	ciphertext, err := repo.encryptKey("provider-secret")
	require.NoError(t, err)
	plain, err := repo.decryptKey("!" + ciphertext[1:])
	require.Error(t, err)
	require.Empty(t, plain)
}
func TestOAuthSessionSharedAndConsumedOnce(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { rdb.Close() })
	first, second := NewOAuthSessionRepo(&Repository{redis: rdb}), NewOAuthSessionRepo(&Repository{redis: rdb})
	session := &channeloauth.Session{ID: "one", State: "state", CodeVerifier: "secret", CreatedAt: time.Now()}
	require.NoError(t, first.Set(session))
	got, ok := second.Get("one", time.Now())
	require.True(t, ok)
	require.Equal(t, "secret", got.CodeVerifier)
	_, ok = second.Pop("one", time.Now())
	require.True(t, ok)
	_, ok = first.Pop("one", time.Now())
	require.False(t, ok)
	require.NoError(t, rdb.Ping(context.Background()).Err())
}
