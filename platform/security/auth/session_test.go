package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestVerifyUserSessionPreservesSignedIdentity(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	claims := UserSessionClaims{UserID: 42, Role: 100, TokenType: "user_session", PwdEpoch: 123, RegisteredClaims: jwt.RegisteredClaims{ID: "real-jti", Subject: "42", Issuer: "identity", Audience: []string{"micro-one-api-web"}, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}
	secret := []byte("isolated-session-signing-secret")
	sign := func(c UserSessionClaims, method jwt.SigningMethod) string {
		raw, err := jwt.NewWithClaims(method, c).SignedString(secret)
		require.NoError(t, err)
		return raw
	}
	actor, err := VerifyUserSession("Bearer "+sign(claims, jwt.SigningMethodHS256), secret, "identity", now)
	require.NoError(t, err)
	require.EqualValues(t, 42, actor.UserID)
	require.Equal(t, "real-jti", actor.SessionID)
	require.EqualValues(t, 123, actor.PasswordEpoch)
	require.Equal(t, claims.ExpiresAt.Time.UTC(), actor.ExpiresAt)
	for _, tc := range []struct {
		name   string
		mutate func(*UserSessionClaims)
	}{
		{"missing jti", func(c *UserSessionClaims) { c.ID = "" }},
		{"blank jti", func(c *UserSessionClaims) { c.ID = "  " }},
		{"missing expiration", func(c *UserSessionClaims) { c.ExpiresAt = nil }},
		{"expired", func(c *UserSessionClaims) { c.ExpiresAt = jwt.NewNumericDate(now) }},
		{"future not before", func(c *UserSessionClaims) { c.NotBefore = jwt.NewNumericDate(now.Add(time.Minute)) }},
		{"wrong subject", func(c *UserSessionClaims) { c.Subject = "43" }},
		{"wrong audience", func(c *UserSessionClaims) { c.Audience = []string{"micro-one-api"} }},
		{"wrong issuer", func(c *UserSessionClaims) { c.Issuer = "attacker" }},
		{"service token", func(c *UserSessionClaims) { c.TokenType = "service" }},
		{"invalid user", func(c *UserSessionClaims) { c.UserID = 0 }},
		{"negative epoch", func(c *UserSessionClaims) { c.PwdEpoch = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := claims
			tc.mutate(&c)
			_, err := VerifyUserSession(sign(c, jwt.SigningMethodHS256), secret, "identity", now)
			require.Error(t, err)
		})
	}
	_, err = VerifyUserSession(sign(claims, jwt.SigningMethodHS512), secret, "identity", now)
	require.Error(t, err)
	_, err = VerifyUserSession(sign(claims, jwt.SigningMethodHS256), []byte("other-secret"), "identity", now)
	require.Error(t, err)
}
