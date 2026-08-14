package db

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
)

// PartitionMaintenanceForTable performs routine maintenance using the safe
// default retention for the table. Logs default to six months for backward
// compatibility; billing ledgers have no automatic deletion policy.
func (pm *PartitionManager) PartitionMaintenanceForTable(ctx context.Context, tableName string) error {
	retention := time.Duration(0)
	if tableName == LogTable {
		retention = 6 * 30 * 24 * time.Hour
	}
	return pm.PartitionMaintenanceForTableWithRetention(ctx, tableName, retention)
}

// PartitionMaintenanceForTableWithRetention performs routine partition
// maintenance for one table. A non-positive retention disables partition
// deletion, which is mandatory for billing_ledgers unless a separately
// approved archival/retention policy is introduced.
func (pm *PartitionManager) PartitionMaintenanceForTableWithRetention(ctx context.Context, tableName string, retention time.Duration) error {
	if !pm.Supported {
		return nil
	}
	if err := pm.EnsureFuturePartitions(ctx, tableName, 12); err != nil {
		return fmt.Errorf("failed to ensure future partitions for %s: %w", tableName, err)
	}
	if retention > 0 {
		if err := pm.DropPartitionsOlderThan(ctx, tableName, retention); err != nil {
			return fmt.Errorf("failed to drop old partitions from %s: %w", tableName, err)
		}
	}

	applogger.Log.Info("Partition maintenance completed for table",
		zap.String("table", tableName),
		zap.Duration("retention", retention),
	)
	return nil
}
