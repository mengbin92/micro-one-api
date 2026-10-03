package data

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
)

type modelWriteKey struct{}

// Model mutations use a fresh policy decision per transaction attempt and
// lock every affected model before evaluating the complete batch.
func (r *Repository) authorizedModelWrite(ctx context.Context, ids []int64, next *biz.Model, operations []string, write func(context.Context, *Repository) error) (bool, error) {
	if ctx.Value(modelWriteKey{}) != nil {
		return false, nil
	}
	active := false
	for _, op := range operations {
		if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
			active = true
		}
	}
	if !active {
		return false, nil
	}
	if r.db == nil {
		return true, authorization.ErrDenied
	}
	err := authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
		if authorization.WriteReason(current) == "" {
			return authorization.ErrWritePrecondition
		}
		owner := &Repository{db: tx, routingGroupRelations: r.routingGroupRelations, routingGroupDualWrite: r.routingGroupDualWrite, encKey: r.encKey}
		if len(ids) == 0 {
			for _, op := range operations {
				if err := authorization.Require(current, op, authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
					return err
				}
			}
			if next != nil && (next.PricingInput != 0 || next.PricingOutput != 0 || next.PricingCacheRead != 0) {
				if err := authorization.Require(current, "billing.pricing.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
					return err
				}
			}
		}
		for _, id := range ids {
			var old modelModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, id).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return biz.ErrModelNotFound
				}
				return err
			}
			facts := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: old.ID}
			for _, op := range operations {
				if err := authorization.Require(current, op, facts); err != nil {
					return err
				}
				if op == "channel.model.delete" {
					if err := owner.checkModelDeletion(current, tx, old); err != nil {
						return err
					}
				}
			}
			if next != nil && (next.PricingInput != old.PricingInput || next.PricingOutput != old.PricingOutput || next.PricingCacheRead != old.PricingCacheRead) {
				if err := authorization.Require(current, "billing.pricing.update", facts); err != nil {
					return err
				}
				if err := authzquery.AppendWriteAudit(current, tx, "billing.pricing.update", old.ID); err != nil {
					return err
				}
			}
			if !authorization.HasExpectedRevision(current, "model", id) && next != nil && next.AuthorizationRevision > 0 {
				current = authorization.WithExpectedRevision(current, "model", id, next.AuthorizationRevision)
			}
			if err := authorization.CheckWriteRevision(current, "model", id, old.AuthorizationRevision); err != nil {
				return err
			}
		}
		if err := write(context.WithValue(current, modelWriteKey{}, true), owner); err != nil {
			return err
		}
		for _, id := range ids {
			if err := tx.Model(&modelModel{}).Where("id = ?", id).Update("authorization_revision", gorm.Expr("authorization_revision + 1")).Error; err != nil {
				return err
			}
		}
		for _, op := range operations {
			for _, id := range append([]int64{}, ids...) {
				if err := authzquery.AppendWriteAudit(current, tx, op, id); err != nil {
					return err
				}
			}
			if len(ids) == 0 {
				id := int64(0)
				if next != nil {
					id = next.ID
				}
				if err := authzquery.AppendWriteAudit(current, tx, op, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil && next != nil && len(ids) > 0 {
		next.AuthorizationRevision++
	}
	if err != nil && len(operations) > 0 {
		id := int64(0)
		if len(ids) > 0 {
			id = ids[0]
		}
		err = authzquery.RecordWriteFailure(ctx, r.db, operations[0], id, err)
	}
	return true, err
}

func modelQuery(ctx context.Context, q *gorm.DB, table, id string, operations ...string) (*gorm.DB, error) {
	return authzquery.ApplyContext(ctx, q, authzquery.Columns{Resource: table + "." + id}, operations...)
}

func modelStatusOperation(status int32) string {
	if status == biz.ModelStatusEnabled {
		return "channel.model.enable"
	}
	return "channel.model.disable"
}

func (r *Repository) mappingReadQuery(ctx context.Context, query *gorm.DB, table string, subscription bool) (*gorm.DB, error) {
	if _, scoped := authorization.QueryScopeFromContext(ctx, "channel.model_mapping.read"); !scoped {
		return query, nil
	}
	parent, column, relation := "channels", "channel_id", "channel_routing_groups"
	if subscription {
		parent, column, relation = "subscription_accounts", "subscription_account_id", "account_routing_groups"
	}
	query = query.Joins("JOIN " + parent + " ON " + parent + ".id = " + table + "." + column).Select(table + ".*")
	return authzquery.ApplyContext(ctx, query, authzquery.Columns{Resource: table + ".id", Groups: r.groupsSubselect(relation, column, parent)}, "channel.model_mapping.read")
}

func (r *Repository) deleteAuthorizedAlias(ctx context.Context, id int64) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
		var alias modelAliasModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&alias, id).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return biz.ErrAliasNotFound
			}
			return err
		}
		if err := authorization.Require(current, "channel.model_alias.delete", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: alias.ModelPK}); err != nil {
			return err
		}
		var parent modelModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, alias.ModelPK).Error; err != nil {
			return err
		}
		if err := authorization.CheckWriteRevision(current, "model", parent.ID, parent.AuthorizationRevision); err != nil {
			return err
		}
		if err := tx.Delete(&alias).Error; err != nil {
			return err
		}
		if err := tx.Model(&modelModel{}).Where("id = ?", parent.ID).Update("authorization_revision", gorm.Expr("authorization_revision + 1")).Error; err != nil {
			return err
		}
		return authzquery.AppendWriteAudit(current, tx, "channel.model_alias.delete", alias.ModelPK)
	})
}

func (r *Repository) authorizedMappingWrite(ctx context.Context, sourceID, modelPK int64, group string, subscription bool, operation string, write func(context.Context, *Repository) error) (bool, error) {
	if ctx.Value(modelWriteKey{}) != nil {
		return false, nil
	}
	active := false
	for _, op := range []string{"channel.model_mapping.create", "channel.model_mapping.update", "channel.model_mapping.delete"} {
		if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
			active = true
		}
	}
	if !active {
		return false, nil
	}
	if r.db == nil {
		return true, authorization.ErrDenied
	}
	return true, authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
		owner := &Repository{db: tx, routingGroupRelations: r.routingGroupRelations, routingGroupDualWrite: r.routingGroupDualWrite, encKey: r.encKey}
		var facts authorization.ObjectFacts
		var err error
		if subscription {
			facts, err = owner.SubscriptionAccountAuthorizationFacts(current, sourceID)
		} else {
			facts, err = owner.ChannelAuthorizationFacts(current, sourceID)
		}
		if err != nil {
			return err
		}
		var model modelModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, modelPK).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return biz.ErrModelNotFound
			}
			return err
		}
		table, column := "model_channel_mapping", "channel_id"
		if subscription {
			table, column = "model_subscription_mapping", "subscription_account_id"
		}
		q := tx.Table(table).Where(column+" = ? AND model_id = ?", sourceID, modelPK)
		if subscription && group != "" {
			q = q.Where("group_name = ?", group)
		}
		var rows []struct{ ID int64 }
		if err := q.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		op := operation
		if op == "" {
			op = "channel.model_mapping.create"
			if len(rows) > 0 {
				op = "channel.model_mapping.update"
			}
		}
		if operation != "" && len(rows) == 0 {
			return biz.ErrMappingNotFound
		}
		if len(rows) == 0 {
			facts.ResourceID = 0
			if err := authorization.Require(current, op, facts); err != nil {
				return err
			}
		}
		for _, row := range rows {
			facts.ResourceID = row.ID
			if err := authorization.Require(current, op, facts); err != nil {
				return err
			}
		}
		if subscription && group != "" {
			ids, err := owner.RoutingGroupAuthorizationIDs(current, group)
			if err != nil {
				return err
			}
			// A mapping cannot add an account to a group it does not belong to.
			for _, id := range ids {
				found := false
				for _, member := range facts.RoutingGroupIDs {
					if id == member {
						found = true
					}
				}
				if !found {
					return authorization.ErrDenied
				}
			}
		}
		if err := write(context.WithValue(current, modelWriteKey{}, true), owner); err != nil {
			return err
		}
		if err := authorization.CheckWriteRevision(current, "model", model.ID, model.AuthorizationRevision); err != nil {
			return err
		}
		if err := tx.Model(&modelModel{}).Where("id = ?", model.ID).Update("authorization_revision", gorm.Expr("authorization_revision + 1")).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			if err := q.Find(&rows).Error; err != nil {
				return err
			}
		}
		for _, row := range rows {
			if err := authzquery.AppendWriteAudit(current, tx, op, row.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) sourceHealthQuery(ctx context.Context, query *gorm.DB) (*gorm.DB, error) {
	if _, scoped := authorization.QueryScopeFromContext(ctx, "monitor.health.model.read"); !scoped {
		return query, nil
	}
	groups := "SELECT 1 FROM routing_groups rg WHERE rg.id IN ? AND ((model_health_states.source_kind = 'channel' AND EXISTS (SELECT 1 FROM channel_routing_groups rel WHERE rel.channel_id = model_health_states.source_id AND rel.routing_group_id = rg.id)) OR (model_health_states.source_kind = 'subscription' AND EXISTS (SELECT 1 FROM account_routing_groups rel WHERE rel.subscription_account_id = model_health_states.source_id AND rel.routing_group_id = rg.id)))"
	if !r.routingGroupRelations {
		return nil, authorization.ErrDenied
	}
	return authzquery.ApplyContext(ctx, query, authzquery.Columns{Resource: "model_health_states.id", Groups: groups}, "monitor.health.model.read")
}

func (r *Repository) checkImportActions(ctx context.Context, next *biz.ModelExportModel, old *existingModelView, options biz.ImportOptions) error {
	facts := authorization.ObjectFacts{Context: authorization.Platform()}
	if old != nil {
		facts.ResourceID = old.Model.ID
	}
	if err := authorization.Require(ctx, "channel.model.import", facts); err != nil {
		return err
	}
	if authorization.WriteReason(ctx) == "" {
		return authorization.ErrWritePrecondition
	}
	if old != nil {
		if err := authorization.CheckWriteRevision(authorization.WithExpectedRevision(ctx, "model", old.Model.ID, next.AuthorizationRevision), "model", old.Model.ID, old.Model.AuthorizationRevision); err != nil {
			return err
		}
	}
	action := computeImportOutcome(next, old, options).Action
	if action == "skip" {
		return nil
	}
	op := "channel.model.create"
	if old != nil {
		op = "channel.model.update"
	}
	if err := authorization.Require(ctx, op, facts); err != nil {
		return err
	}
	if old != nil && old.Model.Status != next.Status {
		if err := authorization.Require(ctx, modelStatusOperation(next.Status), facts); err != nil {
			return err
		}
	}
	if options.ImportPrices {
		for _, op := range []string{"billing.pricing.import", "billing.pricing.update"} {
			if err := authorization.Require(ctx, op, facts); err != nil {
				return err
			}
		}
	}
	if len(next.Aliases) > 0 {
		if err := authorization.Require(ctx, "channel.model_alias.create", facts); err != nil {
			return err
		}
	}
	if old != nil && len(old.Aliases) > 0 {
		if err := authorization.Require(ctx, "channel.model_alias.delete", facts); err != nil {
			return err
		}
	}
	check := func(sourceID, mappingID int64, subscription bool, operation string) error {
		var f authorization.ObjectFacts
		var err error
		if subscription {
			f, err = r.SubscriptionAccountAuthorizationFacts(ctx, sourceID)
		} else {
			f, err = r.ChannelAuthorizationFacts(ctx, sourceID)
		}
		if err != nil {
			return err
		}
		f.ResourceID = mappingID
		return authorization.Require(ctx, operation, f)
	}
	if old != nil {
		for _, m := range old.ChannelMappings {
			if err := check(m.ChannelID, m.ID, false, "channel.model_mapping.delete"); err != nil {
				return err
			}
		}
		for _, m := range old.SubscriptionMappings {
			if err := check(m.SubscriptionAccountID, m.ID, true, "channel.model_mapping.delete"); err != nil {
				return err
			}
		}
	}
	for _, m := range next.ChannelMappings {
		if err := check(m.ChannelID, 0, false, "channel.model_mapping.create"); err != nil {
			return err
		}
	}
	for _, m := range next.SubscriptionMappings {
		if err := check(m.SubscriptionAccountID, 0, true, "channel.model_mapping.create"); err != nil {
			return err
		}
		ids, err := r.RoutingGroupAuthorizationIDs(ctx, m.GroupName)
		if err != nil {
			return err
		}
		f, err := r.SubscriptionAccountAuthorizationFacts(ctx, m.SubscriptionAccountID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			found := false
			for _, member := range f.RoutingGroupIDs {
				if id == member {
					found = true
				}
			}
			if !found {
				return authorization.ErrDenied
			}
		}
	}
	return nil
}
