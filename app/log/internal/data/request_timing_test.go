package data

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/requesttrace"
	dbtest "micro-one-api/platform/database/testutil"
)

func timingEntry() *biz.LogEntry {
	reservation := "reserved-attempt-2"
	s := &requesttrace.TimingSummary{
		Version: 1, ObservationID: uuid.NewString(), ObservationKind: "http", UserID: 7,
		RootRequestID: "same-root", Protocol: "sse", Endpoint: "/v1/responses", ExecutionPath: "legacy",
		Outcome: "incomplete", StartedAtMS: 1000, CollectionState: "not_recorded",
		Attempts: []requesttrace.TimingAttempt{
			{Number: 1, RequestID: "attempt-1", Outcome: "rejected", SourceKind: "channel", SourceID: 4},
			{Number: 2, RequestID: "attempt-2", ReservationID: &reservation, Outcome: "incomplete", SourceKind: "subscription", SourceID: 4},
		},
	}
	finish := s.StartedAtMS
	s.HandlerFinishedAtMS, s.HandlerDurationMS = &finish, new(float64)
	return &biz.LogEntry{UserID: s.UserID, Source: requesttrace.TimingSource, RootRequestID: s.RootRequestID,
		DedupeKey: requesttrace.TimingDedupeKey(s.ObservationID), RequestTiming: s, CreatedAt: time.Now().Add(-time.Hour)}
}

func timingSQLite(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&logModel{}, &logIngestDedupeClaimModel{}, &requestTraceAttemptRefModel{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &Repository{db: db}
}

func timingRefs(t *testing.T, r *Repository, id string) []requestTraceAttemptRefModel {
	t.Helper()
	if r.db == nil {
		return r.timingRefs[id]
	}
	var refs []requestTraceAttemptRefModel
	require.NoError(t, r.db.Where("observation_id = ?", id).Order("attempt_number").Find(&refs).Error)
	return refs
}

func TestTimingPersistenceIsolationReplayAndRetention(t *testing.T) {
	for _, driver := range []string{"memory", "sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := NewMemoryRepositoryForTest()
			if driver == "sqlite" {
				r = timingSQLite(t)
			}
			if driver == "mysql" || driver == "postgres" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			ctx := context.Background()
			usage := &biz.LogEntry{UserID: 7, ModelName: "model", Level: "consume", Source: "relay-gateway",
				Quota: 100, PromptTokens: 50, CompletionTokens: 40, CacheReadTokens: 10, CreatedAt: time.Now()}
			require.NoError(t, r.Create(ctx, usage))
			before, err := r.UsageByUser(ctx, 7, time.Time{}, time.Time{})
			require.NoError(t, err)
			e := timingEntry()
			// Even erroneously copied usage fields must never affect user usage.
			e.ModelName, e.Quota, e.PromptTokens = "model", 999, 888
			original := e.RequestTiming.Clone()
			require.NoError(t, r.Create(ctx, e))
			id := e.ID
			got, err := r.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, original, got.RequestTiming)
			require.Nil(t, got.RequestTiming.FirstContentMS)
			require.Empty(t, got.RequestTiming.OTelTraceID)
			refs := timingRefs(t, r, original.ObservationID)
			require.Len(t, refs, 2)
			require.Nil(t, refs[0].ReservationID)
			require.Equal(t, "reserved-attempt-2", *refs[1].ReservationID)
			require.EqualValues(t, 7, refs[1].UserID)
			// Neither a caller mutation nor a read result can alter stored data.
			e.RequestTiming.Attempts[0].RequestID = "changed"
			*got.RequestTiming.Attempts[1].ReservationID = "changed"
			got, err = r.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, original, got.RequestTiming)
			replay := timingEntry()
			replay.RequestTiming = original.Clone()
			replay.RequestTiming.Outcome = "success"
			replay.DedupeKey = e.DedupeKey
			require.NoError(t, r.Create(ctx, replay))
			require.Equal(t, id, replay.ID)
			got, err = r.Get(ctx, id)
			require.NoError(t, err)
			require.Equal(t, original, got.RequestTiming, "replay never overwrites original observations")
			// Same root and same reservation can belong to several observations.
			other := timingEntry()
			other.RequestTiming.OTelTraceID = "11111111111111111111111111111111"
			other.RequestTiming.SpanID = "2222222222222222"
			other.RequestTiming.CollectionState = "partial"
			require.NoError(t, r.CreateBatch(ctx, []*biz.LogEntry{other, replay}))
			require.NotEqual(t, id, other.ID)
			require.Len(t, timingRefs(t, r, other.RequestTiming.ObservationID), 2)
			storedOther, err := r.Get(ctx, other.ID)
			require.NoError(t, err)
			require.Equal(t, other.RequestTiming, storedOther.RequestTiming)
			if r.db != nil {
				var columns logModel
				require.NoError(t, r.db.Where("id = ?", other.ID).Take(&columns).Error)
				require.Equal(t, other.RequestTiming.OTelTraceID, *columns.OTelTraceID)
				require.Equal(t, other.RequestTiming.ObservationID, *columns.ObservationID)
				require.Equal(t, other.RequestTiming.StartedAtMS, *columns.RequestStartedAtMS)
				require.Equal(t, other.RequestTiming.Outcome, *columns.RequestOutcome)
			}
			after, err := r.UsageByUser(ctx, 7, time.Time{}, time.Time{})
			require.NoError(t, err)
			require.Equal(t, before, after)
			rows, total, err := r.ListByUser(ctx, 7, 1, 50, "", "")
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Equal(t, usage.ID, rows[0].ID)
			deleted, err := r.DeleteBefore(ctx, time.Now().Add(-time.Minute))
			require.NoError(t, err)
			require.EqualValues(t, 2, deleted)
			require.Empty(t, timingRefs(t, r, original.ObservationID))
			if r.db != nil {
				var n int64
				require.NoError(t, r.db.Model(&logIngestDedupeClaimModel{}).Count(&n).Error)
				require.Zero(t, n)
			}
			// Timing retention also removes its dedupe claim (legacy usage policy is unchanged).
			require.NoError(t, r.Create(ctx, replay))
			require.NotEqual(t, id, replay.ID)
		})
	}
}

func TestTimingCreateAndDeleteAreAtomic(t *testing.T) {
	r := timingSQLite(t)
	ctx := context.Background()
	e := timingEntry()
	require.NoError(t, r.db.Exec(`CREATE TRIGGER fail_timing_ref BEFORE INSERT ON request_trace_attempt_refs BEGIN SELECT RAISE(ABORT, 'ref failure'); END`).Error)
	require.Error(t, r.Create(ctx, e))
	require.Zero(t, e.ID, "a rolled back ID must not escape")
	for _, model := range []any{&logModel{}, &logIngestDedupeClaimModel{}, &requestTraceAttemptRefModel{}} {
		var n int64
		require.NoError(t, r.db.Model(model).Count(&n).Error)
		require.Zero(t, n)
	}
	require.NoError(t, r.db.Exec("DROP TRIGGER fail_timing_ref").Error)
	require.NoError(t, r.Create(ctx, e))
	require.NoError(t, r.db.Exec(`CREATE TRIGGER fail_timing_delete BEFORE DELETE ON logs BEGIN SELECT RAISE(ABORT, 'delete failure'); END`).Error)
	_, err := r.DeleteBefore(ctx, time.Now())
	require.Error(t, err)
	require.Len(t, timingRefs(t, r, e.RequestTiming.ObservationID), 2)
	var n int64
	require.NoError(t, r.db.Model(&logIngestDedupeClaimModel{}).Count(&n).Error)
	require.EqualValues(t, 1, n, "failed summary deletion must restore dedupe claim")
}

func TestTimingConcurrentReplay(t *testing.T) {
	for _, memory := range []bool{true, false} {
		t.Run(fmt.Sprint("memory=", memory), func(t *testing.T) {
			r := NewMemoryRepositoryForTest()
			if !memory {
				r = timingSQLite(t)
			}
			source := timingEntry()
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					e := *source
					e.RequestTiming = source.RequestTiming.Clone()
					if err := r.Create(context.Background(), &e); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			_, n, err := r.List(context.Background(), 1, 50, "", requesttrace.TimingSource, "")
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			require.Len(t, timingRefs(t, r, source.RequestTiming.ObservationID), 2)
		})
	}
}

func TestTimingScopedDeleteDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, driver)}
			mine, other := timingEntry(), timingEntry()
			other.UserID, other.RequestTiming.UserID = 8, 8
			require.NoError(t, r.Create(context.Background(), mine))
			require.NoError(t, r.Create(context.Background(), other))
			uc := biz.NewLogUsecase(r)
			uc.SetAuthorization(&authztest.Resolver{ActorID: 7, Scopes: map[string]authorization.QueryScope{"log.request.delete": authztest.Users(7)}})
			deleted, err := uc.DeleteLogs(authorization.WithWriteReason(authztest.Context(), "remove my traces"), biz.DeleteLogsFilter{EndTime: time.Now()})
			require.NoError(t, err)
			require.EqualValues(t, 1, deleted)
			require.Empty(t, timingRefs(t, r, mine.RequestTiming.ObservationID))
			require.Len(t, timingRefs(t, r, other.RequestTiming.ObservationID), 2)
			var claims []logIngestDedupeClaimModel
			require.NoError(t, r.db.Find(&claims).Error)
			require.Len(t, claims, 1)
			require.Equal(t, other.DedupeKey, claims[0].DedupeKey)
		})
	}
}

func TestTimingUnknownOwnerRequiresPlatformScope(t *testing.T) {
	for _, memory := range []bool{true, false} {
		t.Run(fmt.Sprint("memory=", memory), func(t *testing.T) {
			r := NewMemoryRepositoryForTest()
			if !memory {
				r = timingSQLite(t)
			}
			known, unknown := timingEntry(), timingEntry()
			unknown.UserID, unknown.RequestTiming.UserID = 0, 0
			require.NoError(t, r.Create(context.Background(), known))
			require.NoError(t, r.Create(context.Background(), unknown))
			policy := &authztest.Resolver{ActorID: 7, Scopes: map[string]authorization.QueryScope{
				"log.request.list": authztest.Resources(known.ID, unknown.ID),
				"log.request.read": authztest.Resources(known.ID, unknown.ID),
			}}
			uc := biz.NewLogUsecase(r)
			uc.SetAuthorization(policy)
			ctx := authztest.Context()
			_, err := uc.GetLog(ctx, unknown.ID)
			require.ErrorIs(t, err, authorization.ErrDenied)
			rows, n, err := uc.ListLogs(ctx, 1, 50, "", requesttrace.TimingSource, "")
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			require.Len(t, rows, 1)
			require.NotNil(t, rows[0].RequestTiming, "timing does not require prompt/content permission")
			policy.Scopes["log.request.list"] = authztest.All()
			policy.Scopes["log.request.read"] = authztest.All()
			rows, n, err = uc.ListLogs(ctx, 1, 50, "", requesttrace.TimingSource, "")
			require.NoError(t, err)
			require.EqualValues(t, 2, n)
			require.Len(t, rows, 2)
			_, err = uc.GetLog(ctx, unknown.ID)
			require.NoError(t, err)
		})
	}
}

func TestTimingDoesNotEnterSelectionAudit(t *testing.T) {
	for _, memory := range []bool{true, false} {
		t.Run(fmt.Sprint("memory=", memory), func(t *testing.T) {
			r := NewMemoryRepositoryForTest()
			if !memory {
				r = timingSQLite(t)
			}
			timing := timingEntry()
			audit := &biz.LogEntry{Source: "routing-selection", UserID: timing.UserID, RootRequestID: timing.RootRequestID, CreatedAt: timing.CreatedAt}
			require.NoError(t, r.Create(context.Background(), timing))
			require.NoError(t, r.Create(context.Background(), audit))
			rows, err := r.ListSelectionAudit(context.Background(), timing.UserID, timing.RootRequestID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, audit.ID, rows[0].ID)
		})
	}
}
