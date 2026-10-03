package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"math"
	"micro-one-api/app/config/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
)

func TestIAMB4ConfigDeleteRejectsWrappedRevision(t *testing.T) {
	for _, driver := range []string{"sqlite", "memory"} {
		t.Run(driver, func(t *testing.T) {
			r := newMemoryRepository()
			if driver == "sqlite" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			require.NoError(t, r.Set(context.Background(), &biz.ConfigEntry{Namespace: "system", Key: "notice", Value: "keep"}))
			if driver == "memory" {
				r.mem["system/notice"].Revision = -1
			} else {
				require.NoError(t, r.db.Model(&configModel{}).Where("namespace = ? AND key = ?", "system", "notice").Update("revision", -1).Error)
			}
			ctx := authorization.WithQueryScope(authorization.WithWriteReason(context.Background(), "reject corrupted revision"), "system.content.notice.update", authztest.All())
			ctx = authorization.WithExpectedResourceRevision(ctx, math.MaxUint64)
			var err error
			if driver == "memory" {
				err = r.deleteMemory(ctx, "system", "notice")
			} else {
				err = r.Delete(ctx, "system", "notice")
			}
			require.ErrorIs(t, err, biz.ErrConfigRevisionConflict)
			entry, err := r.Get(context.Background(), "system", "notice")
			require.NoError(t, err)
			require.Equal(t, "keep", entry.Value)
		})
	}
}

func TestIAMB4ConfigOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres", "memory"} {
		t.Run(driver, func(t *testing.T) {
			r := newMemoryRepository()
			if driver != "memory" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			require.NoError(t, r.Set(context.Background(), &biz.ConfigEntry{Namespace: "system", Key: "StripeSecret", Value: "private"}))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.option.read": authztest.All(), "system.option.update": authztest.All(), "system.content.notice.update": authztest.All()}}
			uc := biz.NewConfigUsecase(r)
			uc.SetAuthorization(policy)
			request := authorization.WithExpectedResourceRevision(authorization.WithWriteReason(authztest.Context(), "config owner acceptance"), 0)
			entry, err := uc.GetConfig(request, "system", "StripeSecret")
			require.NoError(t, err)
			require.Empty(t, entry.Value)
			require.Error(t, uc.SetConfig(request, "system", "StripeSecret", "stolen", ""))
			require.Error(t, uc.DeleteConfig(request, "system", "StripeSecret"))
			stored, err := r.Get(context.Background(), "system", "StripeSecret")
			require.NoError(t, err)
			require.Equal(t, "private", stored.Value)
			require.Error(t, uc.SetConfig(request, "system", "ModelRatio", "{}", ""))
			require.Error(t, uc.SetConfig(request, "private", "theme", "x", ""))
			if driver == "memory" {
				require.ErrorIs(t, uc.SetConfig(request, "system", "notice", "public notice", ""), authorization.ErrWriteStorageUnavailable)
				require.NoError(t, r.Set(context.Background(), &biz.ConfigEntry{Namespace: "system", Key: "notice", Value: "public notice"}))
			} else {
				require.NoError(t, uc.SetConfig(request, "system", "notice", "public notice", ""))
			}
			entry, err = uc.PublicContent(context.Background(), "notice")
			require.NoError(t, err)
			require.Equal(t, "public notice", entry.Value)
			_, err = uc.PublicContent(context.Background(), "StripeSecret")
			require.Error(t, err)
			// A write-only content actor gets a revision without a read-after-commit error.
			delete(policy.Scopes, "system.option.read")
			request = authorization.WithExpectedResourceRevision(request, uint64(entry.Revision))
			entry, err = uc.SetConfigEntry(request, "system", "notice", "updated", "")
			if driver == "memory" {
				require.ErrorIs(t, err, authorization.ErrWriteStorageUnavailable)
			} else {
				require.NoError(t, err)
				require.Positive(t, entry.Revision)
			}
		})
	}
}

func TestIAMB4ConfigCASAuditDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, driver)}
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.content.notice.update": authztest.All()}}
			uc := biz.NewConfigUsecase(r)
			uc.SetAuthorization(policy)
			request := authorization.WithExpectedResourceRevision(authorization.WithWriteReason(authztest.Context(), "CAS acceptance"), 0)
			entry, err := uc.SetConfigEntry(request, "system", "notice", "initial", "")
			require.NoError(t, err)
			require.EqualValues(t, 1, entry.Revision)
			_, err = uc.SetConfigEntry(request, "system", "notice", "stale", "")
			require.ErrorIs(t, err, biz.ErrConfigRevisionConflict)
			request = authorization.WithExpectedResourceRevision(request, 1)
			entry, err = uc.SetConfigEntry(request, "system", "notice", "current", "")
			require.NoError(t, err)
			require.EqualValues(t, 2, entry.Revision)
			require.ErrorIs(t, uc.DeleteConfig(request, "system", "notice"), biz.ErrConfigRevisionConflict)
			require.NoError(t, r.db.Exec("DROP TABLE resource_write_audits").Error)
			request = authorization.WithExpectedResourceRevision(request, 2)
			require.Error(t, uc.DeleteConfig(request, "system", "notice"))
			failed := &biz.ConfigEntry{Namespace: "system", Key: "notice", Value: "failed audit update"}
			writeCtx := authorization.WithQueryScope(request, "system.content.notice.update", authztest.All())
			require.Error(t, r.Set(writeCtx, failed))
			require.Zero(t, failed.ID, "rolled-back storage ID must not be returned as committed metadata")
			require.Zero(t, failed.Revision, "rolled-back revision must not be returned as committed metadata")
			stored, err := r.Get(context.Background(), "system", "notice")
			require.NoError(t, err)
			require.Equal(t, "current", stored.Value)
			require.EqualValues(t, 2, stored.Revision)
		})
	}
}

func TestIAMB4ConfigDeleteRecreatePreservesRevisionDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, driver)}
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.content.notice.update": authztest.All()}}
			uc := biz.NewConfigUsecase(r)
			uc.SetAuthorization(policy)
			request := authorization.WithExpectedResourceRevision(authorization.WithWriteReason(authztest.Context(), "config key lifecycle"), 0)
			first, err := uc.SetConfigEntry(request, "system", "notice", "first incarnation", "private comment")
			require.NoError(t, err)
			require.EqualValues(t, 1, first.Revision)
			require.NoError(t, uc.DeleteConfig(authorization.WithExpectedResourceRevision(request, 1), "system", "notice"))
			_, err = r.Get(context.Background(), "system", "notice")
			require.ErrorIs(t, err, biz.ErrConfigNotFound)
			rows, total, err := r.List(context.Background(), "system", 1, 20)
			require.NoError(t, err)
			require.Zero(t, total)
			require.Empty(t, rows)
			var deleted configModel
			require.NoError(t, r.db.First(&deleted, first.ID).Error)
			require.EqualValues(t, 1, deleted.Deleted)
			require.Empty(t, deleted.Value)
			require.Empty(t, deleted.Comment)
			require.EqualValues(t, 2, deleted.Revision)
			recreated, err := uc.SetConfigEntry(request, "system", "notice", "new incarnation", "")
			require.NoError(t, err)
			require.Equal(t, first.ID, recreated.ID)
			require.EqualValues(t, 3, recreated.Revision)
			_, err = uc.SetConfigEntry(authorization.WithExpectedResourceRevision(request, 1), "system", "notice", "stale first incarnation", "")
			require.ErrorIs(t, err, biz.ErrConfigRevisionConflict)
			require.ErrorIs(t, uc.DeleteConfig(authorization.WithExpectedResourceRevision(request, 1), "system", "notice"), biz.ErrConfigRevisionConflict)
			require.NoError(t, uc.DeleteConfig(authorization.WithExpectedResourceRevision(request, 3), "system", "notice"))
			var auditIDs []string
			require.NoError(t, r.db.Table("resource_write_audits").Where("result = ?", "success").Pluck("resource_id", &auditIDs).Error)
			require.Len(t, auditIDs, 4)
			for _, id := range auditIDs {
				require.Equal(t, fmt.Sprint(first.ID), id)
			}
			require.NoError(t, r.db.Exec("DROP TABLE resource_write_audits").Error)
			failed := &biz.ConfigEntry{Namespace: "system", Key: "notice", Value: "must remain deleted"}
			writeCtx := authorization.WithQueryScope(request, "system.content.notice.update", authztest.All())
			require.Error(t, r.Set(writeCtx, failed))
			require.Zero(t, failed.ID)
			require.Zero(t, failed.Revision)
			_, err = r.Get(context.Background(), "system", "notice")
			require.ErrorIs(t, err, biz.ErrConfigNotFound)
			require.NoError(t, r.db.First(&deleted, first.ID).Error)
			require.EqualValues(t, 4, deleted.Revision)
			require.EqualValues(t, 1, deleted.Deleted)
		})
	}
}

func TestIAMB4ConfigConcurrentCreateDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			r := &Repository{db: dbtest.RoutingContextDB(t, driver)}
			uc := biz.NewConfigUsecase(r)
			uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.content.notice.update": authztest.All()}})
			request := authorization.WithExpectedResourceRevision(authorization.WithWriteReason(authztest.Context(), "concurrent key creation"), 0)
			createPair := func(expectedRevision int64) {
				start := make(chan struct{})
				results := make(chan error, 2)
				for _, value := range []string{"one", "two"} {
					go func(value string) {
						<-start
						_, err := uc.SetConfigEntry(request, "system", "notice", value, "")
						results <- err
					}(value)
				}
				close(start)
				first, second := <-results, <-results
				if first == nil {
					require.ErrorIs(t, second, biz.ErrConfigRevisionConflict)
				} else {
					require.ErrorIs(t, first, biz.ErrConfigRevisionConflict)
					require.NoError(t, second)
				}
				var count int64
				require.NoError(t, r.db.Model(&configModel{}).Where(map[string]any{"namespace": "system", "key": "notice"}).Count(&count).Error)
				require.EqualValues(t, 1, count)
				active, err := r.Get(context.Background(), "system", "notice")
				require.NoError(t, err)
				require.Equal(t, expectedRevision, active.Revision)
			}
			createPair(1)
			require.NoError(t, uc.DeleteConfig(authorization.WithExpectedResourceRevision(request, 1), "system", "notice"))
			createPair(3)
		})
	}
}
