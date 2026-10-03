package data

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
)

func requireSourceMappingDeletes(tx *gorm.DB, r *Repository, sourceID int64, subscription bool) error {
	ctx := tx.Statement.Context
	if _, iam := authorization.QueryScopeFromContext(ctx, "channel.model_mapping.delete"); !iam {
		return nil
	}
	table, column := "model_channel_mapping", "channel_id"
	if subscription {
		table, column = "model_subscription_mapping", "subscription_account_id"
	}
	var facts authorization.ObjectFacts
	var err error
	owner := &Repository{db: tx, routingGroupRelations: r.routingGroupRelations, encKey: r.encKey}
	if subscription {
		facts, err = owner.SubscriptionAccountAuthorizationFacts(ctx, sourceID)
	} else {
		facts, err = owner.ChannelAuthorizationFacts(ctx, sourceID)
	}
	if err != nil {
		return err
	}
	var rows []struct{ ID int64 }
	if err := tx.Table(table).Where(column+" = ?", sourceID).Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		facts.ResourceID = row.ID
		if err := authorization.Require(ctx, "channel.model_mapping.delete", facts); err != nil {
			return err
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "channel.model_mapping.delete", row.ID); err != nil {
			return err
		}
	}
	return nil
}

func sourceWriteActive(ctx context.Context) bool {
	for _, op := range []string{"channel.channel.create", "channel.channel.update", "channel.account.create", "channel.account.update"} {
		if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
			return true
		}
	}
	return false
}

// Legacy mapping JSON has no stable mapping-row ID. Restrict ID-only grants
// to stored mappings; compatibility map edits need all or real group coverage.
func (r *Repository) checkCompatibilityMappingEdit(ctx context.Context, tx *gorm.DB, id int64, group string, account bool, previous, next string) error {
	if !sourceWriteActive(ctx) || previous == next {
		return nil
	}
	oldMap, newMap := map[string]string{}, map[string]string{}
	if previous != "" {
		if err := jsonx.Unmarshal([]byte(previous), &oldMap); err != nil {
			return err
		}
	}
	if next != "" {
		if err := jsonx.Unmarshal([]byte(next), &newMap); err != nil {
			return err
		}
	}
	owner := &Repository{db: tx, routingGroupRelations: r.routingGroupRelations, encKey: r.encKey}
	facts := authorization.ObjectFacts{Context: authorization.Platform()}
	var err error
	if id > 0 {
		if account {
			facts, err = owner.SubscriptionAccountAuthorizationFacts(ctx, id)
		} else {
			facts, err = owner.ChannelAuthorizationFacts(ctx, id)
		}
		if err != nil {
			return err
		}
	}
	facts.ResourceID = 0
	target := facts
	target.RoutingGroupIDs, err = owner.RoutingGroupAuthorizationIDs(ctx, group)
	if err != nil {
		return err
	}
	for key, value := range oldMap {
		op := "channel.model_mapping.delete"
		if replacement, ok := newMap[key]; ok {
			if replacement == value {
				continue
			}
			op = "channel.model_mapping.update"
		}
		for _, object := range []authorization.ObjectFacts{facts, target} {
			if err := authorization.Require(ctx, op, object); err != nil {
				return err
			}
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, op, id); err != nil {
			return err
		}
	}
	for key := range newMap {
		if _, exists := oldMap[key]; exists {
			continue
		}
		if err := authorization.Require(ctx, "channel.model_mapping.create", target); err != nil {
			return err
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "channel.model_mapping.create", id); err != nil {
			return err
		}
	}
	return nil
}

func requireSourceMappingEffect(tx *gorm.DB, r *Repository, channelID, mappingID int64, operation string) error {
	ctx := tx.Statement.Context
	if !sourceWriteActive(ctx) {
		return nil
	}
	facts, err := (&Repository{db: tx, routingGroupRelations: r.routingGroupRelations, encKey: r.encKey}).ChannelAuthorizationFacts(ctx, channelID)
	if err != nil {
		return err
	}
	facts.ResourceID = mappingID
	if err := authorization.Require(ctx, operation, facts); err != nil {
		return err
	}
	return authzquery.AppendWriteAudit(ctx, tx, operation, mappingID)
}
