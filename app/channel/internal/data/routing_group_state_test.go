package data

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/database/testutil"
	"testing"
)

func TestRoutingGroupStateRevision(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			r := &routingGroupRepo{data: &Repository{db: db}}
			ctx := context.Background()
			g := routingGroupModel{Key: "Case", DisplayName: "Case", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "all_authorized", Revision: 1}
			require.NoError(t, db.Create(&g).Error)
			require.NoError(t, r.SetRoutingGroupState(ctx, g.ID, 1, "disabled", "restricted"))
			require.ErrorIs(t, r.SetRoutingGroupState(ctx, g.ID, 1, "enabled", "public"), biz.ErrRoutingGroupBaselineConflict)
			d, err := r.GetRoutingGroup(ctx, g.ID)
			require.NoError(t, err)
			require.Equal(t, "disabled", d.Group.Status)
			require.EqualValues(t, 2, d.Group.Revision)
			require.NoError(t, r.SetRoutingGroupState(ctx, g.ID, 2, "enabled", "public"))
			d, err = r.GetRoutingGroup(ctx, g.ID)
			require.NoError(t, err)
			require.Equal(t, "public", d.Group.AccessMode)
			require.EqualValues(t, 3, d.Group.Revision)
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_outbox", func(tx *gorm.DB) {
				if tx.Statement.Table == "routing_change_outbox" {
					tx.AddError(errors.New("outbox unavailable"))
				}
			}))
			require.Error(t, r.SetRoutingGroupState(ctx, g.ID, 3, "disabled", "restricted"))
			require.NoError(t, db.Callback().Create().Remove("fail_outbox"))
			d, err = r.GetRoutingGroup(ctx, g.ID)
			require.NoError(t, err)
			require.EqualValues(t, 3, d.Group.Revision)
			require.Equal(t, "enabled", d.Group.Status)
			var count int64
			require.NoError(t, db.Table("routing_change_outbox").Count(&count).Error)
			require.EqualValues(t, 2, count)
		})
	}
}
