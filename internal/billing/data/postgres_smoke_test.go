//go:build manual_smoke

package data

import (
	"context"
	"os"
	"testing"
	"time"

	"micro-one-api/internal/billing/biz"
	"micro-one-api/internal/pkg/xdb"
)

func TestLedgerAggregateUsagePostgresManual(t *testing.T) {
	if os.Getenv("PG_SMOKE") != "1" {
		t.Skip("set PG_SMOKE=1 to enable")
	}
	dsn := "host=127.0.0.1 port=55432 user=postgres password=test dbname=micro_one_api sslmode=disable"
	os.Setenv("BILLING_SQL_DSN", dsn)
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM billing_ledgers").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	type row struct {
		user, model, token                string
		off                               time.Duration
		amt, bal, quota, pt, ct, rt, cost int64
	}
	rows := []row{
		{"u1", "gpt-4", "tok", -48 * time.Hour, 10, 100, 1, 2, 3, 4, 0},
		{"u1", "gpt-4", "tok", -24 * time.Hour, 10, 90, 1, 2, 3, 4, 0},
		{"u1", "gpt-4o", "tok", 0, 10, 80, 1, 2, 3, 4, 0},
	}
	for _, r := range rows {
		err := db.Exec(
			`INSERT INTO billing_ledgers(user_id,amount,balance_after,type,model_name,token_name,quota,prompt_tokens,completion_tokens,cache_read_tokens,upstream_cost,created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.user, r.amt, r.bal, "usage", r.model, r.token, r.quota, r.pt, r.ct, r.rt, r.cost, now.Add(r.off),
		).Error
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := NewData("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewLedgerRepo(data)
	buckets, totals, err := repo.AggregateUsage(context.Background(), biz.UsageFilter{
		GroupBy: []string{biz.UsageDimDay, biz.UsageDimModel},
	})
	if err != nil {
		t.Fatal(err)
	}
	if totals == nil || len(buckets) == 0 {
		t.Fatal("expected buckets and totals")
	}
	for _, b := range buckets {
		t.Logf("day=%s model=%s amount=%d quota=%d", b.Day, b.Model, b.Quota, b.Quota)
	}
	t.Logf("totals amount=%d quota=%d", totals.Quota, totals.Quota)
}
