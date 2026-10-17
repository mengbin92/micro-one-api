package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
	"time"
)

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
