package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"sync"
	"testing"
	"time"
)

func TestRequestTraceCursorSurvivesDeletionBetweenPageReads(t *testing.T) {
	r := timingSQLite(t)
	entries := make([]*biz.LogEntry, 4)
	for i := range entries {
		entries[i] = timingEntry()
		entries[i].RequestTiming.StartedAtMS = int64(1000 + i)
		finished := entries[i].RequestTiming.StartedAtMS
		entries[i].RequestTiming.HandlerFinishedAtMS = &finished
		require.NoError(t, r.Create(context.Background(), entries[i]))
	}
	var once sync.Once
	require.NoError(t, r.db.Callback().Query().Before("gorm:query").Register("purge_selected_timing_page", func(tx *gorm.DB) {
		if tx.Statement.Table != "logs" {
			return
		}
		where, _ := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
		materializing := false
		for _, expression := range where.Exprs {
			if predicate, ok := expression.(clause.Expr); ok && predicate.SQL == "id IN ?" {
				materializing = true
			}
		}
		if !materializing {
			return
		}
		once.Do(func() {
			ids := []int64{entries[2].ID, entries[3].ID}
			observations := []string{entries[2].RequestTiming.ObservationID, entries[3].RequestTiming.ObservationID}
			require.NoError(t, r.db.Transaction(func(purge *gorm.DB) error {
				if err := purge.Where("observation_id IN ?", observations).Delete(&requestTraceAttemptRefModel{}).Error; err != nil {
					return err
				}
				if err := purge.Where("log_id IN ?", ids).Delete(&logIngestDedupeClaimModel{}).Error; err != nil {
					return err
				}
				return purge.Where("id IN ?", ids).Delete(&logModel{}).Error
			}))
		})
	}))
	uc := biz.NewLogUsecase(r)
	filter := biz.RequestTraceFilter{StartMS: 1000, EndMS: 2000, PageSize: 2}
	items, token, err := uc.ListRequestTraces(context.Background(), filter)
	require.NoError(t, err)
	require.Empty(t, items, "selected page was concurrently purged")
	require.NotEmpty(t, token, "cursor must advance past purged keys")
	filter.PageToken = token
	items, token, err = uc.ListRequestTraces(context.Background(), filter)
	require.NoError(t, err, "a concurrently empty page must not produce an invalid cursor")
	require.Len(t, items, 2)
	require.Empty(t, token)
}

func TestRequestTraceSQLitePageKeysUseCoveringIndex(t *testing.T) {
	for _, userFilter := range []bool{false, true} {
		t.Run(map[bool]string{false: "all_users", true: "one_user"}[userFilter], func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, "sqlite")}
			entry := timingEntry()
			require.NoError(t, r.Create(context.Background(), entry))
			var pageSQL string
			var pageArgs []any
			require.NoError(t, r.db.Callback().Query().After("gorm:query").Register("capture_page_keys", func(tx *gorm.DB) {
				if len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "id, request_started_at_ms" {
					pageSQL = tx.Statement.SQL.String()
					pageArgs = append([]any(nil), tx.Statement.Vars...)
				}
			}))
			filter := biz.RequestTraceFilter{StartMS: 1, EndMS: time.Now().Add(time.Hour).UnixMilli(), PageSize: 20}
			if userFilter {
				filter.UserID = new(int64(7))
			}
			_, _, _, err := r.ListRequestTraces(context.Background(), filter, biz.RequestTraceCursor{})
			require.NoError(t, err)
			require.NotEmpty(t, pageSQL)
			var plan []struct{ Detail string }
			require.NoError(t, r.db.Raw("EXPLAIN QUERY PLAN "+pageSQL, pageArgs...).Scan(&plan).Error)
			for _, step := range plan {
				require.NotContains(t, step.Detail, "TEMP B-TREE", "page keys must not sort the growing history: %s", pageSQL)
			}
		})
	}
}

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
