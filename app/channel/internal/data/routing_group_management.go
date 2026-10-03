package data

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/authzquery"
	"micro-one-api/platform/routingoutbox"
	"sort"
	"strings"
	"time"
)

func (r *routingGroupRepo) ArchiveRoutingGroup(ctx context.Context, id, revision int64) (*biz.RoutingGroup, error) {
	if r.data.db == nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	var out *biz.RoutingGroup
	err := authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var current routingGroupModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrRoutingGroupNotFound
			}
			return err
		}
		if err := authorization.Require(ctx, "channel.routing_group.archive", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: current.ID}); err != nil {
			return err
		}
		if current.Key == routing.DefaultGroup || current.Status == "archived" {
			return biz.ErrRoutingGroupInvalid
		}
		if current.Revision != revision {
			return biz.ErrRoutingGroupBaselineConflict
		}
		result := tx.Model(&routingGroupModel{}).Where("id = ? AND revision = ?", id, revision).Updates(map[string]any{"status": "archived", "revision": revision + 1, "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingGroupBaselineConflict
		}
		if err := routingoutbox.Enqueue(tx, "channel", "group", id, revision+1); err != nil {
			return err
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "channel.routing_group.archive", id); err != nil {
			return err
		}
		current.Status = "archived"
		current.Revision++
		current.UpdatedAt = time.Now().Unix()
		out = toRoutingGroup(&current)
		return nil
	})
	return out, authzquery.RecordWriteFailure(ctx, r.data.db, "channel.routing_group.archive", id, err)
}

type groupMembershipRow struct {
	ID    int64
	Group string
}

// ReplaceRoutingGroupMembers uses both the serving CSV facts and relation
// projection to discover every old member. Retained relations preserve their
// overrides; model-scoped grants are independent and remain unchanged.
func (r *routingGroupRepo) ReplaceRoutingGroupMembers(ctx context.Context, id, revision int64, members []routing.Source) (*biz.RoutingGroup, error) {
	if r.data.db == nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	var out *biz.RoutingGroup
	err := authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := routingGroupSchemaReady(tx); err != nil {
			return err
		}
		var group routingGroupModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrRoutingGroupNotFound
			}
			return err
		}
		if err := authorization.Require(ctx, "channel.routing_group.members.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
			return err
		}
		if group.Status == "archived" {
			return biz.ErrRoutingGroupInvalid
		}
		if group.Revision != revision {
			return biz.ErrRoutingGroupBaselineConflict
		}
		desired := map[routing.Source]bool{}
		affected := map[routing.Source]bool{}
		projectedMemberships := map[routing.Source]bool{}
		for _, source := range members {
			desired[source] = true
			affected[source] = true
		}
		for _, kind := range []string{routing.Channel, routing.Subscription} {
			table, relation, column := "channels", "channel_routing_groups", "channel_id"
			if kind == routing.Subscription {
				table, relation, column = "subscription_accounts", "account_routing_groups", "subscription_account_id"
			}
			var rows []groupMembershipRow
			if err := tx.Table(table).Clauses(clause.Select{Columns: []clause.Column{{Name: "id"}, {Name: "group"}}}).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				if routing.ContainsGroup(row.Group, group.Key) {
					affected[routing.Source{Kind: kind, ID: row.ID}] = true
				}
			}
			var projected []int64
			if err := tx.Table(relation).Where("routing_group_id = ?", id).Pluck(column, &projected).Error; err != nil {
				return err
			}
			for _, sourceID := range projected {
				source := routing.Source{Kind: kind, ID: sourceID}
				affected[source] = true
				projectedMemberships[source] = true
			}
		}
		sources := make([]routing.Source, 0, len(affected))
		for source := range affected {
			sources = append(sources, source)
		}
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].Kind == sources[j].Kind {
				return sources[i].ID < sources[j].ID
			}
			return sources[i].Kind < sources[j].Kind
		})
		// Dual-write the replacement even on a serving CSV deployment. It cannot
		// silently leave the projection or ability rows behind.
		writer := &Repository{db: tx, routingGroupDualWrite: true, routingGroupRelations: r.data.routingGroupRelations, encKey: r.data.encKey}
		for _, source := range sources {
			if source.Kind == routing.Channel {
				var old channelModel
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, source.ID).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return biz.ErrChannelNotFound
					}
					return err
				}
				next := replaceGroupMembership(old.Group, group.Key, desired[source])
				if next == old.Group && projectedMemberships[source] == desired[source] {
					continue
				}
				// The request CAS is the enclosing group revision. Fence this
				// internal source projection against the locked source snapshot.
				writeCtx := authorization.WithExpectedRevision(ctx, "channel", source.ID, old.AuthorizationRevision)
				if err := r.data.checkResourceTx(writeCtx, tx, source.ID, next, false, "channel.channel.update"); err != nil {
					return err
				}
				if err := requireAllMemberGroups(ctx, tx, old.Group, next); err != nil {
					return err
				}
				if err := tx.Model(&channelModel{}).Where("id = ?", source.ID).Updates(map[string]any{"group": next, "authorization_revision": gorm.Expr("authorization_revision + 1")}).Error; err != nil {
					return err
				}
				old.Group = next
				if err := writer.syncAbilitiesTx(tx, r.data.modelToChannel(&old)); err != nil {
					return err
				}
				if err := authzquery.AppendWriteAudit(ctx, tx, "channel.channel.update", source.ID); err != nil {
					return err
				}
			} else {
				var old subscriptionAccountModel
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, source.ID).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return biz.ErrSubscriptionAccountNotFound
					}
					return err
				}
				next := replaceGroupMembership(old.Group, group.Key, desired[source])
				if next == old.Group && projectedMemberships[source] == desired[source] {
					continue
				}
				writeCtx := authorization.WithExpectedRevision(ctx, "account", source.ID, old.CredentialRevision)
				if err := r.data.checkResourceTx(writeCtx, tx, source.ID, next, true, "channel.account.update"); err != nil {
					return err
				}
				if err := requireAllMemberGroups(ctx, tx, old.Group, next); err != nil {
					return err
				}
				if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", source.ID).Updates(map[string]any{"group": next, "credential_revision": gorm.Expr("credential_revision + 1")}).Error; err != nil {
					return err
				}
				old.Group = next
				if err := writer.syncSubscriptionAccountAbilitiesTx(tx, r.data.subscriptionAccountModelToBiz(&old)); err != nil {
					return err
				}
				if err := authzquery.AppendWriteAudit(ctx, tx, "channel.account.update", source.ID); err != nil {
					return err
				}
			}
		}
		// Member sync may increment the target group once for each changed resource.
		// Publish the final revision as the CAS result, with one final outbox event.
		if err := tx.Model(&routingGroupModel{}).Where("id = ?", id).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		if err := tx.First(&group, id).Error; err != nil {
			return err
		}
		if err := routingoutbox.Enqueue(tx, "channel", "group", id, group.Revision); err != nil {
			return err
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "channel.routing_group.members.update", id); err != nil {
			return err
		}
		out = toRoutingGroup(&group)
		return nil
	})
	return out, authzquery.RecordWriteFailure(ctx, r.data.db, "channel.routing_group.members.update", id, err)
}
func replaceGroupMembership(csv, key string, include bool) string {
	out := []string{}
	present := false
	for _, old := range routing.Groups(csv) {
		if old == key {
			present = true
			if !include {
				continue
			}
		}
		out = append(out, old)
	}
	if include && !present {
		out = append(out, key)
	}
	return strings.Join(out, ",")
}

func requireAllMemberGroups(ctx context.Context, tx *gorm.DB, old, next string) error {
	keys := map[string]bool{}
	for _, key := range append(routing.Groups(old), routing.Groups(next)...) {
		keys[key] = true
	}
	for key := range keys {
		id, err := routingGroupID(tx, key)
		if err != nil {
			return err
		}
		if err := authorization.Require(ctx, "channel.routing_group.members.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
			return err
		}
	}
	return nil
}
