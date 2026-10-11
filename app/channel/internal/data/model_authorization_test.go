package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	oauthbiz "micro-one-api/app/channel/internal/biz/oauth"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/security/serviceidentity"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicModelCatalogFiltersBeforePagination(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db}
	for _, model := range []*biz.Model{
		{ModelID: "disabled", IsPublic: true, Status: biz.ModelStatusDisabled},
		{ModelID: "private", IsPublic: false, Status: biz.ModelStatusEnabled},
		{ModelID: "public-a", IsPublic: true, Status: biz.ModelStatusEnabled, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ModelID: "public-b", IsPublic: true, Status: biz.ModelStatusEnabled},
		{ModelID: "public-unavailable", IsPublic: true, Status: biz.ModelStatusEnabled},
	} {
		require.NoError(t, repo.CreateModel(context.Background(), model))
	}
	require.NoError(t, repo.CreateChannel(context.Background(), &biz.Channel{Name: "catalog", Status: biz.ChannelStatusEnabled, Models: []string{"public-a", "public-b"}, Group: "default"}))
	uc := biz.NewModelUsecase(repo)
	ctx := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(authorization.WithExternal(context.Background()), serviceidentity.Principal{Name: "admin", Dedicated: true}), "/api.channel.v1.ChannelService/ListPublicModels")
	rows, total, err := uc.ListPublicModels(ctx, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 1)
	first := rows[0]
	rows, total, err = uc.ListPublicModels(ctx, 2, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.ElementsMatch(t, []string{"public-a", "public-b"}, []string{first.ModelID, rows[0].ModelID})
	if first.ModelID == "public-a" {
		require.Equal(t, []string{"text", "image"}, first.InputModalities)
	} else {
		require.Equal(t, []string{"text", "image"}, rows[0].InputModalities)
	}
	for _, denied := range []context.Context{
		authorization.WithExternal(context.Background()),
		serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "legacy-shared"}),
		serviceidentity.WithRPCMethod(ctx, "/api.channel.v1.ChannelService/ListModels"),
		authorization.WithCredential(ctx, "user-session"),
	} {
		_, _, err := uc.ListPublicModels(denied, 1, 10)
		require.ErrorIs(t, err, authorization.ErrDenied)
	}
}

func TestIAMB2ModelExecutionDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db, routingGroupRelations: true, routingGroupDualWrite: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			background := context.Background()
			one := &biz.Model{ModelID: "iam-one", DisplayName: "One", PricingInput: 3, Status: 1, IsPublic: true}
			two := &biz.Model{ModelID: "iam-two", DisplayName: "Two", PricingInput: 9, Status: 1, IsPublic: true}
			require.NoError(t, repo.CreateModel(background, one))
			require.NoError(t, repo.CreateModel(background, two))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"channel.model.list": authztest.Resources(one.ID), "channel.model.read": authztest.Resources(one.ID), "channel.model.update": authztest.Resources(one.ID),
				"channel.model_alias.create": authztest.Resources(one.ID), "channel.model_alias.delete": authztest.Resources(one.ID),
			}}
			uc := biz.NewModelUsecase(repo)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			rows, total, err := uc.ListModels(request, 1, 1, biz.ListModelsFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.Zero(t, rows[0].PricingInput)
			require.False(t, rows[0].PriceFieldsVisible)
			_, _, _, _, err = uc.GetModel(request, two.ID)
			require.Error(t, err)
			_, err = uc.GetModelByID(request, two.ModelID)
			require.Error(t, err)
			alias := &biz.ModelAlias{ModelPK: one.ID, Alias: "private-alias"}
			require.NoError(t, uc.CreateModelAlias(authorization.WithExpectedRevision(request, "model", one.ID, one.AuthorizationRevision), alias))
			_, aliases, mappings, subs, err := uc.GetModel(request, one.ID)
			require.NoError(t, err)
			require.Empty(t, aliases)
			require.Empty(t, mappings)
			require.Empty(t, subs)
			require.Error(t, uc.CreateModelAlias(request, &biz.ModelAlias{ModelPK: two.ID, Alias: "denied"}))
			currentModel, err := repo.GetModel(background, one.ID)
			require.NoError(t, err)
			updated := *currentModel
			updated.DisplayName = "Updated"
			require.NoError(t, uc.UpdateModel(request, &updated))
			updated.PricingInput = 7
			require.Error(t, uc.UpdateModel(request, &updated))
			stored, err := repo.GetModel(background, one.ID)
			require.NoError(t, err)
			require.EqualValues(t, 3, stored.PricingInput)
			policy.Scopes["billing.pricing.update"] = authztest.Resources(one.ID)
			require.NoError(t, uc.UpdateModel(request, &updated))
			policy.Scopes["billing.pricing.read"] = authztest.Resources(one.ID)
			row, _, _, _, err := uc.GetModel(request, one.ID)
			require.NoError(t, err)
			require.True(t, row.PriceFieldsVisible)
			require.EqualValues(t, 7, row.PricingInput)
			policy.Scopes["channel.model.batch_update"] = authztest.All()
			policy.Scopes["channel.model.disable"] = authztest.Resources(one.ID)
			_, err = uc.BatchModels(request, biz.BatchActionDisable, []int64{one.ID, two.ID})
			require.Error(t, err)
			stored, err = repo.GetModel(background, one.ID)
			require.NoError(t, err)
			require.EqualValues(t, 1, stored.Status, "mixed batch must roll back every write")
			policy.Scopes["channel.model.export"] = authztest.Resources(one.ID)
			exported, err := uc.ExportModels(request, biz.ListModelsFilter{}, false)
			require.NoError(t, err)
			require.Len(t, exported.Models, 1)
			require.Empty(t, exported.Models[0].Aliases)
			require.Zero(t, exported.Models[0].PricingInput)
			_, err = uc.ExportModels(request, biz.ListModelsFilter{}, true)
			require.Error(t, err, "price read does not grant price export")
			policy.Scopes["billing.pricing.export"] = authztest.Resources(one.ID)
			exported, err = uc.ExportModels(request, biz.ListModelsFilter{}, true)
			require.NoError(t, err)
			require.EqualValues(t, 7, exported.Models[0].PricingInput)
			// A write and its durable success event commit together.
			var events int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ?", "channel.model.update").Count(&events).Error)
			require.Positive(t, events)
			require.NoError(t, db.Exec("DROP TABLE resource_write_audits").Error)
			updated.DisplayName = "must roll back"
			require.Error(t, uc.UpdateModel(request, &updated))
			stored, err = repo.GetModel(background, one.ID)
			require.NoError(t, err)
			require.Equal(t, "Updated", stored.DisplayName)
		})
	}
}

func TestIAMB2MappingAndRoutingDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db, routingGroupRelations: true, routingGroupDualWrite: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			background := context.Background()
			g1 := routingGroupModel{Key: "first", DisplayName: "First", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "restricted", Revision: 1}
			g2 := routingGroupModel{Key: "second", DisplayName: "Second", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "restricted", Revision: 1}
			require.NoError(t, db.Create(&g1).Error)
			require.NoError(t, db.Create(&g2).Error)
			shared := &biz.Channel{Name: "shared", Group: "first,second", Key: "secret", Status: 1}
			hidden := &biz.Channel{Name: "hidden", Group: "second", Key: "hidden", Status: 1}
			require.NoError(t, repo.CreateChannel(background, shared))
			require.NoError(t, repo.CreateChannel(background, hidden))
			model := &biz.Model{ModelID: "mapped", DisplayName: "Mapped", Status: 1}
			require.NoError(t, repo.CreateModel(background, model))
			require.NoError(t, repo.UpsertChannelMapping(background, &biz.ModelChannelMapping{ChannelID: shared.ID, ModelPK: model.ID, Priority: 1}))
			require.NoError(t, repo.UpsertChannelMapping(background, &biz.ModelChannelMapping{ChannelID: hidden.ID, ModelPK: model.ID, Priority: 1}))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.model_mapping.read": authztest.Groups(g1.ID), "channel.model_mapping.update": authztest.Groups(g1.ID)}}
			uc := biz.NewModelUsecase(repo)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			request = authorization.WithExpectedRevision(request, "model", model.ID, model.AuthorizationRevision)
			rows, err := uc.ListChannelMappings(request, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, shared.ID, rows[0].ChannelID)
			require.Error(t, uc.UpsertChannelMapping(request, &biz.ModelChannelMapping{ChannelID: shared.ID, ModelPK: model.ID, Priority: 2}), "whole write must cover both groups")
			policy.Scopes["channel.model_mapping.update"] = authztest.Groups(g1.ID, g2.ID)
			require.NoError(t, uc.UpsertChannelMapping(request, &biz.ModelChannelMapping{ChannelID: shared.ID, ModelPK: model.ID, Priority: 2}))
			another := &biz.Model{ModelID: "another", Status: 1}
			require.NoError(t, repo.CreateModel(background, another))
			require.Error(t, uc.UpsertChannelMapping(request, &biz.ModelChannelMapping{ChannelID: shared.ID, ModelPK: another.ID}), "update may not create")
			q := policy.Scopes["channel.model_mapping.read"]
			q.Deny = authztest.Groups(g2.ID).Allow
			policy.Scopes["channel.model_mapping.read"] = q
			rows, err = uc.ListChannelMappings(request, 0)
			require.NoError(t, err)
			require.Empty(t, rows, "mandatory deny covers shared source")
			account := &biz.SubscriptionAccount{Name: "account", Platform: "codex", Group: "first", Status: 1}
			require.NoError(t, repo.CreateSubscriptionAccount(background, account))
			routeUC := biz.NewModelRoutingUsecase(repo)
			routeUC.SetAuthorization(policy)
			policy.Scopes["channel.model_routing.create"] = authztest.Groups(g1.ID)
			createRoute := &biz.ModelRouting{GroupName: "first", Model: "*", SubscriptionAccountID: account.ID}
			createCtx := authorization.WithExpectedResourceRevision(request, 0)
			require.NoError(t, routeUC.UpsertModelRouting(createCtx, createRoute))
			require.EqualValues(t, 1, createRoute.Revision)
			staleCreate := authorization.WithExpectedResourceRevision(request, 0)
			require.Error(t, routeUC.UpsertModelRouting(staleCreate, &biz.ModelRouting{GroupName: "first", Model: "*", SubscriptionAccountID: account.ID}), "create cannot overwrite an existing route")
			require.Error(t, routeUC.UpsertModelRouting(createCtx, &biz.ModelRouting{GroupName: "second", Model: "*", SubscriptionAccountID: account.ID}), "cross-group reference is invalid")
			policy.Scopes["channel.model_routing.update"] = authztest.Groups(g1.ID)
			updateRoute := &biz.ModelRouting{GroupName: "first", Model: "*", SubscriptionAccountID: account.ID, Priority: 5}
			updateCtx := authorization.WithExpectedResourceRevision(request, 1)
			require.NoError(t, routeUC.UpsertModelRouting(updateCtx, updateRoute))
			require.EqualValues(t, 2, updateRoute.Revision)
			require.Error(t, routeUC.UpsertModelRouting(updateCtx, &biz.ModelRouting{GroupName: "first", Model: "*", SubscriptionAccountID: account.ID, Priority: 6}), "stale route revision cannot overwrite")
			policy.Scopes["channel.model_routing.read"] = authztest.Groups(g1.ID)
			routings, err := routeUC.ListModelRoutings(request, "", "", "")
			require.NoError(t, err)
			require.Len(t, routings, 1)
		})
	}
}

func TestIAMB2ActionsRecheckRevocation(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.channel.test": authztest.All()}}
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "Bearer provider-secret", r.Header.Get("Authorization"))
		delete(policy.Scopes, "channel.channel.test")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	channel := &biz.Channel{Type: 1, Name: "test", Group: "default", Key: "provider-secret", BaseURL: upstream.URL, Status: 1}
	require.NoError(t, repo.CreateChannel(context.Background(), channel))
	uc := biz.NewChannelUsecase(repo, nil)
	uc.SetAuthorization(policy)
	_, err := uc.ExecuteChannelAction(authztest.Context(), channel.ID, "test")
	require.Error(t, err)
	require.Equal(t, 1, calls)
	stored, err := repo.FindByID(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Zero(t, stored.TestTime, "revoked action must not save its provider result")
}

func TestIAMB2OAuthSessionBindsActorAndTarget(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	group := routingGroupModel{Key: "first", DisplayName: "First", Status: "enabled", AccessMode: "restricted", Revision: 1}
	require.NoError(t, db.Create(&group).Error)
	policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.account.oauth.bind": authztest.Groups(group.ID)}}
	uc := biz.NewChannelUsecase(repo, nil)
	uc.SetAuthorization(policy)
	svc := oauthbiz.NewService(uc)
	pending, err := svc.AuthURL(authztest.Context(), "codex", oauthbiz.AuthURLRequest{Group: "first"})
	require.NoError(t, err)
	stolen := authorization.WithCredential(authztest.Context(), "different-session")
	_, err = svc.Exchange(stolen, "codex", oauthbiz.ExchangeRequest{SessionID: pending.SessionID, State: pending.State, Code: "unused", Group: "first"})
	require.ErrorIs(t, err, oauthbiz.ErrInvalidSession)
	_, err = svc.Exchange(authztest.Context(), "codex", oauthbiz.ExchangeRequest{SessionID: pending.SessionID, State: pending.State, Code: "unused", Group: "other"})
	require.ErrorIs(t, err, oauthbiz.ErrInvalidSession)
	delete(policy.Scopes, "channel.account.oauth.bind")
	_, err = svc.Exchange(authztest.Context(), "codex", oauthbiz.ExchangeRequest{SessionID: pending.SessionID, State: pending.State, Code: "unused", Group: "first"})
	require.Error(t, err)
}

// A model operator editing a redacted DTO must never clear stored prices or
// need price authority merely to update the model's nonfinancial fields.
func TestIAMCRedactedModelUpdatePreservesPrices(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db}
	stored := &biz.Model{ModelID: "c-priced", DisplayName: "Priced", PricingInput: 7, PricingOutput: 9, PricingCacheRead: 2, Status: 1}
	require.NoError(t, repo.CreateModel(context.Background(), stored))
	policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.model.read": authztest.Resources(stored.ID), "channel.model.update": authztest.Resources(stored.ID)}}
	uc := biz.NewModelUsecase(repo)
	uc.SetAuthorization(policy)
	request := authztest.Context()
	redacted, _, _, _, err := uc.GetModel(request, stored.ID)
	require.NoError(t, err)
	require.False(t, redacted.PriceFieldsVisible)
	redacted.DisplayName = "Edited without finance"
	redacted.PreservePricing = true
	require.NoError(t, uc.UpdateModel(request, redacted))
	actual, err := repo.GetModel(context.Background(), stored.ID)
	require.NoError(t, err)
	require.Equal(t, "Edited without finance", actual.DisplayName)
	require.EqualValues(t, 7, actual.PricingInput)
	require.EqualValues(t, 9, actual.PricingOutput)
	require.EqualValues(t, 2, actual.PricingCacheRead)
	redacted.PreservePricing = false
	redacted.AuthorizationRevision = actual.AuthorizationRevision
	require.Error(t, uc.UpdateModel(request, redacted), "clearing a real price still requires finance permission")
	request = authorization.WithExpectedRevision(request, "model", actual.ID, actual.AuthorizationRevision-1)
	redacted.PreservePricing = true
	require.ErrorIs(t, uc.UpdateModel(request, redacted), authorization.ErrWriteConflict)
}
