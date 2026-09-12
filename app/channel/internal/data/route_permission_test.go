package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
)

func TestRoutePermissionMatchesSelectionAndPreservesModelGrant(t *testing.T) {
	for _, storage := range []string{"memory", "sqlite"} {
		t.Run(storage, func(t *testing.T) {
			ctx := context.Background()
			repo := newMemoryRepository()
			if storage == "sqlite" {
				repo = setupChannelTestDB(t)
				require.NoError(t, repo.db.AutoMigrate(&modelSubscriptionMappingModel{}))
			}
			uc := biz.NewChannelUsecase(repo, nil)
			uc.SetModelRoutingRepo(repo)
			account := &biz.SubscriptionAccount{Group: "default", Models: []string{"managed", "legacy-*"}, Platform: "codex", Status: 1}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
			model := &biz.Model{ModelID: "managed", Status: 1, IsPublic: true}
			require.NoError(t, repo.CreateModel(ctx, model))
			mapping := &biz.ModelSubscriptionMapping{ModelPK: model.ID, SubscriptionAccountID: account.ID, GroupName: "vip", Enabled: true, UpstreamModelID: "upstream-managed"}
			require.NoError(t, repo.UpsertSubscriptionMapping(ctx, mapping))
			source := routing.Source{Kind: routing.Subscription, ID: account.ID}
			permission, err := uc.CanRoute(ctx, "vip", "managed", source)
			require.NoError(t, err)
			require.True(t, permission.Allowed)
			require.Equal(t, "upstream-managed", permission.UpstreamModelID)
			selected, err := uc.SelectSubscriptionAccount(ctx, "vip", "managed", "codex", false)
			require.NoError(t, err)
			require.Equal(t, source.ID, selected.ID)
			require.Equal(t, permission.UpstreamModelID, selected.UpstreamModelID)
			models, err := uc.ListAvailableModels(ctx, "vip")
			require.NoError(t, err)
			require.Contains(t, models, "managed")
			// CSV membership alone cannot bypass managed-model mapping denial.
			permission, err = uc.CanRoute(ctx, "default", "managed", source)
			require.NoError(t, err)
			require.False(t, permission.Allowed)
			_, err = uc.SelectSubscriptionAccount(ctx, "default", "managed", "codex", false)
			require.Error(t, err)
			// A grant for one model never grants another model in that group.
			permission, err = uc.CanRoute(ctx, "vip", "legacy-chat", source)
			require.NoError(t, err)
			require.False(t, permission.Allowed)
			permission, err = uc.CanRoute(ctx, "default", "legacy-chat", source)
			require.NoError(t, err)
			require.True(t, permission.Allowed)
			// A model-routing pin narrows mappings; it never creates a grant.
			routingUC := biz.NewModelRoutingUsecase(repo)
			routingUC.SetCacheInvalidator(uc)
			require.NoError(t, routingUC.UpsertModelRouting(ctx, &biz.ModelRouting{GroupName: "vip", Model: "managed", SubscriptionAccountID: 999, Enabled: true}))
			permission, err = uc.CanRoute(ctx, "vip", "managed", source)
			require.NoError(t, err)
			require.False(t, permission.Allowed)
			models, err = uc.ListAvailableModels(ctx, "vip")
			require.NoError(t, err)
			require.NotContains(t, models, "managed")
		})
	}
}

func TestRoutePermissionRegistryDenialDoesNotWidenToCatchAll(t *testing.T) {
	for _, storage := range []string{"memory", "sqlite"} {
		t.Run(storage, func(t *testing.T) {
			repo := newMemoryRepository()
			if storage == "sqlite" {
				repo = setupChannelTestDB(t)
			}
			ctx := context.Background()
			channel := &biz.Channel{Group: "default,vip", Models: nil, Status: 1, RestrictModels: false}
			require.NoError(t, repo.CreateChannel(ctx, channel))
			require.NoError(t, repo.CreateModel(ctx, &biz.Model{ModelID: "managed", Status: 1, IsPublic: true}))
			uc := biz.NewChannelUsecase(repo, nil)
			for _, tt := range []struct {
				group, model string
				allowed      bool
			}{{"vip", "unknown", true}, {"vip", "managed", false}, {"other", "unknown", false}} {
				permission, err := uc.CanRoute(ctx, tt.group, tt.model, routing.Source{Kind: routing.Channel, ID: channel.ID})
				require.NoError(t, err)
				require.Equal(t, tt.allowed, permission.Allowed)
				selected, err := uc.SelectChannel(ctx, tt.group, tt.model, false)
				if tt.allowed {
					require.NoError(t, err)
					require.Equal(t, channel.ID, selected.ID)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}
