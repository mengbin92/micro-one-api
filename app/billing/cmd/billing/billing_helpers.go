package main

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	bcfg "micro-one-api/app/billing/internal/conf"
	appdb "micro-one-api/platform/database/partition"
	applogger "micro-one-api/platform/logging"
)

// startPartitionMaintenance runs periodic partition maintenance for the
// billing-service's partitioned tables. It is gated by the partition feature
// flag (default off) and is a no-op when the repository has no *gorm.DB.
func startPartitionMaintenance(ctx context.Context, db *gorm.DB, cfg bcfg.PartitionConfig) func() {
	if !cfg.Enabled || db == nil {
		return nil
	}
	maintenanceCtx, cancel := context.WithCancel(ctx)
	pm := appdb.NewPartitionManager(db)
	interval := parseDurationOrDefault(cfg.Interval, 24*time.Hour)
	tables := cfg.PartitionTables()
	runMaintenance := func() {
		for _, table := range tables {
			if err := pm.PartitionMaintenanceForTable(maintenanceCtx, table); err != nil {
				applogger.Log.Warn("partition maintenance failed",
					zap.String("table", table), zap.Error(err))
			}
		}
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Run once immediately so newly-enabled services don't wait a full
		// interval before their first partition is created.
		runMaintenance()
		for {
			select {
			case <-maintenanceCtx.Done():
				return
			case <-ticker.C:
				runMaintenance()
			}
		}
	}()
	return cancel
}

func parseDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func defaultInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
