package data

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
)

// The supplied members are a concurrency precondition, never authoritative
// facts. Read and lock the complete stored canonical group before any write.
func validateCanonicalMembers(tx *gorm.DB, group biz.DuplicateModelGroup) ([]modelModel, error) {
	var rows []modelModel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("LOWER(TRIM(model_id)) = ?", group.CanonicalID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	want := make([]int64, 0, len(group.Members))
	for _, m := range group.Members {
		want = append(want, m.ModelPK)
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	actual := make([]int64, 0, len(rows))
	for _, m := range rows {
		actual = append(actual, m.ID)
	}
	if len(rows) < 2 || !slices.Equal(want, actual) || !slices.Contains(actual, group.SurvivingPK) {
		return nil, fmt.Errorf("%w: stored canonical membership changed", biz.ErrCanonicalConflict)
	}
	return rows, nil
}

func (r *Repository) authorizedCanonicalMerge(ctx context.Context, group biz.DuplicateModelGroup) (bool, *biz.MergeResult, error) {
	if ctx.Value(modelWriteKey{}) != nil {
		return false, nil, nil
	}
	if _, scoped := authorization.QueryScopeFromContext(ctx, "channel.model.canonical.merge"); !scoped {
		return false, nil, nil
	}
	if r.db == nil {
		return true, nil, authorization.ErrDenied
	}
	var result *biz.MergeResult
	err := authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
		rows, err := validateCanonicalMembers(tx, group)
		if err != nil {
			return err
		}
		owner := &Repository{db: tx, routingGroupRelations: r.routingGroupRelations, routingGroupDualWrite: r.routingGroupDualWrite, encKey: r.encKey}
		for _, row := range rows {
			facts := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: row.ID}
			if err := authorization.Require(current, "channel.model.canonical.merge", facts); err != nil {
				return err
			}
			if err := authorization.CheckWriteRevision(current, "model", row.ID, row.AuthorizationRevision); err != nil {
				return err
			}
			op := "channel.model.delete"
			if row.ID == group.SurvivingPK {
				op = "channel.model.update"
			}
			if err := authorization.Require(current, op, facts); err != nil {
				return err
			}
			if row.ID == group.SurvivingPK {
				continue
			}
			if row.PricingInput != 0 || row.PricingOutput != 0 || row.PricingCacheRead != 0 {
				if err := authorization.Require(current, "billing.pricing.update", facts); err != nil {
					return err
				}
			}
			var aliases []modelAliasModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_id = ?", row.ID).Find(&aliases).Error; err != nil {
				return err
			}
			if len(aliases) > 0 {
				if err := authorization.Require(current, "channel.model_alias.delete", facts); err != nil {
					return err
				}
				if err := authorization.Require(current, "channel.model_alias.create", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: group.SurvivingPK}); err != nil {
					return err
				}
			}
			for _, entry := range []struct {
				table, column string
				account       bool
			}{{"model_channel_mapping", "channel_id", false}, {"model_subscription_mapping", "subscription_account_id", true}} {
				var mappings []struct{ ID, SourceID int64 }
				if err := tx.Table(entry.table).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, "+entry.column+" AS source_id").Where("model_id = ?", row.ID).Order("id").Find(&mappings).Error; err != nil {
					return err
				}
				for _, mapping := range mappings {
					var source authorization.ObjectFacts
					if entry.account {
						source, err = owner.SubscriptionAccountAuthorizationFacts(current, mapping.SourceID)
					} else {
						source, err = owner.ChannelAuthorizationFacts(current, mapping.SourceID)
					}
					if err != nil {
						return err
					}
					source.ResourceID = mapping.ID
					if err := authorization.Require(current, "channel.model_mapping.update", source); err != nil {
						return err
					}
				}
			}
		}
		result, err = owner.MergeCanonicalModels(context.WithValue(current, modelWriteKey{}, true), group)
		if err != nil {
			return err
		}
		if err := tx.Model(&modelModel{}).Where("id = ?", group.SurvivingPK).Update("authorization_revision", gorm.Expr("authorization_revision + 1")).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := authzquery.AppendWriteAudit(current, tx, "channel.model.canonical.merge", row.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return true, nil, authzquery.RecordWriteFailure(ctx, r.db, "channel.model.canonical.merge", group.SurvivingPK, err)
	}
	// Dependent counts have independent field permissions, even after a merge.
	if authorization.Require(ctx, "channel.model_usage.read", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: group.SurvivingPK}) != nil {
		result.UsageStatsRepointed = 0
	}
	return true, result, nil
}

func (r *Repository) checkModelDeletion(ctx context.Context, tx *gorm.DB, row modelModel) error {
	facts := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: row.ID}
	if row.PricingInput != 0 || row.PricingOutput != 0 || row.PricingCacheRead != 0 {
		if err := authorization.Require(ctx, "billing.pricing.update", facts); err != nil {
			return err
		}
	}
	var aliases int64
	if err := tx.Model(&modelAliasModel{}).Where("model_id = ?", row.ID).Count(&aliases).Error; err != nil {
		return err
	}
	if aliases > 0 {
		if err := authorization.Require(ctx, "channel.model_alias.delete", facts); err != nil {
			return err
		}
	}
	for _, entry := range []struct {
		table, column string
		account       bool
	}{{"model_channel_mapping", "channel_id", false}, {"model_subscription_mapping", "subscription_account_id", true}} {
		var mappings []struct{ ID, SourceID int64 }
		if err := tx.Table(entry.table).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, "+entry.column+" AS source_id").Where("model_id = ?", row.ID).Find(&mappings).Error; err != nil {
			return err
		}
		for _, mapping := range mappings {
			var source authorization.ObjectFacts
			var err error
			if entry.account {
				source, err = r.SubscriptionAccountAuthorizationFacts(ctx, mapping.SourceID)
			} else {
				source, err = r.ChannelAuthorizationFacts(ctx, mapping.SourceID)
			}
			if err != nil {
				return err
			}
			source.ResourceID = mapping.ID
			if err := authorization.Require(ctx, "channel.model_mapping.delete", source); err != nil {
				return err
			}
		}
	}
	return nil
}
