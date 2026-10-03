package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
)

func TestIAMB2ChannelBatchExportDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			r := &Repository{db: db, routingGroupDualWrite: true, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			legacy := context.Background()
			one := routingGroupModel{Key: "one", DisplayName: "one", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "restricted", Revision: 1}
			two := one
			two.Key = "two"
			two.DisplayName = "two"
			require.NoError(t, db.Create(&one).Error)
			require.NoError(t, db.Create(&two).Error)
			a := &biz.Channel{Name: "a", Group: "one", Key: "first-secret", RestrictModels: true, Status: 1}
			b := &biz.Channel{Name: "b", Group: "two", Key: "second-secret", RestrictModels: true, Status: 1}
			shared := &biz.Channel{Name: "shared", Group: "one,two", Key: "shared-secret", RestrictModels: true, Status: 1}
			for _, ch := range []*biz.Channel{a, b, shared} {
				require.NoError(t, r.CreateChannel(legacy, ch))
				stored, err := r.FindByID(legacy, ch.ID)
				require.NoError(t, err)
				ch.AuthorizationRevision = stored.AuthorizationRevision
			}
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.channel.export": authztest.Groups(one.ID), "channel.channel.batch_delete": authztest.Groups(one.ID), "channel.routing_group.members.update": authztest.All(), "channel.model_mapping.delete": authztest.All()}}
			uc := biz.NewChannelUsecase(r, nil)
			uc.SetAuthorization(policy)
			ctx := authztest.Context()
			rows, total, err := uc.ExportChannels(ctx, 1, 10, "", "", 0, 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, rows, 2)
			for _, row := range rows {
				require.Empty(t, row.Key)
				require.NotEqual(t, b.ID, row.ID)
			}
			_, _, err = uc.ListChannels(ctx, 1, 10, "", "", 0, 0)
			require.Error(t, err, "export does not borrow list")
			require.ErrorIs(t, uc.BatchDeleteChannels(ctx, []int64{a.ID, b.ID}, map[int64]int64{a.ID: a.AuthorizationRevision, b.ID: b.AuthorizationRevision}, "scope rejected"), authorization.ErrDenied)
			_, err = r.FindByID(legacy, a.ID)
			require.NoError(t, err, "one rejected item must roll back entire batch")
			require.ErrorIs(t, uc.BatchDeleteChannels(ctx, []int64{shared.ID}, map[int64]int64{shared.ID: shared.AuthorizationRevision}, "whole shared object"), authorization.ErrDenied)
			require.Error(t, uc.BatchDeleteChannels(ctx, []int64{a.ID}, map[int64]int64{a.ID: a.AuthorizationRevision + 1}, "stale revision"))
			require.NoError(t, uc.BatchDeleteChannels(ctx, []int64{a.ID}, map[int64]int64{a.ID: a.AuthorizationRevision}, "reviewed cleanup"))
			_, err = r.FindByID(legacy, a.ID)
			require.Error(t, err)
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND result = ?", "channel.channel.batch_delete", "success").Count(&audits).Error)
			require.EqualValues(t, 1, audits)
		})
	}
}

func TestIAMB2SingleDeleteChecksMappingEffectsDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			r := &Repository{db: db, routingGroupDualWrite: true, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			background := context.Background()
			group := routingGroupModel{Key: "one", DisplayName: "one", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "restricted", Revision: 1}
			require.NoError(t, db.Create(&group).Error)
			channel := &biz.Channel{Name: "mapped", Group: "one", Key: "secret", RestrictModels: true, Status: 1}
			require.NoError(t, r.CreateChannel(background, channel))
			model := &biz.Model{ModelID: "mapped-model", Status: 1}
			require.NoError(t, r.CreateModel(background, model))
			mapping := &biz.ModelChannelMapping{ChannelID: channel.ID, ModelPK: model.ID, Enabled: true}
			require.NoError(t, r.UpsertChannelMapping(background, mapping))
			stored, err := r.FindByID(background, channel.ID)
			require.NoError(t, err)
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"channel.channel.delete": authztest.Groups(group.ID), "channel.routing_group.members.update": authztest.Resources(group.ID),
			}}
			uc := biz.NewChannelUsecase(r, nil)
			uc.SetAuthorization(policy)
			ctx := authorization.WithExpectedRevision(authztest.Context(), "channel", channel.ID, stored.AuthorizationRevision)
			require.ErrorIs(t, uc.DeleteChannel(ctx, channel.ID), authorization.ErrDenied)
			current, err := r.FindByID(background, channel.ID)
			require.NoError(t, err)
			require.Equal(t, stored.AuthorizationRevision, current.AuthorizationRevision, "denied cascade must roll back the revision")
			var mappings, audits int64
			require.NoError(t, db.Table("model_channel_mapping").Where("channel_id = ?", channel.ID).Count(&mappings).Error)
			require.EqualValues(t, 1, mappings)
			require.NoError(t, db.Table("resource_write_audits").Count(&audits).Error)
			require.Zero(t, audits, "denied cascade must not commit success audits")
			policy.Scopes["channel.model_mapping.delete"] = authztest.Groups(group.ID)
			require.NoError(t, uc.DeleteChannel(ctx, channel.ID))
			require.NoError(t, db.Table("model_channel_mapping").Where("channel_id = ?", channel.ID).Count(&mappings).Error)
			require.Zero(t, mappings)
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ?", "channel.model_mapping.delete").Count(&audits).Error)
			require.EqualValues(t, 1, audits)
		})
	}
}
