package data

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type routingGroupModel struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	Key             string `gorm:"column:key"`
	DisplayName     string
	Description     string
	Status          string
	AccessMode      string
	ModelAccessMode string
	SortOrder       int32
	Revision        int64
	CreatedAt       int64
	UpdatedAt       int64
}

func (routingGroupModel) TableName() string { return "routing_groups" }

type channelRoutingGroupModel struct {
	ChannelID      int64
	RoutingGroupID int64
}

func (channelRoutingGroupModel) TableName() string { return "channel_routing_groups" }

type accountRoutingGroupModel struct {
	SubscriptionAccountID int64
	RoutingGroupID        int64
}

func (accountRoutingGroupModel) TableName() string { return "account_routing_groups" }

type routingGroupRepo struct{ data *Repository }

func NewRoutingGroupRepo(d *Repository) biz.RoutingGroupRepo { return &routingGroupRepo{data: d} }

func toRoutingGroup(po *routingGroupModel) *biz.RoutingGroup {
	return &biz.RoutingGroup{ID: po.ID, Key: po.Key, DisplayName: po.DisplayName, Description: po.Description, Status: po.Status, AccessMode: po.AccessMode, ModelAccessMode: po.ModelAccessMode, SortOrder: po.SortOrder, Revision: po.Revision, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt}
}
func (r *routingGroupRepo) ListRoutingGroups(ctx context.Context, options biz.RoutingGroupListOptions) ([]*biz.RoutingGroup, error) {
	if r.data.db == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	var rows []routingGroupModel
	query := r.data.db.WithContext(ctx)
	for key, value := range options.Filter {
		if key != "key" && key != "status" && key != "access_mode" {
			return nil, biz.ErrRoutingGroupInvalid
		}
		query = query.Where(map[string]any{key: value})
	}
	for _, order := range options.OrderBy {
		if order.Name != "id" && order.Name != "key" && order.Name != "sort_order" {
			return nil, biz.ErrRoutingGroupInvalid
		}
		query = query.Order(clause.OrderByColumn{Column: clause.Column{Name: order.Name}, Desc: order.Desc})
	}
	if err := query.Order("id ASC").Offset(options.Offset).Limit(options.Limit).Find(&rows).Error; err != nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	result := make([]*biz.RoutingGroup, 0, len(rows))
	for i := range rows {
		result = append(result, toRoutingGroup(&rows[i]))
	}
	return result, nil
}
func (r *routingGroupRepo) GetRoutingGroup(ctx context.Context, id int64) (*biz.RoutingGroupDetail, error) {
	if r.data.db == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	var result *biz.RoutingGroupDetail
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = getRoutingGroupTx(tx, id)
		return err
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		if errors.Is(err, biz.ErrRoutingGroupNotFound) {
			return nil, err
		}
		return nil, biz.ErrRoutingGroupStorage
	}
	return result, nil
}

func getRoutingGroupTx(db *gorm.DB, id int64) (*biz.RoutingGroupDetail, error) {
	var group routingGroupModel
	if err := db.First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrRoutingGroupNotFound
		}
		return nil, biz.ErrRoutingGroupStorage
	}
	result := &biz.RoutingGroupDetail{Group: toRoutingGroup(&group), Resources: []routing.GroupResource{}, ModelGrants: []routing.GroupModelGrant{}}
	var members []struct {
		ID       int64
		Priority int64
		Weight   int64
	}
	if err := db.Table("channel_routing_groups AS rg").Select("c.id, COALESCE(c.priority,0) AS priority, COALESCE(c.weight,0) AS weight").Joins("JOIN channels c ON c.id = rg.channel_id").Where("rg.routing_group_id = ?", id).Order("c.id ASC").Scan(&members).Error; err != nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	for _, m := range members {
		result.Resources = append(result.Resources, routing.GroupResource{Source: routing.Source{Kind: routing.Channel, ID: m.ID}, Priority: m.Priority, Weight: m.Weight})
	}
	members = nil
	if err := db.Table("account_routing_groups AS rg").Select("a.id, COALESCE(a.priority,0) AS priority, COALESCE(a.weight,0) AS weight").Joins("JOIN subscription_accounts a ON a.id = rg.subscription_account_id").Where("rg.routing_group_id = ?", id).Order("a.id ASC").Scan(&members).Error; err != nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	accounts := map[int64]bool{}
	for _, m := range members {
		accounts[m.ID] = true
		result.Resources = append(result.Resources, routing.GroupResource{Source: routing.Source{Kind: routing.Subscription, ID: m.ID}, Priority: m.Priority, Weight: m.Weight})
	}
	var grants []struct {
		ID                    int64
		SubscriptionAccountID int64
		ModelID               string
		UpstreamModelID       string
		Enabled               bool
		Priority              int32
	}
	if err := db.Table("model_subscription_mapping AS mm").Select("mm.id, mm.subscription_account_id, m.model_id, mm.upstream_model_id, mm.enabled, mm.priority").Joins("JOIN models m ON m.id = mm.model_id").Where("mm.routing_group_id = ?", id).Order("mm.id ASC").Scan(&grants).Error; err != nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	for _, m := range grants {
		result.ModelGrants = append(result.ModelGrants, routing.GroupModelGrant{MappingID: m.ID, AccountID: m.SubscriptionAccountID, Model: m.ModelID, UpstreamModelID: m.UpstreamModelID, Enabled: m.Enabled, Priority: m.Priority, ExtraAuthorization: !accounts[m.SubscriptionAccountID]})
	}
	return result, nil
}

func routingGroupID(tx *gorm.DB, key string) (int64, error) {
	var row routingGroupModel
	if err := tx.Where(map[string]any{"key": key}).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, biz.ErrRoutingGroupNotFound
		}
		return 0, biz.ErrRoutingGroupStorage
	}
	return row.ID, nil
}

// syncRoutingMembersTx participates in the existing resource/ability write.
// Unknown keys fail the entire transaction; CSV editors cannot invent groups.
func (r *Repository) syncRoutingMembersTx(tx *gorm.DB, source routing.Source, csv string) error {
	if !r.routingGroupDualWrite {
		return nil
	}
	ids := []int64{}
	for _, key := range routing.Groups(csv) {
		id, err := routingGroupID(tx, key)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	table, column := "channel_routing_groups", "channel_id"
	if source.Kind == routing.Subscription {
		table, column = "account_routing_groups", "subscription_account_id"
	}
	var oldIDs []int64
	if err := tx.Table(table).Where(column+" = ?", source.ID).Pluck("routing_group_id", &oldIDs).Error; err != nil {
		return err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sort.Slice(oldIDs, func(i, j int) bool { return oldIDs[i] < oldIDs[j] })
	if equalIDs(ids, oldIDs) {
		return nil
	}
	if err := tx.Table(table).Where(column+" = ?", source.ID).Delete(map[string]any{}).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := tx.Table(table).Create(map[string]any{column: source.ID, "routing_group_id": id}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&routingGroupModel{}).Where("id IN ?", append(ids, oldIDs...)).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now()}).Error
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *Repository) syncRoutingMappingTx(tx *gorm.DB, table string, where map[string]any, key string) error {
	if !r.routingGroupDualWrite {
		return nil
	}
	id, err := routingGroupID(tx, key)
	if err != nil {
		return err
	}
	if err := tx.Table(table).Where(where).Update("routing_group_id", id).Error; err != nil {
		return err
	}
	return tx.Model(&routingGroupModel{}).Where("id = ?", id).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now()}).Error
}

func (r *Repository) routingMappingTransaction(ctx context.Context, table string, where map[string]any, key string, write func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := write(tx); err != nil {
			return err
		}
		return r.syncRoutingMappingTx(tx, table, where, key)
	})
}

// Relation reads are used by the migration shadow check. The serving path
// stays on legacy reads in Phase B; switching request semantics is a later gate.
func (r *Repository) memberScope(query *gorm.DB, group, sourceColumn string, subscription bool) *gorm.DB {
	table, column := "channel_routing_groups", "channel_id"
	if subscription {
		table, column = "account_routing_groups", "subscription_account_id"
	}
	groupIDs := r.db.Model(&routingGroupModel{}).Select("id").Where(map[string]any{"key": group})
	members := r.db.Table(table).Select(column).Where("routing_group_id IN (?)", groupIDs)
	col := clause.Column{Name: sourceColumn}
	if alias, name, ok := strings.Cut(sourceColumn, "."); ok {
		col = clause.Column{Table: alias, Name: name}
	}
	return query.Where(clause.Expr{SQL: "? IN (?)", Vars: []any{col, members}})
}

func (r *Repository) csvGroupScope(query *gorm.DB, group, column, sourceColumn string) *gorm.DB {
	if r.routingGroupRelations {
		return r.memberScope(query, group, sourceColumn, false)
	}
	predicate := column + " = ? OR " + column + " LIKE ? OR " + column + " LIKE ? OR " + column + " LIKE ?"
	return query.Where(quoteGroupColumnSQL(r.db, predicate), group, group+",%", "%,"+group, "%,"+group+",%")
}

func (r *Repository) routingGroupSQL(predicate string) string {
	if r.routingGroupRelations {
		switch r.db.Dialector.Name() {
		case "mysql":
			predicate = strings.ReplaceAll(predicate, "`group` = ?", "BINARY `group` = ?")
		case "sqlite":
			predicate = strings.ReplaceAll(predicate, "`group` = ?", "`group` COLLATE BINARY = ?")
		case "postgres":
			predicate = strings.ReplaceAll(predicate, "`group` = ?", "`group` COLLATE \"C\" = ?")
		}
	}
	return quoteGroupColumnSQL(r.db, predicate)
}

func (r *Repository) mappingGroupScope(query *gorm.DB, group, alias string) *gorm.DB {
	if !r.routingGroupRelations {
		return query.Where(alias+"group_name = ?", group)
	}
	ids := r.db.Model(&routingGroupModel{}).Select("id").Where(map[string]any{"key": group})
	return query.Where(alias+"routing_group_id IN (?)", ids)
}

// Enable dual writes only after the explicit migration and successful backfill.
func routingGroupSchemaReady(db *gorm.DB) error {
	for _, table := range []string{"routing_groups", "channel_routing_groups", "account_routing_groups", "routing_group_backfills"} {
		if !db.Migrator().HasTable(table) {
			return biz.ErrRoutingGroupMigrationRequired
		}
	}
	for _, table := range []string{"model_subscription_mapping", "model_routings"} {
		if !db.Migrator().HasColumn(table, "routing_group_id") {
			return biz.ErrRoutingGroupMigrationRequired
		}
	}
	var count int64
	if db.Table("routing_group_backfills").Limit(1).Count(&count).Error != nil || count == 0 {
		return biz.ErrRoutingGroupMigrationRequired
	}
	return nil
}
