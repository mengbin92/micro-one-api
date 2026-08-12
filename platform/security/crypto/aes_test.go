package crypto

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt_RoundTrip_AllKeySizes(t *testing.T) {
	plaintext := "sensitive-value-✓-unicode"
	for _, key := range [][]byte{
		[]byte("0123456789abcdef"),                 // 16 bytes AES-128
		[]byte("0123456789abcdef01234567"),         // 24 bytes AES-192
		[]byte("0123456789abcdef0123456789abcdef"), // 32 bytes AES-256
	} {
		encoded, err := Encrypt(plaintext, key)
		require.NoError(t, err)
		decoded, err := Decrypt(encoded, key)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decoded, "round trip must restore the plaintext")
	}
}

func TestEncrypt_NonceRandomization(t *testing.T) {
	key := []byte("0123456789abcdef")
	a, err := Encrypt("same-plaintext", key)
	require.NoError(t, err)
	b, err := Encrypt("same-plaintext", key)
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "each encryption must use a fresh nonce")
}

func TestEncrypt_InvalidKeyLength(t *testing.T) {
	for _, bad := range [][]byte{nil, []byte("short"), []byte("1234567890")} {
		_, err := Encrypt("x", bad)
		require.Error(t, err, "key of length %d must be rejected", len(bad))
		assert.Contains(t, err.Error(), "AES")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	encoded, err := Encrypt("secret", []byte("0123456789abcdef"))
	require.NoError(t, err)

	_, err = Decrypt(encoded, []byte("fedcba9876543210"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt")
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	_, err := Decrypt("!!!not-base64!!!", []byte("0123456789abcdef"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base64")
}

func TestDecrypt_CiphertextTooShort(t *testing.T) {
	// A valid base64 string shorter than the GCM nonce (12 bytes).
	encoded := base64.StdEncoding.EncodeToString([]byte("tiny"))
	_, err := Decrypt(encoded, []byte("0123456789abcdef"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestDecrypt_InvalidKeyLength(t *testing.T) {
	_, err := Decrypt("c2hvcnQ=", []byte("bad"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AES")
}

func TestEncrypt_EmptyPlaintext(t *testing.T) {
	encoded, err := Encrypt("", []byte("0123456789abcdef"))
	require.NoError(t, err)
	decoded, err := Decrypt(encoded, []byte("0123456789abcdef"))
	require.NoError(t, err)
	assert.Equal(t, "", decoded)
}

func TestEncrypt_LargePayload(t *testing.T) {
	key := []byte("0123456789abcdef")
	payload := strings.Repeat("A", 1<<20) // 1 MiB
	encoded, err := Encrypt(payload, key)
	require.NoError(t, err)
	decoded, err := Decrypt(encoded, key)
	require.NoError(t, err)
	assert.Equal(t, payload, decoded)
}
