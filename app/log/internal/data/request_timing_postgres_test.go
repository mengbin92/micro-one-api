package data

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/log/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestTimingPostgresFreshClaimsNeedNoReadOrRewrite(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "postgres")
	r := &Repository{db: db}
	var fresh atomic.Bool
	fresh.Store(true)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reject_fresh_claim_scan", func(tx *gorm.DB) {
		if fresh.Load() && tx.Statement.Table == "log_ingest_dedupe_claims" {
			tx.AddError(errors.New("fresh claims must come from INSERT RETURNING"))
		}
	}))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reject_claim_rewrite", func(tx *gorm.DB) {
		if tx.Statement.Table == "log_ingest_dedupe_claims" {
			tx.AddError(errors.New("claim ID must be final at insertion"))
		}
	}))
	first, second := timingEntry(), timingEntry()
	require.NoError(t, r.CreateBatch(context.Background(), []*biz.LogEntry{first, second}))
	require.Positive(t, first.ID)
	require.Positive(t, second.ID)
	fresh.Store(false)
	originalID := first.ID
	first.RequestTiming.Outcome = "success"
	require.NoError(t, r.CreateBatch(context.Background(), []*biz.LogEntry{first, second}))
	require.Equal(t, originalID, first.ID)
	stored, err := r.Get(context.Background(), first.ID)
	require.NoError(t, err)
	require.Equal(t, "incomplete", stored.RequestTiming.Outcome, "replay cannot overwrite the original observation")
}

func TestTimingPostgresClaimOrderIsCompatibleWithLegacyWriter(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "postgres")
	r := &Repository{db: db}
	original := timingEntry()
	start := make(chan struct{})
	type result struct {
		id  int64
		err error
	}
	results := make(chan result, 2)
	for _, legacy := range []bool{true, false} {
		go func() {
			<-start
			entry := *original
			entry.RequestTiming = original.RequestTiming.Clone()
			var err error
			if legacy {
				err = r.createDB(context.Background(), &entry)
			} else {
				err = r.Create(context.Background(), &entry)
			}
			results <- result{entry.ID, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Positive(t, first.id)
	require.Equal(t, first.id, second.id)
}
