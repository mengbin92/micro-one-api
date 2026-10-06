package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMB2CanonicalOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db, routingGroupRelations: true, routingGroupDualWrite: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			// Exercise the documented pre-constraint repair workflow in this
			// isolated database. Production migrations remain unchanged.
			require.NoError(t, db.Migrator().DropIndex(&modelModel{}, "uk_models_canonical_id"))
			first := &biz.Model{ModelID: "duplicate", DisplayName: "First", Status: 1}
			second := &biz.Model{ModelID: "DUPLICATE", DisplayName: "Second", Status: 1}
			other := &biz.Model{ModelID: "unrelated", DisplayName: "Unrelated", Status: 1}
			for _, model := range []*biz.Model{first, second, other} {
				require.NoError(t, repo.CreateModel(context.Background(), model))
			}
			alias := &biz.ModelAlias{ModelPK: second.ID, Alias: "kept-alias"}
			require.NoError(t, repo.CreateModelAlias(context.Background(), alias))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"channel.model.canonical.preflight": authztest.Resources(first.ID),
				"channel.model.canonical.merge":     authztest.All(), "channel.model.update": authztest.All(), "channel.model.delete": authztest.All(),
			}}
			uc := biz.NewModelUsecase(repo)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			report, err := uc.CanonicalModelPreflight(request)
			require.NoError(t, err)
			require.Empty(t, report.Groups)
			policy.Scopes["channel.model.canonical.preflight"] = authztest.All()
			report, err = uc.CanonicalModelPreflight(request)
			require.NoError(t, err)
			require.Len(t, report.Groups, 1)
			for _, member := range report.Groups[0].Members {
				require.Zero(t, member.Aliases, "preflight cannot expose independently protected counts")
			}
			forged := biz.DuplicateModelGroup{CanonicalID: "duplicate", SurvivingPK: first.ID, Members: []biz.DuplicateModelRef{{ModelPK: first.ID}, {ModelPK: other.ID, ModelID: "DUPLICATE", IsPrimary: true}}}
			_, err = uc.MergeCanonicalModels(request, forged)
			require.Error(t, err, "request member names cannot rename/delete unrelated stored models")
			group := report.Groups[0]
			group.SurvivingPK = first.ID
			_, err = uc.MergeCanonicalModels(request, group)
			require.Error(t, err, "merge alone cannot move aliases")
			policy.Scopes["channel.model_alias.create"] = authztest.Resources(first.ID)
			policy.Scopes["channel.model_alias.delete"] = authztest.Resources(second.ID)
			result, err := uc.MergeCanonicalModels(request, group)
			require.NoError(t, err)
			require.Equal(t, first.ID, result.SurvivingPK)
			var kept modelAliasModel
			require.NoError(t, db.First(&kept, alias.ID).Error)
			require.Equal(t, first.ID, kept.ModelPK)
			_, err = repo.GetModel(context.Background(), other.ID)
			require.NoError(t, err)
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND result = ?", "channel.model.canonical.merge", "success").Count(&audits).Error)
			require.EqualValues(t, 2, audits)
		})
	}
}
