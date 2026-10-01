package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// BootstrapResult describes what EnsureRootAdmin did. Generated is true when a
// random password was generated; callers should treat PlainPassword as
// one-time-display material in that case.
type BootstrapResult struct {
	Created       bool
	Username      string
	Email         string
	PlainPassword string
	Generated     bool
}

// EnsureRootAdmin creates the initial admin user if the users table is empty.
//
// Configuration is read from environment variables:
//   - INITIAL_ADMIN_USERNAME (default "admin")
//   - INITIAL_ADMIN_EMAIL    (default "admin@example.com")
//   - INITIAL_ADMIN_PASSWORD (optional; if unset a random 16-char hex password
//     is generated and returned in BootstrapResult.PlainPassword with
//     Generated=true so the caller can print it once)
//
// Returns Created=false when at least one user already exists — the function
// intentionally does NOT recreate or overwrite an existing admin to avoid
// surprising operators who rotated the default credentials.
func (uc *IdentityUsecase) EnsureRootAdmin(ctx context.Context) (*BootstrapResult, error) {
	count, err := uc.repo.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return &BootstrapResult{Created: false}, nil
	}

	username := strings.TrimSpace(os.Getenv("INITIAL_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	email := strings.TrimSpace(os.Getenv("INITIAL_ADMIN_EMAIL"))
	if email == "" {
		email = "admin@example.com"
	}

	password := os.Getenv("INITIAL_ADMIN_PASSWORD")
	generated := false
	if password == "" {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate password: %w", err)
		}
		password = hex.EncodeToString(buf)
		generated = true
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &User{
		Username:     username,
		DisplayName:  "Administrator",
		Email:        email,
		Group:        "default",
		Status:       UserStatusEnabled,
		Role:         RoleRootUser,
		PasswordHash: string(hash),
		Balance:      uc.defaultQuota,
	}
	if err := uc.bindLegacyGroup(ctx, user); err != nil {
		return nil, err
	}
	if uc.iam != nil {
		_, created, err := uc.createIAMAccount(ctx, *user, true, true)
		if err != nil {
			return nil, err
		}
		if !created {
			return &BootstrapResult{}, nil
		}
	} else if err := uc.repo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("create admin user: %w", err)
	}

	return &BootstrapResult{
		Created:       true,
		Username:      username,
		Email:         email,
		PlainPassword: password,
		Generated:     generated,
	}, nil
}
