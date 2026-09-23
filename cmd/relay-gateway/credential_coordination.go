package main

import (
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"micro-one-api/domain/upstream/credential"
	"micro-one-api/internal/data"
	applogger "micro-one-api/platform/logging"
)

type coordinatedTokenProvider interface {
	SetRefreshCoordinator(credential.RefreshCoordinator) error
}

func configureCredentialCoordination(mode string, client *redis.Client, sweepEnabled bool, providers ...coordinatedTokenProvider) error {
	switch strings.TrimSpace(mode) {
	case "", "single":
		applogger.Log.Warn("OAuth refresh coordination is single-process; keep one OAuth relay replica")
		return nil
	case "redis":
		if !sweepEnabled {
			return fmt.Errorf("RELAY_CREDENTIAL_COORDINATION=redis requires the credential refresh sweep")
		}
		coordinator := data.NewCredentialRefreshCoordinator(client)
		if coordinator == nil {
			return fmt.Errorf("RELAY_CREDENTIAL_COORDINATION=redis requires configured Redis")
		}
		for _, provider := range providers {
			if err := provider.SetRefreshCoordinator(coordinator); err != nil {
				return fmt.Errorf("configure credential coordination: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("RELAY_CREDENTIAL_COORDINATION must be single or redis")
	}
}
