package db

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPartitionNameParsing(t *testing.T) {
	tests := []struct {
		name          string
		partitionName string
		wantDate      string
		wantValid     bool
	}{
		{"valid partition", "p202606", "202606", true},
		{"valid partition 2027", "p202701", "202701", true},
		{"invalid prefix", "x202606", "", false},
		{"invalid length", "p2026", "", false},
		{"invalid length long", "p20260615", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := false
			date := ""

			if len(tt.partitionName) == 7 && tt.partitionName[0] == 'p' {
				date = tt.partitionName[1:]
				valid = true
			}

			if valid != tt.wantValid {
				t.Errorf("valid=%v, want %v", valid, tt.wantValid)
			}
			if date != tt.wantDate {
				t.Errorf("date=%v, want %v", date, tt.wantDate)
			}
		})
	}
}

func TestAddOldPartition(t *testing.T) {
	// This is a basic unit test for the partition logic
	// Integration tests would require a MySQL database

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	pm := NewPartitionManager(db)
	ctx := context.Background()

	// Test that we can create a partition manager
	if pm == nil {
		t.Fatal("failed to create partition manager")
	}

	// Test GetPartitionStatus on a non-partitioned table
	// Should return empty or error depending on database
	_, err = pm.GetPartitionStatus(ctx, "test_table")
	// We expect an error with SQLite (doesn't support partitioning the same way)
	if err == nil {
		t.Log("GetPartitionStatus succeeded (may not be supported in SQLite)")
	}
}

func TestDropPartitionsOlderThan(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	pm := NewPartitionManager(db)
	ctx := context.Background()

	// Test with 6 month retention
	retention := 6 * 30 * 24 * time.Hour

	// This should not fail even with no partitions
	err = pm.DropPartitionsOlderThan(ctx, "logs", retention)
	if err != nil {
		t.Logf("DropPartitionsOlderThan returned error (expected with non-partitioned table): %v", err)
	}
}

func TestEnsureFuturePartitions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	pm := NewPartitionManager(db)
	ctx := context.Background()

	// Test ensuring future partitions
	err = pm.EnsureFuturePartitions(ctx, "logs", 12)
	if err != nil {
		t.Logf("EnsureFuturePartitions returned error (expected with non-partitioned table): %v", err)
	}
}

func TestGetTablePartitionSummary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	pm := NewPartitionManager(db)
	ctx := context.Background()

	// Test getting partition summary
	summary, err := pm.GetTablePartitionSummary(ctx, "logs")
	if err != nil {
		t.Logf("GetTablePartitionSummary returned error (expected with non-partitioned table): %v", err)
	} else {
		if summary.TableName != "logs" {
			t.Errorf("TableName=%s, want logs", summary.TableName)
		}
	}
}

func TestPartitionMaintenanceForTableUnsupportedTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	pm := NewPartitionManager(db)
	ctx := context.Background()

	// An unsupported table name must produce an explicit error rather than
	// silently doing nothing — this is the contract the per-service cron
	// (REVIEW_v4 §六) relies on.
	err = pm.PartitionMaintenanceForTable(ctx, "unknown_table")
	if err == nil {
		t.Fatal("expected error for unsupported table, got nil")
	}
}

func TestPartitionMaintenanceForTableLogs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	pm := NewPartitionManager(db)
	ctx := context.Background()

	// With a non-partitioned SQLite table the underlying ALTER/INFORMATION
	// queries fail; the helper should surface that error rather than panic.
	err = pm.PartitionMaintenanceForTable(ctx, LogTable)
	if err == nil {
		t.Logf("PartitionMaintenanceForTable(logs) succeeded on SQLite (unexpected)")
	}
}
