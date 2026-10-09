package data

import (
	"context"
	"testing"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepository_ModelAvailabilityFollowsSourceStatus(t *testing.T) {
	for _, storage := range []string{"db", "memory"} {
		t.Run(storage, func(t *testing.T) {
			repo := newMemoryRepository()
			if storage == "db" {
				repo = setupChannelTestDB(t)
				require.NoError(t, repo.db.AutoMigrate(&modelSubscriptionMappingModel{}, &modelAliasModel{}))
			}
			ctx := context.Background()
			step := &biz.Channel{ID: 9, Name: "step", Status: biz.ChannelStatusEnabled, Group: "default", Models: []string{"step-5-preview", "step-3.7-flash"}}
			glm := &biz.Channel{ID: 1, Name: "glm-channel", Status: biz.ChannelStatusEnabled, Group: "default", Models: []string{"glm-5.3"}}
			account := &biz.SubscriptionAccount{Name: "zhipu", Status: biz.ChannelStatusEnabled, Group: "default", Models: []string{"glm-5.3"}}
			require.NoError(t, repo.CreateChannel(ctx, step))
			require.NoError(t, repo.CreateChannel(ctx, glm))
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
			if storage == "memory" {
				for _, channel := range []*biz.Channel{step, glm} {
					for _, id := range channel.Models {
						m := &biz.Model{ModelID: id, DisplayName: id, Status: biz.ModelStatusEnabled, IsPublic: true}
						require.NoError(t, repo.CreateModel(ctx, m))
						require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: channel.ID, ModelPK: m.ID, Enabled: true, EnabledHasValue: true}))
					}
				}
			}
			model, err := repo.GetModelByID(ctx, "glm-5.3")
			require.NoError(t, err)
			require.NoError(t, repo.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{SubscriptionAccountID: account.ID, ModelPK: model.ID, GroupName: "default", Enabled: true, EnabledHasValue: true}))
			// A second group does not turn one supplying account into two.
			require.NoError(t, repo.UpsertSubscriptionMapping(ctx, &biz.ModelSubscriptionMapping{SubscriptionAccountID: account.ID, ModelPK: model.ID, GroupName: "vip", Enabled: true, EnabledHasValue: true}))

			check := func(id string, status, channels, subscriptions int32, suppliers []string) {
				t.Helper()
				models, _, err := repo.ListModels(ctx, 1, 100, biz.ListModelsFilter{})
				require.NoError(t, err)
				var found *biz.Model
				for _, m := range models {
					if m.ModelID == id {
						found = m
					}
				}
				require.NotNil(t, found)
				assert.Equal(t, status, found.EffectiveStatus(), id+" status")
				assert.Equal(t, channels, found.ChannelCount, id+" channel count")
				assert.Equal(t, subscriptions, found.SubscriptionCount, id+" subscription count")
				assert.Equal(t, suppliers, found.Suppliers, id+" suppliers")
				var enabledTotal int64
				for _, m := range models {
					if m.EffectiveStatus() == biz.ModelStatusEnabled {
						enabledTotal++
					}
				}
				page, total, err := repo.ListModels(ctx, 1, 1, biz.ListModelsFilter{Status: biz.ModelStatusEnabled})
				require.NoError(t, err)
				assert.Equal(t, enabledTotal, total, "enabled filter must run before pagination")
				for _, m := range page {
					assert.EqualValues(t, biz.ModelStatusEnabled, m.EffectiveStatus())
				}
				byPK, err := repo.GetModel(ctx, found.ID)
				require.NoError(t, err)
				byID, err := repo.GetModelByID(ctx, id)
				require.NoError(t, err)
				for _, detail := range []*biz.Model{byPK, byID} {
					assert.Equal(t, status, detail.EffectiveStatus(), id+" detail status")
					assert.Equal(t, channels, detail.ChannelCount)
					assert.Equal(t, subscriptions, detail.SubscriptionCount)
				}
				available, err := repo.ListAvailableModels(ctx, "default")
				require.NoError(t, err)
				assert.Equal(t, status == biz.ModelStatusEnabled, containsModel(available, id), id+" relay availability")
			}
			check("glm-5.3", biz.ModelStatusEnabled, 1, 1, []string{"glm-channel", "zhipu"})
			require.NoError(t, repo.ChangeStatus(ctx, step.ID, biz.ChannelStatusDisabled))
			check("step-5-preview", biz.ModelStatusDisabled, 0, 0, nil)
			check("step-3.7-flash", biz.ModelStatusDisabled, 0, 0, nil)
			require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusDisabled))
			check("glm-5.3", biz.ModelStatusEnabled, 1, 0, []string{"glm-channel"})
			require.NoError(t, repo.ChangeStatus(ctx, glm.ID, biz.ChannelStatusDisabled))
			check("glm-5.3", biz.ModelStatusDisabled, 0, 0, nil)
			exported, err := repo.ExportAllModels(ctx, biz.ListModelsFilter{})
			require.NoError(t, err)
			require.Len(t, exported, 3)
			for _, m := range exported {
				assert.EqualValues(t, biz.ModelStatusEnabled, m.Status, "export preserves the configured switch")
			}
			enabledExport, err := repo.ExportAllModels(ctx, biz.ListModelsFilter{Status: biz.ModelStatusEnabled})
			require.NoError(t, err)
			assert.Empty(t, enabledExport, "export filter must agree with the list")
			require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusEnabled))
			check("glm-5.3", biz.ModelStatusEnabled, 0, 1, []string{"zhipu"})
			// Source recovery must never override a model's manual disable.
			require.NoError(t, repo.ChangeModelStatus(ctx, model.ID, biz.ModelStatusDisabled))
			require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusDisabled))
			require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusEnabled))
			check("glm-5.3", biz.ModelStatusDisabled, 0, 1, []string{"zhipu"})
		})
	}
}

func TestRepository_ModelAvailabilityIndependentOfMappingVisibility(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	model := &biz.Model{ModelID: "hidden-source", DisplayName: "Hidden source", Status: biz.ModelStatusEnabled}
	require.NoError(t, repo.CreateModel(ctx, model))
	require.NoError(t, repo.db.Exec(`INSERT INTO channels (id, name, status) VALUES (1, 'hidden', 1)`).Error)
	require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ChannelID: 1, ModelPK: model.ID, Enabled: true, EnabledHasValue: true}))
	ctx = authorization.WithQueryScope(ctx, "channel.model_mapping.read", authorization.QueryScope{ActorID: 1})
	models, total, err := repo.ListModels(ctx, 1, 10, biz.ListModelsFilter{Status: biz.ModelStatusEnabled})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, models, 1)
	assert.Zero(t, models[0].ChannelCount)
	assert.Empty(t, models[0].Suppliers)
	assert.EqualValues(t, biz.ModelStatusEnabled, models[0].EffectiveStatus())
}

func TestRepository_ModelAvailabilityDisabledFilter(t *testing.T) {
	for _, storage := range []string{"db", "memory"} {
		t.Run(storage, func(t *testing.T) {
			repo := newMemoryRepository()
			if storage == "db" {
				repo = setupModelTestDB(t)
				require.NoError(t, repo.db.Exec(`INSERT INTO channels (id, name, status) VALUES (1, 'active', 1), (2, 'disabled', 2)`).Error)
			} else {
				require.NoError(t, repo.CreateChannel(context.Background(), &biz.Channel{ID: 1, Name: "active", Status: biz.ChannelStatusEnabled}))
				require.NoError(t, repo.CreateChannel(context.Background(), &biz.Channel{ID: 2, Name: "disabled", Status: biz.ChannelStatusDisabled}))
			}
			ctx := context.Background()
			for _, fixture := range []struct {
				id             string
				status         int32
				channel        int64
				mappingEnabled bool
			}{
				{"active", biz.ModelStatusEnabled, 1, true},
				{"disabled-source", biz.ModelStatusEnabled, 2, true},
				{"unmapped", biz.ModelStatusEnabled, 0, false},
				{"manual-disabled", biz.ModelStatusDisabled, 1, true},
				{"disabled-mapping", biz.ModelStatusEnabled, 1, false},
				{"testing", biz.ModelStatusTesting, 0, false},
			} {
				model := &biz.Model{ModelID: fixture.id, DisplayName: fixture.id, Status: fixture.status}
				require.NoError(t, repo.CreateModel(ctx, model))
				if fixture.channel != 0 {
					require.NoError(t, repo.UpsertChannelMapping(ctx, &biz.ModelChannelMapping{ModelPK: model.ID, ChannelID: fixture.channel, Enabled: fixture.mappingEnabled, EnabledHasValue: true}))
				}
			}
			models, total, err := repo.ListModels(ctx, 1, 10, biz.ListModelsFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 6, total, "omitted status must include every state")
			require.Len(t, models, 6)
			filter := biz.ListModelsFilter{Status: biz.ModelStatusDisabled, StatusHasValue: true}
			models, total, err = repo.ListModels(ctx, 1, 1, filter)
			require.NoError(t, err)
			require.EqualValues(t, 4, total, "disabled filter must apply before pagination")
			require.Len(t, models, 1)
			models, _, err = repo.ListModels(ctx, 1, 10, filter)
			require.NoError(t, err)
			ids := make([]string, 0, len(models))
			for _, model := range models {
				ids = append(ids, model.ModelID)
				require.EqualValues(t, biz.ModelStatusDisabled, model.EffectiveStatus())
			}
			require.ElementsMatch(t, []string{"disabled-source", "unmapped", "manual-disabled", "disabled-mapping"}, ids)
			exported, err := repo.ExportAllModels(ctx, filter)
			require.NoError(t, err)
			ids = ids[:0]
			for _, model := range exported {
				ids = append(ids, model.ModelID)
			}
			require.ElementsMatch(t, []string{"disabled-source", "unmapped", "manual-disabled", "disabled-mapping"}, ids)
		})
	}
}

func containsModel(models []string, id string) bool {
	for _, model := range models {
		if model == id {
			return true
		}
	}
	return false
}
