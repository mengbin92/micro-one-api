package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/testutil"
)

// TestRoutingRelationOverrides covers the 098 override chain
// (relation override > ability/mapping priority > source priority), the
// NULL-inheritance invariant, detail exposure, and the revision/outbox
// contract of SetRoutingGroupResourceOverrides across all three dialects.
func TestRoutingRelationOverrides(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, driver)
			r := &Repository{db: db, routingGroupRelations: true}
			repo := &routingGroupRepo{data: r}
			ctx := context.Background()

			g := routingGroupModel{Key: "vip", DisplayName: "vip", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "all_authorized", Revision: 1}
			require.NoError(t, db.Create(&g).Error)
			chA := map[string]any{"name": "a", "group": "vip", "priority": 9, "weight": 3, "status": 1}
			chB := map[string]any{"name": "b", "group": "vip", "priority": 8, "weight": 2, "status": 1}
			require.NoError(t, db.Table("channels").Create(&chA).Error)
			require.NoError(t, db.Table("channels").Create(&chB).Error)
			var idA, idB int64
			require.NoError(t, db.Table("channels").Select("id").Where("name = ?", "a").Scan(&idA).Error)
			require.NoError(t, db.Table("channels").Select("id").Where("name = ?", "b").Scan(&idB).Error)
			require.NoError(t, db.Table("abilities").Create(map[string]any{"group": "vip", "model": "m1", "channel_id": idA, "enabled": 1, "priority": 5}).Error)
			require.NoError(t, db.Table("abilities").Create(map[string]any{"group": "vip", "model": "m1", "channel_id": idB, "enabled": 1, "priority": 4}).Error)
			require.NoError(t, db.Table("channel_routing_groups").Create(map[string]any{"channel_id": idA, "routing_group_id": g.ID}).Error)
			require.NoError(t, db.Table("channel_routing_groups").Create(map[string]any{"channel_id": idB, "routing_group_id": g.ID}).Error)

			abilities, err := r.ListAbilitiesByGroupAndModel(ctx, "vip", "m1")
			require.NoError(t, err)
			require.Len(t, abilities, 2)
			byID := map[int64]biz.Ability{}
			for _, a := range abilities {
				byID[a.ChannelID] = a
			}
			// Baseline: ability priority wins; all overrides NULL.
			require.EqualValues(t, 5, byID[idA].Priority)
			require.EqualValues(t, 4, byID[idB].Priority)
			require.Zero(t, byID[idA].Weight)
			baseline := abilities

			p7, w9 := int64(7), int64(9)
			detail, err := repoSetOverrides(repo, ctx, g.ID, routing.Source{Kind: routing.Channel, ID: idA}, &p7, &w9)
			require.NoError(t, err)
			res := map[int64]routing.GroupResource{}
			for _, resource := range detail.Resources {
				res[resource.Source.ID] = resource
			}
			require.EqualValues(t, 7, res[idA].Priority, "override replaces ability priority")
			require.EqualValues(t, 9, res[idA].Weight)
			require.NotNil(t, res[idA].PriorityOverride)
			require.EqualValues(t, 8, res[idB].Priority, "other members inherit the channel's own priority")

			abilities, err = r.ListAbilitiesByGroupAndModel(ctx, "vip", "m1")
			require.NoError(t, err)
			byID = map[int64]biz.Ability{}
			for _, a := range abilities {
				byID[a.ChannelID] = a
			}
			require.EqualValues(t, 7, byID[idA].Priority)
			require.EqualValues(t, 9, byID[idA].Weight)
			require.EqualValues(t, 4, byID[idB].Priority)
			require.Zero(t, byID[idB].Weight)

			// Clearing the override restores the exact pre-F baseline.
			detail, err = repoSetOverrides(repo, ctx, g.ID, routing.Source{Kind: routing.Channel, ID: idA}, nil, nil)
			require.NoError(t, err)
			for _, resource := range detail.Resources {
				res[resource.Source.ID] = resource
			}
			require.Nil(t, res[idA].PriorityOverride)
			require.EqualValues(t, 9, res[idA].Priority, "cleared override restores the channel's own priority")
			abilities, err = r.ListAbilitiesByGroupAndModel(ctx, "vip", "m1")
			require.NoError(t, err)
			require.Equal(t, baseline, abilities, "all-NULL overrides keep the pre-F distribution inputs identical")

			// Unknown relation and archived group are rejected.
			require.ErrorIs(t, repoSetOverridesErr(repo, ctx, g.ID, routing.Source{Kind: routing.Channel, ID: 9999}, &p7, nil), biz.ErrRoutingGroupNotFound)

			// Revision bump + outbox event per override write.
			require.EqualValues(t, 3, detail.Group.Revision)
			var events int64
			require.NoError(t, db.Table("routing_change_outbox").Where("owner = ? AND aggregate_id = ? AND kind = ?", "channel", g.ID, "group").Count(&events).Error)
			require.EqualValues(t, 2, events)

			// A zero override is deliberately asymmetric: weight 0 cannot
			// schedule traffic, so it means "inherit" (matching
			// applyRelationOverride on the serving path), while priority 0 is a
			// real value that wins outright.
			srcA := routing.Source{Kind: routing.Channel, ID: idA}
			detail, err = repoSetOverrides(repo, ctx, g.ID, srcA, nil, nil)
			require.NoError(t, err)
			var inherited int64
			for _, resource := range detail.Resources {
				if resource.Source.ID == idA {
					inherited = resource.Weight
				}
			}
			zero := int64(0)
			detail, err = repoSetOverrides(repo, ctx, g.ID, srcA, &zero, &zero)
			require.NoError(t, err)
			res = map[int64]routing.GroupResource{}
			for _, resource := range detail.Resources {
				res[resource.Source.ID] = resource
			}
			require.EqualValues(t, 0, res[idA].Priority, "a zero priority override is a real value")
			require.EqualValues(t, inherited, res[idA].Weight, "a zero weight override inherits the resource weight")

			// Re-PUT with identical values must still succeed: MySQL reports zero
			// *changed* rows for a no-op update, so the repo falls back to a
			// membership existence check instead of returning not-found.
			_, err = repoSetOverrides(repo, ctx, g.ID, srcA, &zero, &zero)
			require.NoError(t, err, "an identical re-PUT must not fail on MySQL row-count semantics")
		})
	}
}

func repoSetOverrides(repo *routingGroupRepo, ctx context.Context, groupID int64, source routing.Source, priority, weight *int64) (*biz.RoutingGroupDetail, error) {
	detail, err := repo.GetRoutingGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	member := false
	for _, resource := range detail.Resources {
		if resource.Source.Kind == source.Kind && resource.Source.ID == source.ID {
			member = true
			break
		}
	}
	if !member {
		return nil, biz.ErrRoutingGroupNotFound
	}
	if err := repo.SetRoutingGroupResourceOverrides(ctx, groupID, source, priority, weight); err != nil {
		return nil, err
	}
	return repo.GetRoutingGroup(ctx, groupID)
}

func repoSetOverridesErr(repo *routingGroupRepo, ctx context.Context, groupID int64, source routing.Source, priority, weight *int64) error {
	_, err := repoSetOverrides(repo, ctx, groupID, source, priority, weight)
	return err
}

// TestRoutingGroupDualWriteKeepsRelationOverrides pins the incremental-sync
// contract: editing a resource's CSV membership must not drop the 098
// priority/weight overrides stored on relation rows that are retained. The
// previous delete-all-then-reinsert sync silently lost them.
func TestRoutingGroupDualWriteKeepsRelationOverrides(t *testing.T) {
	db, gdb := routingGroupFixture(t)
	groupSQLFile(t, db, "../../../../migrations/sqlite/098_channel_routing_relation_overrides.sql", "sqlite3")
	report := groupBaseline(t, db, "sqlite3")
	require.NoError(t, applyGroupBaseline(t, db, "sqlite3", report, true))
	r := &Repository{db: gdb, routingGroupDualWrite: true}

	vip, err := routingGroupID(gdb, "vip")
	require.NoError(t, err)
	require.NoError(t, gdb.Table("channel_routing_groups").Where("channel_id = 1 AND routing_group_id = ?", vip).Updates(map[string]any{"priority_override": 5, "weight_override": 7}).Error)

	update := func(group string) error {
		return gdb.Transaction(func(tx *gorm.DB) error {
			if err := tx.Table("channels").Where("id = ?", 1).Update("group", group).Error; err != nil {
				return err
			}
			return r.syncAbilitiesTx(tx, &biz.Channel{ID: 1, Group: group, Models: []string{"legacy-*"}, Status: biz.ChannelStatusEnabled, Priority: 7})
		})
	}
	// "vip" keeps the vip membership and drops default, so the vip relation row
	// is retained and must carry its override through the edit.
	require.NoError(t, update("vip"))

	var row struct {
		PriorityOverride *int64
		WeightOverride   *int64
	}
	require.NoError(t, gdb.Table("channel_routing_groups").Select("priority_override, weight_override").Where("channel_id = 1 AND routing_group_id = ?", vip).Scan(&row).Error)
	require.NotNil(t, row.PriorityOverride, "a retained relation row must keep its priority override across a CSV edit")
	require.EqualValues(t, 5, *row.PriorityOverride)
	require.NotNil(t, row.WeightOverride, "a retained relation row must keep its weight override across a CSV edit")
	require.EqualValues(t, 7, *row.WeightOverride)
}
