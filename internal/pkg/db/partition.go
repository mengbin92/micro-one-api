// Package db provides database utilities including partition management.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
	"gorm.io/gorm"
)

// PartitionManager handles table partition operations.
//
// MySQL is the only supported backend: native range partitioning,
// REORGANIZE PARTITION and information_schema.PARTITIONS are MySQL-only.
// On any other dialector (e.g. SQLite used by the Lite deployment) the
// manager reports Supported=false and all maintenance calls become
// no-ops so the cron tickers can keep running without error.
type PartitionManager struct {
	db        *gorm.DB
	Supported bool
}

// NewPartitionManager creates a new partition manager. The Supported
// flag is set automatically based on the underlying dialector; callers
// that need to override (e.g. in tests) can construct the struct
// directly.
func NewPartitionManager(db *gorm.DB) *PartitionManager {
	pm := &PartitionManager{db: db, Supported: true}
	if db != nil && db.Dialector != nil {
		name := strings.ToLower(db.Dialector.Name())
		if name != "mysql" {
			pm.Supported = false
		}
	}
	return pm
}

// PartitionInfo contains information about a table partition.
type PartitionInfo struct {
	PartitionName          string `gorm:"column:PARTITION_NAME"`
	PartitionOrdinalPos     int    `gorm:"column:PARTITION_ORDINAL_POSITION"`
	PartitionMethod         string `gorm:"column:PARTITION_METHOD"`
	PartitionExpression     string `gorm:"column:PARTITION_EXPRESSION"`
	PartitionDescription    string `gorm:"column:PARTITION_DESCRIPTION"`
	TableRows              int64  `gorm:"column:TABLE_ROWS"`
	AvgRowLength           int64  `gorm:"column:AVG_ROW_LENGTH"`
	DataLength             int64  `gorm:"column:DATA_LENGTH"`
	IndexLength            int64  `gorm:"column:INDEX_LENGTH"`
}

// GetPartitionStatus retrieves partition information for a table.
func (pm *PartitionManager) GetPartitionStatus(ctx context.Context, tableName string) ([]PartitionInfo, error) {
	var partitions []PartitionInfo

	query := `
		SELECT
			PARTITION_NAME,
			PARTITION_ORDINAL_POSITION,
			PARTITION_METHOD,
			PARTITION_EXPRESSION,
			PARTITION_DESCRIPTION,
			TABLE_ROWS,
			AVG_ROW_LENGTH,
			DATA_LENGTH,
			INDEX_LENGTH
		FROM information_schema.PARTITIONS
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = ?
		ORDER BY PARTITION_ORDINAL_POSITION
	`

	if err := pm.db.WithContext(ctx).Raw(query, tableName).Scan(&partitions).Error; err != nil {
		return nil, fmt.Errorf("failed to get partition status for %s: %w", tableName, err)
	}

	return partitions, nil
}

// AddLogsPartition adds a new monthly partition to the logs table.
func (pm *PartitionManager) AddLogsPartition(ctx context.Context, partitionName string, cutoffDate time.Time) error {
	return pm.addPartition(ctx, "logs", partitionName, cutoffDate)
}

// AddBillingLedgersPartition adds a new monthly partition to the billing_ledgers table.
func (pm *PartitionManager) AddBillingLedgersPartition(ctx context.Context, partitionName string, cutoffDate time.Time) error {
	return pm.addPartition(ctx, "billing_ledgers", partitionName, cutoffDate)
}

// addPartition is a helper that adds a new partition by reorganizing the pmax partition.
func (pm *PartitionManager) addPartition(ctx context.Context, tableName, partitionName string, cutoffDate time.Time) error {
	cutoffStr := cutoffDate.Format("2006-01-02 15:04:05")

	query := fmt.Sprintf(`
		ALTER TABLE %s REORGANIZE PARTITION pmax INTO (
			PARTITION %s VALUES LESS THAN (UNIX_TIMESTAMP('%s')),
			PARTITION pmax VALUES LESS THAN MAXVALUE
		)
	`, tableName, partitionName, cutoffStr)

	if err := pm.db.WithContext(ctx).Exec(query).Error; err != nil {
		return fmt.Errorf("failed to add partition %s to %s: %w", partitionName, tableName, err)
	}

	applogger.Log.Info("Added partition to table",
		zap.String("table", tableName),
		zap.String("partition", partitionName),
		zap.Time("cutoff_date", cutoffDate),
	)

	return nil
}

// DropOldPartition removes an old partition from a table.
func (pm *PartitionManager) DropOldPartition(ctx context.Context, tableName, partitionName string) error {
	query := fmt.Sprintf("ALTER TABLE %s DROP PARTITION %s", tableName, partitionName)

	if err := pm.db.WithContext(ctx).Exec(query).Error; err != nil {
		return fmt.Errorf("failed to drop partition %s from %s: %w", partitionName, tableName, err)
	}

	applogger.Log.Info("Dropped partition from table",
		zap.String("table", tableName),
		zap.String("partition", partitionName),
	)

	return nil
}

// DropPartitionsOlderThan removes all partitions older than the specified retention period.
func (pm *PartitionManager) DropPartitionsOlderThan(ctx context.Context, tableName string, retention time.Duration) error {
	cutoffDate := time.Now().Add(-retention).Format("2006-01")

	partitions, err := pm.GetPartitionStatus(ctx, tableName)
	if err != nil {
		return err
	}

	for _, p := range partitions {
		if p.PartitionName == "pmax" {
			continue
		}

		// Extract date from partition name (format: pYYYYMM)
		if len(p.PartitionName) != 7 || p.PartitionName[0] != 'p' {
			continue
		}

		partDate := p.PartitionName[1:]
		if partDate < cutoffDate {
			if err := pm.DropOldPartition(ctx, tableName, p.PartitionName); err != nil {
				applogger.Log.Warn("Failed to drop old partition",
					zap.String("table", tableName),
					zap.String("partition", p.PartitionName),
					zap.Error(err),
				)
				continue
			}

			applogger.Log.Info("Dropped old partition per retention policy",
				zap.String("table", tableName),
				zap.String("partition", p.PartitionName),
				zap.String("partition_date", partDate),
				zap.Duration("retention", retention),
			)
		}
	}

	return nil
}

// EnsureFuturePartitions ensures partitions exist for the next N months.
func (pm *PartitionManager) EnsureFuturePartitions(ctx context.Context, tableName string, monthsAhead int) error {
	now := time.Now()

	partitions, err := pm.GetPartitionStatus(ctx, tableName)
	if err != nil {
		return err
	}

	// Build a set of existing partition names
	existing := make(map[string]bool)
	for _, p := range partitions {
		existing[p.PartitionName] = true
	}

	// Add partitions for the next N months
	for i := 1; i <= monthsAhead; i++ {
		targetMonth := now.AddDate(0, i, 0)
		partitionName := fmt.Sprintf("p%s", targetMonth.Format("200601"))
		cutoffDate := time.Date(targetMonth.Year(), targetMonth.Month(), 1, 0, 0, 0, 0, time.UTC)

		if !existing[partitionName] {
			var err error
			switch tableName {
			case "logs":
				err = pm.AddLogsPartition(ctx, partitionName, cutoffDate)
			case "billing_ledgers":
				err = pm.AddBillingLedgersPartition(ctx, partitionName, cutoffDate)
			default:
				return fmt.Errorf("unsupported table for partitioning: %s", tableName)
			}

			if err != nil {
				return fmt.Errorf("failed to ensure partition %s for %s: %w", partitionName, tableName, err)
			}
		}
	}

	return nil
}

// LogTable is the partitioned logs table.
const LogTable = "logs"

// BillingLedgersTable is the partitioned billing_ledgers table.
const BillingLedgersTable = "billing_ledgers"

// PartitionMaintenance performs routine partition maintenance tasks.
//
// On non-MySQL backends (e.g. SQLite) this returns nil immediately so the
// background ticker in log-service / billing-service can keep running
// without surfacing "no such table: information_schema" errors.
func (pm *PartitionManager) PartitionMaintenance(ctx context.Context) error {
	if !pm.Supported {
		return nil
	}

	// Ensure 12 months of future partitions exist
	for _, table := range []string{LogTable, BillingLedgersTable} {
		if err := pm.EnsureFuturePartitions(ctx, table, 12); err != nil {
			return fmt.Errorf("failed to ensure future partitions for %s: %w", table, err)
		}
	}

	// Clean up partitions older than 6 months
	retention := 6 * 30 * 24 * time.Hour // approximately 6 months
	for _, table := range []string{LogTable, BillingLedgersTable} {
		if err := pm.DropPartitionsOlderThan(ctx, table, retention); err != nil {
			return fmt.Errorf("failed to drop old partitions from %s: %w", table, err)
		}
	}

	applogger.Log.Info("Partition maintenance completed")
	return nil
}

// TablePartitionInfo provides a summary of partition status for a table.
type TablePartitionInfo struct {
	TableName      string
	PartitionCount int
	TotalRows      int64
	TotalDataSize  int64
	TotalIndexSize int64
	OldestPartition string
	NewestPartition string
}

// GetTablePartitionSummary returns a summary of partition information for a table.
func (pm *PartitionManager) GetTablePartitionSummary(ctx context.Context, tableName string) (*TablePartitionInfo, error) {
	partitions, err := pm.GetPartitionStatus(ctx, tableName)
	if err != nil {
		return nil, err
	}

	info := &TablePartitionInfo{
		TableName: tableName,
	}

	if len(partitions) == 0 {
		return info, nil
	}

	info.PartitionCount = len(partitions)
	info.OldestPartition = partitions[0].PartitionName
	info.NewestPartition = partitions[len(partitions)-2].PartitionName // exclude pmax

	for _, p := range partitions {
		if p.PartitionName == "pmax" {
			continue
		}
		info.TotalRows += p.TableRows
		info.TotalDataSize += p.DataLength
		info.TotalIndexSize += p.IndexLength
	}

	return info, nil
}
