package main

import (
	"strings"
	"testing"

	identityconf "micro-one-api/app/identity/internal/conf"
)

func TestRequireIdentitySecrets(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "")
	if err := requireIdentitySecrets(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET_KEY") {
		t.Fatalf("expected missing JWT secret error, got %v", err)
	}

	t.Setenv("JWT_SECRET_KEY", "test-secret")
	if err := requireIdentitySecrets(); err != nil {
		t.Fatalf("requireIdentitySecrets() error = %v", err)
	}
}

func TestNewRepoValidatesSecretsBeforeOpeningDatabase(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "")
	t.Setenv("IDENTITY_MEMORY_MODE", "")
	cfg := &Config{Bootstrap: &identityconf.Bootstrap{Data: &identityconf.Data{
		Database: &identityconf.Database{Driver: "unsupported", Source: "invalid-dsn"},
	}}}

	_, err := newRepo(cfg)
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET_KEY") {
		t.Fatalf("newRepo() error = %v, want missing JWT secret before database access", err)
	}
}

func TestNewRepoAllowsMissingSecretsOnlyForExplicitMemoryMode(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "")
	t.Setenv("IDENTITY_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")
	t.Setenv("IDENTITY_MEMORY_MODE", "true")
	cfg := &Config{Bootstrap: &identityconf.Bootstrap{Data: &identityconf.Data{
		Database: &identityconf.Database{},
	}}}

	repo, err := newRepo(cfg)
	if err != nil {
		t.Fatalf("newRepo() error = %v", err)
	}
	if repo == nil {
		t.Fatal("newRepo() returned nil memory repository")
	}
}

func TestExplicitMemoryModeDoesNotBypassSecretsForPersistentRepository(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "")
	t.Setenv("IDENTITY_MEMORY_MODE", "true")
	cfg := &Config{Bootstrap: &identityconf.Bootstrap{Data: &identityconf.Data{
		Database: &identityconf.Database{Driver: "sqlite3", Source: "file::memory:?cache=shared"},
	}}}

	_, err := newRepo(cfg)
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET_KEY") {
		t.Fatalf("newRepo() error = %v, want persistent repository secret validation", err)
	}
}
