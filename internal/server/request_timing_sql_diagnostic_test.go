package server

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/pkg/jsonx"
)

// This fast feedback loop deliberately has a different name from the full
// acceptance test. Its JSON states the shorter duration; it is not capacity
// admission and cannot weaken TestRequestTimingSQLOverhead.
func TestRequestTimingSQLDiagnosticOverhead(t *testing.T) {
	if os.Getenv("TIMING_SQL_DIAGNOSTIC_BENCH") != "1" {
		t.Skip("short SQL diagnostic is opt-in; not acceptance")
	}
	driver := os.Getenv("TIMING_SQL_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	runRequestTimingOverheadDuration(t, newTimingSQLStack(t, driver), driver, 2*time.Second)
}

// Optional probes are separate from the acceptance gate: the original four
// 30-second pairs and their thresholds are never reduced by an environment flag.
func attachTimingSQLDiagnostics(t *testing.T, db *gorm.DB) {
	t.Helper()
	type statement struct {
		Count   int64   `json:"count"`
		TotalMS float64 `json:"total_ms"`
		MaxMS   float64 `json:"max_ms"`
		Errors  int64   `json:"errors"`
	}
	var mu sync.Mutex
	statements := make(map[string]*statement)
	before := func(tx *gorm.DB) { tx.InstanceSet("timing_diagnostic_start", time.Now()) }
	after := func(operation string) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			value, ok := tx.InstanceGet("timing_diagnostic_start")
			if !ok {
				return
			}
			elapsed := float64(time.Since(value.(time.Time))) / 1e6
			mu.Lock()
			defer mu.Unlock()
			key := operation + ":" + tx.Statement.Table
			s := statements[key]
			if s == nil {
				s = &statement{}
				statements[key] = s
			}
			s.Count++
			s.TotalMS += elapsed
			s.MaxMS = max(s.MaxMS, elapsed)
			if tx.Error != nil {
				s.Errors++
			}
		}
	}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("timing_diagnostic_before", before))
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("timing_diagnostic_after", after("create")))
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("timing_diagnostic_before", before))
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("timing_diagnostic_after", after("query")))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("timing_diagnostic_before", before))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("timing_diagnostic_after", after("update")))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	baseline := sqlDB.Stats()
	t.Cleanup(func() {
		stats := sqlDB.Stats()
		mu.Lock()
		defer mu.Unlock()
		encoded, err := jsonx.MarshalIndent(map[string]any{
			"statements": statements, "connection_wait_count": stats.WaitCount - baseline.WaitCount,
			"connection_wait_ms": float64(stats.WaitDuration-baseline.WaitDuration) / 1e6,
			"scope":              "test-only GORM statement callbacks; excludes outer transaction commit and RPC overhead; SQL text/values are never recorded",
		}, "", "  ")
		require.NoError(t, err)
		t.Log(string(encoded))
		if path := os.Getenv("TIMING_SQL_DIAGNOSTIC_OUTPUT"); path != "" {
			require.NoError(t, os.WriteFile(path, encoded, 0600))
		}
	})
}
