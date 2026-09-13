package data

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/testutil"
)

func TestIdentityRoutingMigrationAndDualWrite(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			repo := NewRoutingBackfillRepository(db)
			ctx := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 8101, "username": "routing-user", "group": "Case", "status": 1}).Error)
			require.NoError(t, db.Table("tokens").Create(map[string]any{"id": 8102, "user_id": 8101, "key": "test-prefix", "status": 1}).Error)
			groups := []*routing.Group{{ID: 71, Key: "Case", Status: "enabled"}, {ID: 72, Key: "case", Status: "enabled"}}
			require.ErrorIs(t, repo.CheckRoutingSchema(ctx), biz.ErrRoutingFactsUnavailable)
			count, err := repo.BackfillRoutingGroups(ctx, groups, false)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			require.ErrorIs(t, repo.CheckRoutingSchema(ctx), biz.ErrRoutingFactsUnavailable)
			count, err = repo.BackfillRoutingGroups(ctx, groups, true)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			count, err = repo.BackfillRoutingGroups(ctx, groups, true)
			require.NoError(t, err)
			require.Zero(t, count)
			require.NoError(t, repo.CheckRoutingSchema(ctx))
			facts, err := repo.GetRoutingFacts(ctx, 8101, 8102, "Case")
			require.NoError(t, err)
			require.EqualValues(t, 71, facts.DefaultGroupID)
			require.Equal(t, "explicit_only", facts.PublicGroupAccess)
			require.Equal(t, "inherit", facts.TokenMode)
			require.EqualValues(t, 1, facts.TokenRevision)
			require.Len(t, facts.Grants, 1)
			require.NoError(t, db.Create(&routingGrantModel{UserID: 8101, RoutingGroupID: 71, SourceType: "admin", SourceRef: "independent", Status: "active"}).Error)
			t.Setenv("IDENTITY_ROUTING_V2", "true")
			user, err := repo.FindUserByID(ctx, 8101)
			require.NoError(t, err)
			stale := *user
			user.Group, user.DefaultRoutingGroupID = "case", 72
			// A failure between preference and grant writes must roll back both.
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_routing_grant", func(tx *gorm.DB) {
				if tx.Statement.Table == "user_routing_group_grants" {
					tx.AddError(fmt.Errorf("injected grant failure"))
				}
			}))
			require.Error(t, repo.UpdateUser(ctx, user))
			require.NoError(t, db.Callback().Create().Remove("fail_routing_grant"))
			facts, err = repo.GetRoutingFacts(ctx, 8101, 8102, "Case")
			require.NoError(t, err)
			require.EqualValues(t, 71, facts.DefaultGroupID)
			require.Len(t, facts.Grants, 2)
			require.NoError(t, repo.UpdateUser(ctx, user))
			facts, err = repo.GetRoutingFacts(ctx, 8101, 8102, "case")
			require.NoError(t, err)
			require.EqualValues(t, 72, facts.DefaultGroupID)
			require.EqualValues(t, 2, facts.AccessRevision)
			require.Len(t, facts.Grants, 2)
			require.ErrorIs(t, repo.UpdateUser(ctx, &stale), biz.ErrRoutingAccessConflict)
			_, err = repo.GetRoutingFacts(ctx, 8101, 8102, "Case")
			require.ErrorIs(t, err, biz.ErrRoutingAccessConflict)
			// Fixed is an explicit unsupported capability in phase C.
			require.NoError(t, db.Table("tokens").Where("id = ?", 8102).Update("routing_mode", "fixed").Error)
			_, err = repo.GetRoutingFacts(ctx, 8101, 8102, "case")
			require.ErrorIs(t, err, biz.ErrRoutingDefaultInvalid)
		})
	}
}

func TestIdentityRoutingBackfillUnknownGroupRollsBack(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	repo := NewRoutingBackfillRepository(db)
	for id, key := range map[int64]string{1: "default", 2: "unknown"} {
		require.NoError(t, db.Table("users").Create(map[string]any{"id": id, "username": fmt.Sprintf("user-%d", id), "group": key}).Error)
	}
	_, err := repo.BackfillRoutingGroups(context.Background(), []*routing.Group{{ID: 7, Key: "default"}}, true)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("users").Where("default_routing_group_id IS NOT NULL").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&routingGrantModel{}).Count(&count).Error)
	require.Zero(t, count)
}
