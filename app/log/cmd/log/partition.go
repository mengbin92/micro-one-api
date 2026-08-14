package main

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	logconf "micro-one-api/app/log/internal/conf"
	appdb "micro-one-api/platform/database/partition"
	applogger "micro-one-api/platform/logging"
)

// startPartitionMaintenance runs periodic partition maintenance for the
// log-service's `logs` table. It is gated by the partition feature flag
// (default off) and is a no-op when the repository is backed by the
// in-memory store (no *gorm.DB). This is the cron integration listed in
// REVIEW_v4 §六 as a remaining optional optimization item.
func startPartitionMaintenance(ctx context.Context, db *gorm.DB, cfg *logconf.Partition, retentionDays int) func() {
	if cfg == nil || !cfg.Enabled || db == nil {
		return func() {}
	}
	maintenanceCtx, cancel := context.WithCancel(ctx)
	pm := appdb.NewPartitionManager(db)
	interval := parsePartitionDurationOrDefault(cfg.Interval, 24*time.Hour)
	retention := time.Duration(retentionDays) * 24 * time.Hour
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	runMaintenance := func() {
		if err := pm.PartitionMaintenanceForTableWithRetention(maintenanceCtx, appdb.LogTable, retention); err != nil {
			applogger.Log.Warn("partition maintenance failed", zap.String("table", appdb.LogTable), zap.Error(err))
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

// parsePartitionDurationOrDefault parses a duration string, falling back when
// empty/invalid/non-positive. It mirrors the helper in billing-service but
// keeps a local copy so log-service does not depend on billing internals.
func parsePartitionDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
