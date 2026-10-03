package data

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/authzquery"
)

func (r *Repository) ExportChannels(ctx context.Context, page, size int32, keyword, group string, status, kind int32) ([]*biz.Channel, int64, error) {
	if r.db == nil {
		if _, iam := authorization.QueryScopeFromContext(ctx, "channel.channel.export"); iam {
			return nil, 0, authorization.ErrWriteStorageUnavailable
		}
	}
	return r.ListChannels(ctx, page, size, keyword, group, status, kind)
}
func (r *Repository) BatchDeleteChannels(ctx context.Context, ids []int64, expected map[int64]int64) error {
	if r.db == nil {
		return authorization.ErrWriteStorageUnavailable
	}
	err := authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		owner := &Repository{db: tx, encKey: r.encKey, routingGroupRelations: r.routingGroupRelations}
		memberAudits := map[int64]bool{}
		mappingAudits := map[int64]bool{}
		// Lock and validate the entire batch before performing any deletion.
		for _, id := range ids {
			var row channelModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return biz.ErrChannelNotFound
				}
				return err
			}
			if _, iam := authorization.QueryScopeFromContext(ctx, "channel.channel.batch_delete"); iam {
				revision, supplied := expected[id]
				if !supplied || revision != row.AuthorizationRevision {
					return errors.Conflict("CHANNEL_REVISION_CONFLICT", "channel revision conflict")
				}
			}
			facts, err := owner.ChannelAuthorizationFacts(ctx, id)
			if err != nil {
				return err
			}
			if err = authorization.Require(ctx, "channel.channel.batch_delete", facts); err != nil {
				return err
			}
			for _, groupID := range facts.RoutingGroupIDs {
				memberAudits[groupID] = true
				if err = authorization.Require(ctx, "channel.routing_group.members.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: groupID}); err != nil {
					return err
				}
			}
			var mappings []modelChannelMappingModel
			if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ?", id).Find(&mappings).Error; err != nil {
				return err
			}
			for _, mapping := range mappings {
				mappingAudits[mapping.ID] = true
				mappingFacts := facts
				mappingFacts.ResourceID = mapping.ID
				if err = authorization.Require(ctx, "channel.model_mapping.delete", mappingFacts); err != nil {
					return err
				}
			}
		}
		for _, id := range ids {
			if err := r.syncRoutingMembersTx(tx, routing.Source{Kind: routing.Channel, ID: id}, ""); err != nil {
				return err
			}
			if err := tx.Where("channel_id = ?", id).Delete(&modelChannelMappingModel{}).Error; err != nil {
				return err
			}
			if err := tx.Where("channel_id = ?", id).Delete(&abilityModel{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ?", id).Delete(&channelModel{}).Error; err != nil {
				return err
			}
			if err := authzquery.AppendWriteAudit(ctx, tx, "channel.channel.batch_delete", id); err != nil {
				return err
			}
		}
		for groupID := range memberAudits {
			if err := authzquery.AppendWriteAudit(ctx, tx, "channel.routing_group.members.update", groupID); err != nil {
				return err
			}
		}
		for mappingID := range mappingAudits {
			if err := authzquery.AppendWriteAudit(ctx, tx, "channel.model_mapping.delete", mappingID); err != nil {
				return err
			}
		}
		return nil
	})
	return authzquery.RecordWriteFailure(ctx, r.db, "channel.channel.batch_delete", 0, err)
}
