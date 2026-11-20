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

func TestOAuthDevicePollClaimPreservesSessionAndExpiry(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { rdb.Close() })
	first := NewOAuthSessionRepo(&Repository{redis: rdb})
	second := NewOAuthSessionRepo(&Repository{redis: rdb})
	now := time.Now()
	session := &channeloauth.Session{ID: "device", CreatedAt: now, Device: &channeloauth.DeviceSession{ExpiresAt: now.Add(time.Minute)}}
	require.NoError(t, first.Set(session))
	original, ok := first.Get(session.ID, now)
	require.True(t, ok)
	srv.FastForward(5 * time.Second)
	claimed := *original
	device := *original.Device
	device.PollAt = now.Add(20 * time.Second)
	claimed.Device = &device
	ok, err := first.CompareAndSwap(original, &claimed, now.Add(5*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = second.CompareAndSwap(original, nil, now.Add(5*time.Second))
	require.NoError(t, err)
	require.False(t, ok, "an obsolete snapshot cannot consume a claimed session")
	current, ok := second.Get(session.ID, now.Add(5*time.Second))
	require.True(t, ok)
	require.True(t, current.Device.PollAt.Equal(device.PollAt))
	srv.FastForward(56 * time.Second)
	_, ok = first.Get(session.ID, now.Add(5*time.Second))
	require.False(t, ok, "claiming a poll preserves the Redis TTL")
}
