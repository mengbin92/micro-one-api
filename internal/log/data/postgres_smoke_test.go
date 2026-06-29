//go:build manual_smoke

package data

import (
	"context"
	"os"
	"testing"
	"time"

	"micro-one-api/internal/pkg/xdb"
)

func TestUsageByUserPostgresManual(t *testing.T) {
	if os.Getenv("PG_SMOKE") != "1" {
		t.Skip("set PG_SMOKE=1 to enable")
	}
	dsn := "host=127.0.0.1 port=55432 user=postgres password=test dbname=micro_one_api sslmode=disable"
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM logs").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cases := []struct {
		model string
		off   time.Duration
	}{
		{"gpt-4", -48 * time.Hour},
		{"gpt-4", -24 * time.Hour},
		{"gpt-4o", 0},
	}
	for _, c := range cases {
		if err := db.Exec(
			"INSERT INTO logs(level,message,user_id,model_name,quota,prompt_tokens,completion_tokens,cache_read_tokens,created_at) VALUES('info','m',7,?,1,2,3,4,?)",
			c.model, now.Add(c.off).Unix(),
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewRepositoryFromEnv("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := r.UsageByUser(context.Background(), 7, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) == 0 {
		t.Fatal("expected aggregated rows, got 0")
	}
	for _, s := range stats {
		t.Logf("day=%s model=%s req=%d quota=%d", s.Day, s.ModelName, s.RequestCount, s.Quota)
	}
}
