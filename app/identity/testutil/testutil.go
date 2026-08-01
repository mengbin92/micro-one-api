// Package testutil re-exports the identity business types, interfaces,
// constructors and sentinel errors needed by cross-service integration
// tests. These symbols live under app/identity/internal and are
// normally invisible outside that subtree.
package testutil

import (
	identitybiz "micro-one-api/app/identity/internal/biz"
	identityservice "micro-one-api/app/identity/internal/service"
)

// Type aliases for entities.
type (
	User            = identitybiz.User
	Token           = identitybiz.Token
	OAuthIdentity   = identitybiz.OAuthIdentity
	IdentityRepo    = identitybiz.IdentityRepo
	IdentityUsecase = identitybiz.IdentityUsecase
)

// Sentinel errors.
var (
	ErrUserNotFound      = identitybiz.ErrUserNotFound
	ErrTokenNotFound     = identitybiz.ErrTokenNotFound
	ErrOAuthUserNotFound = identitybiz.ErrOAuthUserNotFound
	ErrOAuthAlreadyBound = identitybiz.ErrOAuthAlreadyBound
)

// Constants.
const (
	UserStatusEnabled    = identitybiz.UserStatusEnabled
	UserStatusDisabled   = identitybiz.UserStatusDisabled
	TokenStatusEnabled   = identitybiz.TokenStatusEnabled
	TokenStatusDisabled  = identitybiz.TokenStatusDisabled
	TokenStatusExhausted = identitybiz.TokenStatusExhausted
)

// NewIdentityUsecase re-exports the constructor.
func NewIdentityUsecase(repo IdentityRepo) *IdentityUsecase {
	return identitybiz.NewIdentityUsecase(repo)
}

// NewIdentityService re-exports the service constructor.
func NewIdentityService(uc *IdentityUsecase) *identityservice.IdentityService {
	return identityservice.NewIdentityService(uc)
}
