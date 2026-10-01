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

func TestIAMB4LogOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres", "memory"} {
		t.Run(driver, func(t *testing.T) {
			repo := NewMemoryRepositoryForTest()
			if driver != "memory" {
				repo = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			entries := []*biz.LogEntry{{UserID: 10, Message: "hidden payload", ModelName: "model", Quota: 2, CreatedAt: time.Now()}, {UserID: 20, Message: "hidden payload", ModelName: "model", Quota: 5, CreatedAt: time.Now()}, {UserID: 10, Message: "another", ModelName: "model", Quota: 3, CreatedAt: time.Now()}}
			for _, e := range entries {
				require.NoError(t, repo.Create(context.Background(), e))
			}
			policy := &authztest.Resolver{ActorID: 10, Scopes: map[string]authorization.QueryScope{"log.request.list": authztest.Users(10), "log.request.read": authztest.Users(10), "log.request.stats.read": authztest.Users(10), "log.request.delete": authztest.Users(10)}}
			uc := biz.NewLogUsecase(repo)
			uc.SetAuthorization(policy)
			ctx := authztest.Context()
			rows, total, err := uc.ListLogs(ctx, 1, 1, "", "", "")
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, rows, 1)
			require.Empty(t, rows[0].Message)
			_, err = uc.GetLog(ctx, entries[1].ID)
			require.Error(t, err)
			rows, total, err = uc.ListLogs(ctx, 1, 20, "", "", "hidden")
			require.NoError(t, err)
			require.Zero(t, total)
			require.Empty(t, rows, "search cannot infer denied content")
			policy.Scopes["log.request.content.read"] = authztest.Resources(entries[0].ID)
			rows, total, err = uc.ListLogs(ctx, 1, 20, "", "", "hidden")
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Equal(t, "hidden payload", rows[0].Message)
			stats, err := uc.UserUsageStats(ctx, 20, time.Time{}, time.Time{})
			require.NoError(t, err)
			require.Empty(t, stats)
			// Self endpoints do not need an administrative read grant, but prove identity.
			selfPolicy := &authztest.Resolver{ActorID: 10, Scopes: map[string]authorization.QueryScope{}}
			selfUC := biz.NewLogUsecase(repo)
			selfUC.SetAuthorization(selfPolicy)
			rows, total, err = selfUC.ListOwnLogs(ctx, 10, 1, 20, "", "")
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			_, _, err = selfUC.ListOwnLogs(ctx, 20, 1, 20, "", "")
			require.Error(t, err)
			_, err = selfUC.OwnUsageStats(ctx, 20, time.Time{}, time.Time{})
			require.Error(t, err)

			n, err := uc.DeleteLogs(ctx, biz.DeleteLogsFilter{EndTime: time.Now().Add(time.Hour)})
			require.NoError(t, err)
			require.EqualValues(t, 2, n)
			left, err := repo.Get(context.Background(), entries[1].ID)
			require.NoError(t, err)
			require.EqualValues(t, 20, left.UserID)
		})
	}
}
