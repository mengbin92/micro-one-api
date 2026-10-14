package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestChannelProviderCorrectionPersistsWithoutRotatingCredential(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	channel := &biz.Channel{Name: "ds", Type: 14, BaseURL: "https://api.deepseek.com/", Key: "fixture-key", Group: "default", Status: 1}
	require.NoError(t, r.CreateChannel(context.Background(), channel))
	channel.Type = 6
	require.NoError(t, r.UpdateChannel(context.Background(), channel))
	stored, err := r.FindByID(context.Background(), channel.ID)
	require.NoError(t, err)
	require.EqualValues(t, 6, stored.Type)
	require.Equal(t, "fixture-key", stored.Key)
	require.Empty(t, stored.Models)
}

func TestControlledInventoryRegistersTestingPrivateModelWithoutEnablingRoute(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db, encKey: []byte("0123456789abcdef0123456789abcdef")}
	channel := &biz.Channel{Name: "ds", Type: 6, Group: "default", Status: 1}
	require.NoError(t, r.CreateChannel(context.Background(), channel))
	i := biz.UpstreamInventory{SourceKind: "channel", SourceID: channel.ID, ExactModelIDs: []string{"deepseek-flash"}, AttemptID: "official-inventory", ObservedAt: time.Now().Unix(), DiscoveredModels: []*biz.Model{{ModelID: "deepseek-flash", DisplayName: "deepseek-flash", Provider: "deepseek", ModelType: "chat", Status: biz.ModelStatusTesting, IsPublic: false}}}
	_, err := r.SaveUpstreamInventory(context.Background(), i)
	require.NoError(t, err)
	model, err := r.GetModelByID(context.Background(), "deepseek-flash")
	require.NoError(t, err)
	require.EqualValues(t, biz.ModelStatusTesting, model.Status)
	require.False(t, model.IsPublic)
	mappings, err := r.ListChannelMappings(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	require.False(t, mappings[0].Enabled)
	require.Equal(t, "deepseek-flash", mappings[0].UpstreamModelID)
	stored, err := r.FindByID(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Empty(t, stored.Models)
}

func TestControlledInventoryPreservesApprovedCatalogAndExistingMapping(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db, encKey: []byte("0123456789abcdef0123456789abcdef")}
	channel := &biz.Channel{Name: "approved", Type: 6, Group: "default", Status: 1, Models: []string{"deepseek-flash"}}
	require.NoError(t, r.CreateChannel(context.Background(), channel))
	model, err := r.GetModelByID(context.Background(), "deepseek-flash")
	require.NoError(t, err)
	require.NoError(t, db.Table("models").Where("id = ?", model.ID).Updates(map[string]any{"pricing_input": 7, "status": 1, "is_public": true}).Error)
	i := biz.UpstreamInventory{SourceKind: "channel", SourceID: channel.ID, ExactModelIDs: []string{"deepseek-flash"}, AttemptID: "refresh", ObservedAt: 10, DiscoveredModels: []*biz.Model{{ModelID: "deepseek-flash", Status: biz.ModelStatusTesting, IsPublic: false}}}
	_, err = r.SaveUpstreamInventory(context.Background(), i)
	require.NoError(t, err)
	model, err = r.GetModelByID(context.Background(), "deepseek-flash")
	require.NoError(t, err)
	require.EqualValues(t, 1, model.Status)
	require.True(t, model.IsPublic)
	require.EqualValues(t, 7, model.PricingInput)
	mappings, err := r.ListChannelMappings(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	require.True(t, mappings[0].Enabled)
	i.ObservedAt = 9
	i.ExactModelIDs = []string{"stale-new"}
	i.DiscoveredModels = []*biz.Model{{ModelID: "stale-new", Status: biz.ModelStatusTesting}}
	_, err = r.SaveUpstreamInventory(context.Background(), i)
	require.NoError(t, err)
	_, err = r.GetModelByID(context.Background(), "stale-new")
	require.ErrorIs(t, err, biz.ErrModelNotFound)
}

func TestControlledSubscriptionInventoryRegistersDisabledMappings(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	r := &Repository{db: db, encKey: []byte("0123456789abcdef0123456789abcdef")}
	account := &biz.SubscriptionAccount{Name: "subscription", Platform: "codex", Group: "default", Status: 1}
	require.NoError(t, r.CreateSubscriptionAccount(context.Background(), account))
	i := biz.UpstreamInventory{SourceKind: "subscription", SourceID: account.ID, ExactModelIDs: []string{"candidate-exact"}, AttemptID: "inventory", ObservedAt: 1, DiscoveredModels: []*biz.Model{{ModelID: "candidate-exact", ModelType: "chat", Status: biz.ModelStatusTesting, IsPublic: false}}}
	_, err := r.SaveUpstreamInventory(context.Background(), i)
	require.NoError(t, err)
	mappings, err := r.ListSubscriptionMappings(context.Background(), account.ID)
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	require.Equal(t, "default", mappings[0].GroupName)
	require.Equal(t, "candidate-exact", mappings[0].UpstreamModelID)
	require.False(t, mappings[0].Enabled)
}
