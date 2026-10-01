package auth

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"micro-one-api/domain/authorization"
)

// UserSessionClaims authenticates identity only. Role is a legacy display claim;
// IAM authority always comes from the primary database.
type UserSessionClaims struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Role      int32  `json:"role"`
	TokenType string `json:"token_type"`
	PwdEpoch  int64  `json:"pwd_epoch,omitempty"`
	jwt.RegisteredClaims
}

// VerifyUserSession returns only signed, time-validated authentication facts.
// Never synthesize a JTI from the user ID or accept a service/API token here.
func VerifyUserSession(raw string, secret []byte, issuer string, now time.Time) (authorization.Actor, error) {
	invalid := errors.New("invalid user session")
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "Bearer "))
	if raw == "" || len(secret) == 0 || issuer == "" || now.IsZero() {
		return authorization.Actor{}, invalid
	}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(issuer), jwt.WithAudience("micro-one-api-web"), jwt.WithExpirationRequired(), jwt.WithTimeFunc(func() time.Time { return now }))
	token, err := parser.ParseWithClaims(raw, &UserSessionClaims{}, func(*jwt.Token) (any, error) { return secret, nil })
	if err != nil {
		return authorization.Actor{}, invalid
	}
	c, ok := token.Claims.(*UserSessionClaims)
	if !ok || !token.Valid || c.TokenType != "user_session" || c.UserID <= 0 || c.PwdEpoch < 0 || strings.TrimSpace(c.ID) == "" || len(c.ID) > 128 || c.Subject != strconv.FormatInt(c.UserID, 10) || c.ExpiresAt == nil {
		return authorization.Actor{}, invalid
	}
	return authorization.Actor{UserID: c.UserID, SessionID: c.ID, PasswordEpoch: c.PwdEpoch, ExpiresAt: c.ExpiresAt.Time.UTC()}, nil
}
