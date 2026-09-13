package data

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"sort"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"

	"gorm.io/gorm/clause"
)

type routingGroupBackfillModel struct {
	ReportHash  string `gorm:"primaryKey"`
	CompletedAt int64
	GroupCount  int
}

func (routingGroupBackfillModel) TableName() string { return "routing_group_backfills" }

type routingGroupBackfillRepo struct{ audit *groupAuditRepo }

// The command owns the serializable transaction and chooses rollback (dry run)
// or commit. Schema installation is a separate explicit migration operation.
func NewRoutingGroupBackfillRepo(tx *sql.Tx, driver string, schemas GroupAuditSchemas) (biz.RoutingGroupBackfillRepo, error) {
	_, _, inventory, err := NewGroupAuditRepositories(tx, driver, schemas)
	if err != nil {
		return nil, err
	}
	return &routingGroupBackfillRepo{audit: inventory.(*groupAuditRepo)}, nil
}

func channelAuditReferences(refs []biz.GroupReference) []biz.GroupReference {
	result := []biz.GroupReference{}
	for _, ref := range refs {
		switch ref.Table {
		case "channels", "subscription_accounts", "abilities", "subscription_account_abilities", "model_subscription_mapping", "model_channel_mapping", "model_routings":
			result = append(result, ref)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, _ := jsonx.Marshal(result[i])
		b, _ := jsonx.Marshal(result[j])
		return string(a) < string(b)
	})
	return result
}

func (r *routingGroupBackfillRepo) ApplyRoutingGroupBackfill(ctx context.Context, report *biz.GroupAuditReport) (*biz.RoutingGroupBackfillResult, error) {
	db := r.audit.db.WithContext(ctx)
	if !db.Migrator().HasTable(&routingGroupModel{}) {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	inventory, err := r.audit.LoadGroupInventory(ctx)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(channelAuditReferences(inventory.References), channelAuditReferences(report.References)) {
		return nil, biz.ErrRoutingGroupBaselineConflict
	}
	legacy := biz.NewChannelUsecase(r.audit, nil)
	legacy.SetModelRoutingRepo(r.audit)
	before, err := auditGrantMatrix(ctx, legacy, report, inventory.Sources)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, report.Grants) {
		return nil, biz.ErrRoutingGroupBaselineConflict
	}
	for _, entry := range report.Migration.Groups {
		row := routingGroupModel{Key: entry.LegacyKey, DisplayName: entry.LegacyKey, Description: "", Status: entry.InitialStatus, AccessMode: entry.AccessMode, ModelAccessMode: "all_authorized", Revision: 1, CreatedAt: now(), UpdatedAt: now()}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return nil, err
		}
	}
	// Shared transaction, no separate clients and no implicit migration at boot.
	writer := &Repository{db: db, routingGroupDualWrite: true}
	for _, ref := range inventory.References {
		switch ref.Table {
		case "channels", "subscription_accounts":
			if err := writer.syncRoutingMembersTx(db, ref.Source, ref.Group); err != nil {
				return nil, err
			}
		case "model_subscription_mapping", "model_routings":
			id, err := routingGroupID(db, ref.Group)
			if err != nil {
				return nil, err
			}
			if err := db.Table(ref.Table).Where("id = ?", ref.ID).Update("routing_group_id", id).Error; err != nil {
				return nil, err
			}
		}
	}
	shadowRepo := &groupAuditRepo{Repository: &Repository{db: db, routingGroupRelations: true}}
	shadow := biz.NewChannelUsecase(shadowRepo, nil)
	shadow.SetModelRoutingRepo(shadowRepo)
	after, err := auditGrantMatrix(ctx, shadow, report, inventory.Sources)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, biz.ErrRoutingGroupBaselineConflict
	}
	encoded, err := jsonx.Marshal(report)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(encoded))
	progress := routingGroupBackfillModel{ReportHash: hash, CompletedAt: now(), GroupCount: len(report.Groups)}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&progress).Error; err != nil {
		return nil, err
	}
	return &biz.RoutingGroupBackfillResult{ReportHash: hash, Groups: len(report.Groups), Grants: len(after)}, nil
}

func auditGrantMatrix(ctx context.Context, checker biz.RoutePermissionChecker, report *biz.GroupAuditReport, sources []routing.Source) ([]biz.GroupAuditGrant, error) {
	sources = append([]routing.Source{}, sources...)
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Kind != sources[j].Kind {
			return sources[i].Kind < sources[j].Kind
		}
		return sources[i].ID < sources[j].ID
	})
	grants := []biz.GroupAuditGrant{}
	if len(report.Groups)*len(report.Models)*len(sources) > 100000 {
		return nil, biz.ErrRoutingGroupInvalid
	}
	for _, group := range report.Groups {
		for _, model := range report.Models {
			for _, source := range sources {
				p, err := checker.CanRoute(ctx, group, model, source)
				if err != nil {
					return nil, err
				}
				if p.Allowed {
					grants = append(grants, biz.GroupAuditGrant{Group: group, Model: model, Source: source, UpstreamModelID: p.UpstreamModelID})
				}
			}
		}
	}
	return grants, nil
}
