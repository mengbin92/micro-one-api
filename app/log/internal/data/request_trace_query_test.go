package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

func TestRequestTraceFirstPageSnapshotReadIsBounded(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "mysql")
	r := &Repository{db: db}
	for start := 0; start < 2048; start += timingCommitBatchSize {
		entries := make([]*biz.LogEntry, 0, timingCommitBatchSize)
		for i := start; i < start+timingCommitBatchSize; i++ {
			entry := timingEntry()
			entry.RequestTiming.StartedAtMS = int64(1000 + i)
			finished := entry.RequestTiming.StartedAtMS
			entry.RequestTiming.HandlerFinishedAtMS = &finished
			entries = append(entries, entry)
		}
		require.NoError(t, r.CreateBatch(context.Background(), entries))
	}
	// A session counter measures index records read, avoiding wall-clock timing
	// assertions and distinguishing a bounded page from a growing MAX scan.
	require.NoError(t, db.Connection(func(conn *gorm.DB) error {
		reads := func() uint64 {
			var counters []struct {
				Name  string `gorm:"column:Variable_name"`
				Value uint64 `gorm:"column:Value"`
			}
			require.NoError(t, conn.Raw("SHOW SESSION STATUS WHERE Variable_name IN ('Handler_read_first','Handler_read_key','Handler_read_last','Handler_read_next','Handler_read_prev','Handler_read_rnd','Handler_read_rnd_next')").Scan(&counters).Error)
			var total uint64
			for _, counter := range counters {
				total += counter.Value
			}
			return total
		}
		before := reads()
		page, _, more, err := (&Repository{db: conn}).ListRequestTraces(context.Background(), biz.RequestTraceFilter{
			StartMS: 1000, EndMS: 100000, UserID: new(int64(7)), PageSize: 20,
		}, biz.RequestTraceCursor{})
		require.NoError(t, err)
		require.Len(t, page, 20)
		require.True(t, more)
		examined := reads() - before
		t.Logf("first page of 2048 summaries read %d index records", examined)
		require.LessOrEqual(t, examined, uint64(128), "snapshot acquisition must not scan every matching summary")
		return nil
	}))
}

func TestRequestTraceCursorLateIngestScopeAndReservationCandidates(t *testing.T) {
	for _, driver := range []string{"memory", "sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := NewMemoryRepositoryForTest()
			if driver != "memory" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			ctx := context.Background()
			for i := 1; i <= 4; i++ {
				entry := timingEntry()
				entry.RequestTiming.StartedAtMS = int64(1000 + i)
				finished := entry.RequestTiming.StartedAtMS
				entry.RequestTiming.HandlerFinishedAtMS = &finished
				require.NoError(t, r.Create(ctx, entry))
			}
			uc := biz.NewLogUsecase(r)
			policy := &authztest.Resolver{ActorID: 7, Scopes: map[string]authorization.QueryScope{"log.request.list": authztest.Users(7), "log.request.read": authztest.Users(7)}}
			uc.SetAuthorization(policy)
			filter := biz.RequestTraceFilter{StartMS: 1000, EndMS: 2000, PageSize: 2}
			items, token, err := uc.ListRequestTraces(authztest.Context(), filter)
			require.NoError(t, err)
			require.Len(t, items, 2)
			require.NotEmpty(t, token)
			late := timingEntry()
			late.RequestTiming.StartedAtMS = 1002
			finished := int64(1002)
			late.RequestTiming.HandlerFinishedAtMS = &finished
			require.NoError(t, r.Create(ctx, late))
			filter.PageToken = token
			next, nextToken, err := uc.ListRequestTraces(authztest.Context(), filter)
			require.NoError(t, err)
			require.Len(t, next, 2)
			require.Empty(t, nextToken)
			for _, entry := range next {
				require.NotEqual(t, late.RequestTiming.ObservationID, entry.ObservationID)
				require.NotEqual(t, items[0].ObservationID, entry.ObservationID)
				require.NotEqual(t, items[1].ObservationID, entry.ObservationID)
			}
			filter.Outcome = "success"
			_, _, err = uc.ListRequestTraces(authztest.Context(), filter)
			require.Error(t, err, "cursor cannot be reused with changed filters")
			filter.Outcome = ""
			policy.Scopes["log.request.list"] = authztest.Users(8)
			next, _, err = uc.ListRequestTraces(authztest.Context(), filter)
			require.NoError(t, err)
			require.Empty(t, next, "every page re-applies current owner scope")
			policy.Scopes["log.request.list"] = authztest.Users(7)
			owner := int64(7)
			items, _, err = uc.ListRequestTraces(authztest.Context(), biz.RequestTraceFilter{UserID: &owner, ReservationID: "reserved-attempt-2", PageSize: 200})
			require.NoError(t, err)
			require.Len(t, items, 5, "exact reservation index includes older summaries and all observations")
			_, _, err = uc.ListRequestTraces(authztest.Context(), biz.RequestTraceFilter{StartMS: 1000, EndMS: time.Now().UnixMilli()})
			require.Error(t, err, "unbounded time scans are rejected")
		})
	}
}
