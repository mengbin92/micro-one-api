package auth

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetRevocationList clears the process-global revocation blocklist so tests
// never leak state into each other (the list is a package-level global).
func resetRevocationList(t *testing.T) {
	t.Helper()
	revocationMutex.Lock()
	defer revocationMutex.Unlock()
	revocationList = make(map[string]*revokedEntry)
	revocationCleaned = time.Time{}
	t.Cleanup(func() {
		revocationMutex.Lock()
		defer revocationMutex.Unlock()
		revocationList = make(map[string]*revokedEntry)
		revocationCleaned = time.Time{}
	})
}

func newTestManager(t *testing.T) *JWTManager {
	t.Helper()
	t.Setenv("JWT_SECRET_KEY", "test-secret-key-that-is-long-enough")
	t.Setenv("JWT_ISSUER", "test-issuer")
	t.Setenv("JWT_TOKEN_DURATION", "1h")
	jm, err := NewJWTManager()
	require.NoError(t, err)
	return jm
}

func TestNewJWTManager_RequiresSecret(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "")
	_, err := NewJWTManager()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET_KEY")
}

func TestNewJWTManager_Defaults(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "x")
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_TOKEN_DURATION", "")
	jm, err := NewJWTManager()
	require.NoError(t, err)
	assert.Equal(t, "micro-one-api", jm.issuer)
	assert.Equal(t, 24*time.Hour, jm.tokenDuration)
}

func TestGenerateAndValidate_RoundTrip(t *testing.T) {
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", []string{"service"})
	require.NoError(t, err)

	claims, err := jm.ValidateServiceToken(token)
	require.NoError(t, err)
	assert.Equal(t, "relay", claims.ServiceName)
	assert.Equal(t, "api", claims.ServiceType)
	assert.Equal(t, []string{"service"}, claims.Roles)
	assert.Equal(t, "test-issuer", claims.Issuer)
	assert.NotEmpty(t, claims.ID, "every token must carry a JTI")
	assert.True(t, claims.ExpiresAt.Time.After(time.Now()), "exp must be in the future")
}

func TestValidate_BearerPrefix(t *testing.T) {
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)

	_, err = jm.ValidateServiceToken("Bearer " + token)
	require.NoError(t, err, "Bearer prefix must be stripped")
}

func TestValidate_WrongSecret_Rejected(t *testing.T) {
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)

	jm2, err := NewJWTManager() // same env: same secret
	require.NoError(t, err)
	_ = jm2
	other := &JWTManager{secretKey: []byte("a-different-secret-0123456789"), issuer: "test-issuer"}
	_, err = other.ValidateServiceToken(token)
	require.Error(t, err)
}

func TestValidate_ExpiredToken_Rejected(t *testing.T) {
	jm := newTestManager(t)
	// Craft an already-expired token directly so we don't depend on time travel.
	now := time.Now()
	claims := JWTClaims{
		ServiceName: "relay",
		ID:          "jti-expired",
		Issuer:      "test-issuer",
		Audience:    []string{"micro-one-api"},
		ExpiresAt:   jwt.NewNumericDate(now.Add(-time.Hour)),
		IssuedAt:    jwt.NewNumericDate(now.Add(-2 * time.Hour)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jm.secretKey)
	require.NoError(t, err)

	_, err = jm.ValidateServiceToken(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestValidate_MissingExp_Rejected_NoPanic(t *testing.T) {
	// platform-M4 regression: a token WITHOUT an exp claim has a nil
	// ExpiresAt; the old code dereferenced .Time and panicked on the request
	// path. It must be rejected cleanly instead.
	jm := newTestManager(t)
	claims := JWTClaims{
		ServiceName: "relay",
		ID:          "jti-noexp",
		Issuer:      "test-issuer",
		// Audience present so the check reaches the exp guard; ExpiresAt
		// intentionally nil.
		Audience: []string{"micro-one-api"},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jm.secretKey)
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		_, err = jm.ValidateServiceToken(token)
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing expiration")
}

func TestValidate_WrongIssuer_Rejected(t *testing.T) {
	jm := newTestManager(t)
	claims := JWTClaims{
		ServiceName: "relay",
		ID:          "jti-issuer",
		Issuer:      "someone-else",
		Audience:    []string{"micro-one-api"},
		ExpiresAt:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jm.secretKey)
	require.NoError(t, err)

	_, err = jm.ValidateServiceToken(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid issuer")
}

func TestValidate_TamperedToken_Rejected(t *testing.T) {
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)

	// Flip a character in the signature portion. The replacement must be
	// guaranteed different from the original character (base64url alphabet
	// includes 'A', so blindly prefixing "A" was flaky).
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	sig := []byte(parts[2])
	flip := byte('A')
	if sig[0] == flip {
		flip = 'B'
	}
	sig[0] = flip
	tampered := parts[0] + "." + parts[1] + "." + string(sig)

	_, err = jm.ValidateServiceToken(tampered)
	require.Error(t, err)
}

func TestValidate_NonHMACAlgorithm_Rejected(t *testing.T) {
	jm := newTestManager(t)
	claims := JWTClaims{
		ServiceName: "relay",
		ID:          "jti-nonalg",
		Issuer:      "test-issuer",
		Audience:    []string{"micro-one-api"},
		ExpiresAt:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	// Sign with the none algorithm (unsigned). The validator must reject any
	// non-HMAC signing method.
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = jm.ValidateServiceToken(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected signing method")
}

func TestRevokeAndValidate_RevokedTokenRejected(t *testing.T) {
	resetRevocationList(t)
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)

	require.NoError(t, jm.RevokeToken(token))
	_, err = jm.ValidateServiceToken(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoked")
}

func TestRevokeToken_MissingJTI_Error(t *testing.T) {
	resetRevocationList(t)
	jm := newTestManager(t)
	claims := JWTClaims{
		ServiceName: "relay",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Audience:  []string{"micro-one-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			// ID intentionally empty.
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jm.secretKey)
	require.NoError(t, err)

	err = jm.RevokeToken(token)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no JTI")
}

func TestRevokeAndValidate_OtherTokenStillValid(t *testing.T) {
	resetRevocationList(t)
	jm := newTestManager(t)
	t1, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)
	t2, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)

	require.NoError(t, jm.RevokeToken(t1))
	_, err = jm.ValidateServiceToken(t2)
	require.NoError(t, err, "revoking one token must not invalidate others")
}

func TestRefreshToken_ValidToken_IssuesNew(t *testing.T) {
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", []string{"service"})
	require.NoError(t, err)

	refreshed, err := jm.RefreshToken(token)
	require.NoError(t, err)
	assert.NotEqual(t, token, refreshed, "refreshed token must be a new token")

	claims, err := jm.ValidateServiceToken(refreshed)
	require.NoError(t, err)
	assert.Equal(t, "relay", claims.ServiceName)
	assert.NotEqual(t, "", claims.ID)
}

func TestRefreshToken_InvalidToken_Error(t *testing.T) {
	jm := newTestManager(t)
	_, err := jm.RefreshToken("garbage.token.value")
	require.Error(t, err)
}

func TestExtractTokenFromHeader(t *testing.T) {
	assert.Equal(t, "abc", ExtractTokenFromHeader("Bearer abc"))
	assert.Equal(t, "abc", ExtractTokenFromHeader("abc"))
	assert.Equal(t, "", ExtractTokenFromHeader(""))
}

func TestLoadServiceAuthConfig_Defaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	t.Setenv("SERVICE_TYPE", "")
	t.Setenv("SERVICE_ROLES", "")
	t.Setenv("SERVICE_TOKEN", "tok")
	cfg, err := LoadServiceAuthConfig()
	require.NoError(t, err)
	assert.Equal(t, "unknown-service", cfg.ServiceName)
	assert.Equal(t, "api", cfg.ServiceType)
	// strings.Split("", ",") yields [""] — documented quirk of the loader;
	// the caller's HasRole("") simply never matches.
	assert.Equal(t, []string{""}, cfg.Roles)
}

func TestLoadServiceAuthConfig_ParsesRoles(t *testing.T) {
	t.Setenv("SERVICE_NAME", "relay")
	t.Setenv("SERVICE_TYPE", "api")
	t.Setenv("SERVICE_ROLES", "service, admin, billing ")
	t.Setenv("SERVICE_TOKEN", "tok")
	cfg, err := LoadServiceAuthConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"service", "admin", "billing"}, cfg.Roles)
}

func TestLoadServiceAuthConfig_MissingToken_Error(t *testing.T) {
	t.Setenv("SERVICE_TOKEN", "")
	_, err := LoadServiceAuthConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVICE_TOKEN")
}

func TestJWTClaims_RBAC(t *testing.T) {
	admin := &JWTClaims{Roles: []string{"admin"}}
	api := &JWTClaims{Roles: []string{"api"}}
	svc := &JWTClaims{Roles: []string{"service"}}
	none := &JWTClaims{Roles: nil}

	assert.True(t, admin.HasRole("admin"))
	assert.False(t, api.HasRole("admin"))
	assert.True(t, api.HasAnyRole("service", "api"))
	assert.False(t, svc.HasAnyRole("api"))
	assert.True(t, svc.HasAllRoles("service"))
	assert.False(t, svc.HasAllRoles("service", "api"))
	assert.True(t, admin.IsAdmin())
	assert.False(t, api.IsAdmin())

	// CanAccess matrix.
	assert.True(t, admin.CanAccess("admin", "read"))
	assert.False(t, api.CanAccess("admin", "read"))
	assert.True(t, api.CanAccess("api", "read"))
	assert.True(t, svc.CanAccess("service", "read"))
	assert.True(t, svc.CanAccess("unknown-resource", "read"), "unknown resources fall back to service/admin")
	assert.False(t, none.CanAccess("api", "read"))
}

func TestValidateServiceTokenWithRoles(t *testing.T) {
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", []string{"service", "admin"})
	require.NoError(t, err)

	claims, err := jm.ValidateServiceTokenWithRoles(token, []string{"service"})
	require.NoError(t, err)
	assert.Equal(t, "relay", claims.ServiceName)

	_, err = jm.ValidateServiceTokenWithRoles(token, []string{"service", "billing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required role")
}

func TestRevocation_ConcurrentAccess_NoRace(t *testing.T) {
	resetRevocationList(t)
	jm := newTestManager(t)
	token, err := jm.GenerateServiceToken("relay", "api", nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _ = jm.ValidateServiceToken(token)
		})
	}
	wg.Wait()
	// No race detector failure is the assertion; also confirm validity.
	_, err = jm.ValidateServiceToken(token)
	require.NoError(t, err)
}
