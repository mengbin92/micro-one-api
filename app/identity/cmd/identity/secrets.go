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
