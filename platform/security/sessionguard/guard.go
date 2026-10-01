// Package sessionguard authenticates end-user sessions on data-owner HTTP
// entry points. It verifies the shared user-session JWT locally; authorization
// decisions still come from the owner's resource authorization path.
package sessionguard

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"os"
	"strings"
	"time"

	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	sessionauth "micro-one-api/platform/security/auth"
)

type actorKey struct{}

// Guard verifies user-session JWTs with the deployment's shared secret. A
// missing secret keeps every guarded route closed.
type Guard struct {
	secret []byte
	issuer string
}

// FromEnvironment reads JWT_SECRET_KEY and JWT_ISSUER (default issuer
// "micro-one-api"), matching identity's signing configuration.
func FromEnvironment() *Guard {
	issuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if issuer == "" {
		issuer = "micro-one-api"
	}
	return &Guard{secret: []byte(os.Getenv("JWT_SECRET_KEY")), issuer: issuer}
}

// Wrap requires a verified user session and stores its actor in the request
// context. It answers 401 before the handler runs.
func (g *Guard) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := g.Verify(r.Header.Get("Authorization"))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = jsonx.NewEncoder(w).Encode(map[string]string{"error": "user session required"})
			return
		}
		next(w, r.WithContext(WithActor(r.Context(), actor)))
	}
}

// Verify parses and validates the bearer credential.
func (g *Guard) Verify(header string) (authorization.Actor, error) {
	return sessionauth.VerifyUserSession(strings.TrimSpace(header), g.secret, g.issuer, time.Now())
}

// WithActor stores a verified actor. Only Wrap and tests may set it.
func WithActor(ctx context.Context, a authorization.Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the verified actor, if Wrap authenticated this request.
func ActorFrom(ctx context.Context) (authorization.Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(authorization.Actor)
	return a, ok
}

// CredentialFrom returns the raw bearer credential so the owner can forward
// it for a resource authorization decision.
func CredentialFrom(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer "))
}

// LegacyAdmin verifies the JWT before reading its compatibility role. This
// value is used only after identity explicitly reports legacy mode.
func (g *Guard) LegacyAdmin(raw string) bool {
	if _, err := g.Verify(raw); err != nil {
		return false
	}
	claims := &sessionauth.UserSessionClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")), claims)
	return err == nil && claims.Role >= 10
}
