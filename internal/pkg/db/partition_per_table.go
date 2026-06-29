package db

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// PartitionMaintenanceForTable performs routine partition maintenance for a
// single table only. It mirrors PartitionMaintenance but scopes the work to
// one table so each service only touches the table(s) it owns:
//
//   - log-service    → LogTable
//   - billing-service → BillingLedgersTable
//
// This avoids cross-service assumptions about shared DSNs and lets each
// service run its own cron with per-table config. See REVIEW_v4 §六 for
// context.
//
// On non-MySQL backends (e.g. SQLite used by the Lite deployment) this
// returns nil immediately so the per-service cron can keep ticking
// without surfacing "no such table: information_schema" errors.
func (pm *PartitionManager) PartitionMaintenanceForTable(ctx context.Context, tableName string) error {
	if !pm.Supported {
		return nil
	}
	if err := pm.EnsureFuturePartitions(ctx, tableName, 12); err != nil {
		return fmt.Errorf("failed to ensure future partitions for %s: %w", tableName, err)
	}

	retention := 6 * 30 * 24 * time.Hour // approximately 6 months
	if err := pm.DropPartitionsOlderThan(ctx, tableName, retention); err != nil {
		return fmt.Errorf("failed to drop old partitions from %s: %w", tableName, err)
	}

	applogger.Log.Info("Partition maintenance completed for table",
		zap.String("table", tableName),
	)
	return nil
}
