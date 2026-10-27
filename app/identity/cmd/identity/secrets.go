package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

func requireIdentitySecrets() error {
	if strings.TrimSpace(os.Getenv("JWT_SECRET_KEY")) == "" {
		return errors.New("JWT_SECRET_KEY is required for identity-service")
	}
	key := os.Getenv("IDENTITY_MFA_ENCRYPTION_KEY")
	if key != "" && len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return errors.New("IDENTITY_MFA_ENCRYPTION_KEY must be 16, 24, or 32 bytes")
	}
	for _, name := range []string{"IDENTITY_STEP_UP_ENABLED", "IDENTITY_MFA_REQUIRE_ADMIN"} {
		value := os.Getenv(name)
		if value != "" && value != "true" && value != "false" {
			return errors.New(name + " must be true or false")
		}
	}
	if os.Getenv("IDENTITY_MFA_REQUIRE_ADMIN") == "true" && key == "" {
		return errors.New("IDENTITY_MFA_ENCRYPTION_KEY is required by administrator MFA policy")
	}
	return nil
}

func usesExplicitIdentityMemoryRepository(cfg *Config) bool {
	if cfg == nil || cfg.Bootstrap == nil || cfg.Bootstrap.Data == nil || cfg.Bootstrap.Data.Database == nil {
		return false
	}
	if cfg.Bootstrap.Data.Database.Source != "" || os.Getenv("IDENTITY_SQL_DSN") != "" || os.Getenv("SQL_DSN") != "" {
		return false
	}
	allowed, _ := strconv.ParseBool(os.Getenv("IDENTITY_MEMORY_MODE"))
	return allowed
}
