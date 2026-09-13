package data

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/routingoutbox"

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

// relationOverride is the raw nullable per-group override on a resource
// relation; nil fields inherit the resource's own values.
type relationOverride struct {
	Priority *int64
	Weight   *int64
}

// relationOverrides loads per-group priority/weight overrides for every
// member of the group key. It returns an empty map on legacy (CSV) schemas or
// when the 098 columns are absent, so pre-F deployments are untouched.
func (r *Repository) relationOverrides(ctx context.Context, group string, subscription bool) (map[int64]relationOverride, error) {
	empty := map[int64]relationOverride{}
	if r.db == nil || !r.routingGroupRelations {
		return empty, nil
	}
	if !r.relationOverrideColsReady() {
		return empty, nil
	}
	table, column := "channel_routing_groups", "channel_id"
	if subscription {
		table, column = "account_routing_groups", "subscription_account_id"
	}
	groupIDs := r.db.Model(&routingGroupModel{}).Select("id").Where(map[string]any{"key": group})
	var rows []struct {
		ID       int64
		Priority *int64
		Weight   *int64
	}
	if err := r.db.WithContext(ctx).Table(table).Select(column+" AS id, priority_override AS priority, weight_override AS weight").Where("routing_group_id IN (?)", groupIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]relationOverride, len(rows))
	for _, row := range rows {
		out[row.ID] = relationOverride{Priority: row.Priority, Weight: row.Weight}
	}
	return out, nil
}

func (r *Repository) relationOverrideColsReady() bool {
	if r.db == nil {
		return false
	}
	r.overrideColsReadyOnce.Do(func() {
		r.overrideColsReady = r.db.Migrator().HasColumn("channel_routing_groups", "priority_override") &&
			r.db.Migrator().HasColumn("account_routing_groups", "weight_override")
	})
	return r.overrideColsReady
}

// applyRelationOverride folds a relation-level override into the effective
// ability values: override priority wins outright; weight override > 0 is
// carried on the ability (0 = inherit, preserving pre-F distribution).
func applyRelationOverride(overrides map[int64]relationOverride, id, priority, weight int64) (int64, int64) {
	if ov, ok := overrides[id]; ok {
		if ov.Priority != nil {
			priority = *ov.Priority
		}
		if ov.Weight != nil && *ov.Weight > 0 {
			weight = *ov.Weight
		}
	}
	return priority, weight
}

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

// CreateRoutingGroup inserts an explicitly created group and enqueues the
// revision-1 change event. Both happen in one transaction so a crash cannot
// leave an undelivered group revision. Callers (biz) validate the key; the
// unique index is the authority, and a concurrent duplicate is mapped to
// ErrRoutingGroupExists rather than surfacing as a storage failure.
func (r *routingGroupRepo) CreateRoutingGroup(ctx context.Context, group *biz.RoutingGroup) (*biz.RoutingGroup, error) {
	if r.data.db == nil || group == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	if err := routingGroupSchemaReady(r.data.db); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	row := routingGroupModel{
		Key:             group.Key,
		DisplayName:     group.DisplayName,
		Description:     group.Description,
		Status:          group.Status,
		AccessMode:      group.AccessMode,
		ModelAccessMode: group.ModelAccessMode,
		SortOrder:       group.SortOrder,
		Revision:        1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&routingGroupModel{}).Where(map[string]any{"key": group.Key}).Count(&existing).Error; err != nil {
			return biz.ErrRoutingGroupStorage
		}
		if existing > 0 {
			return biz.ErrRoutingGroupExists
		}
		if err := tx.Create(&row).Error; err != nil {
			if isDuplicateKeyErr(err) {
				return biz.ErrRoutingGroupExists
			}
			return biz.ErrRoutingGroupStorage
		}
		return routingoutbox.Enqueue(tx, "channel", "group", row.ID, row.Revision)
	})
	if err != nil {
		return nil, err
	}
	return toRoutingGroup(&row), nil
}

func (r *routingGroupRepo) GetRoutingGroup(ctx context.Context, id int64) (*biz.RoutingGroupDetail, error) {
	if r.data.db == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	var result *biz.RoutingGroupDetail
	// Schema probes use the pool, so finish them before the transaction owns
	// SQLite's only connection (and before concurrent readers hold the pool).
	overridesReady := r.data.relationOverrideColsReady()
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = getRoutingGroupTx(tx, id, overridesReady)
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

func getRoutingGroupTx(db *gorm.DB, id int64, overridesReady bool) (*biz.RoutingGroupDetail, error) {
	var group routingGroupModel
	if err := db.First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrRoutingGroupNotFound
		}
		return nil, biz.ErrRoutingGroupStorage
	}
	result := &biz.RoutingGroupDetail{Group: toRoutingGroup(&group), Resources: []routing.GroupResource{}, ModelGrants: []routing.GroupModelGrant{}}
	var members []struct {
		ID               int64
		Priority         int64
		Weight           int64
		PriorityOverride *int64
		WeightOverride   *int64
	}
	channelSelect := "c.id, COALESCE(c.priority,0) AS priority, COALESCE(c.weight,0) AS weight"
	if overridesReady {
		channelSelect += ", rg.priority_override, rg.weight_override"
	}
	if err := db.Table("channel_routing_groups AS rg").Select(channelSelect).Joins("JOIN channels c ON c.id = rg.channel_id").Where("rg.routing_group_id = ?", id).Order("c.id ASC").Scan(&members).Error; err != nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	for _, m := range members {
		priority, weight := m.Priority, m.Weight
		if m.PriorityOverride != nil {
			priority = *m.PriorityOverride
		}
		// 0 = inherit, matching applyRelationOverride on the serving path; a
		// stored 0 override must not present a phantom effective weight here.
		if m.WeightOverride != nil && *m.WeightOverride > 0 {
			weight = *m.WeightOverride
		}
		result.Resources = append(result.Resources, routing.GroupResource{Source: routing.Source{Kind: routing.Channel, ID: m.ID}, Priority: priority, Weight: weight, PriorityOverride: m.PriorityOverride, WeightOverride: m.WeightOverride})
	}
	members = nil
	accountSelect := "a.id, COALESCE(a.priority,0) AS priority, COALESCE(a.weight,0) AS weight"
	if overridesReady {
		accountSelect += ", rg.priority_override, rg.weight_override"
	}
	if err := db.Table("account_routing_groups AS rg").Select(accountSelect).Joins("JOIN subscription_accounts a ON a.id = rg.subscription_account_id").Where("rg.routing_group_id = ?", id).Order("a.id ASC").Scan(&members).Error; err != nil {
		return nil, biz.ErrRoutingGroupStorage
	}
	accounts := map[int64]bool{}
	for _, m := range members {
		accounts[m.ID] = true
		priority, weight := m.Priority, m.Weight
		if m.PriorityOverride != nil {
			priority = *m.PriorityOverride
		}
		// 0 = inherit, matching applyRelationOverride on the serving path.
		if m.WeightOverride != nil && *m.WeightOverride > 0 {
			weight = *m.WeightOverride
		}
		result.Resources = append(result.Resources, routing.GroupResource{Source: routing.Source{Kind: routing.Subscription, ID: m.ID}, Priority: priority, Weight: weight, PriorityOverride: m.PriorityOverride, WeightOverride: m.WeightOverride})
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
	// Incremental sync: only insert genuinely new memberships and delete removed
	// ones, so relation-level priority/weight overrides (098) on retained rows
	// survive a resource CSV edit.
	oldSet := make(map[int64]bool, len(oldIDs))
	for _, id := range oldIDs {
		oldSet[id] = true
	}
	idSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
		if !oldSet[id] {
			if err := tx.Table(table).Create(map[string]any{column: source.ID, "routing_group_id": id}).Error; err != nil {
				return err
			}
		}
	}
	var removed []int64
	for _, id := range oldIDs {
		if !idSet[id] {
			removed = append(removed, id)
		}
	}
	if len(removed) > 0 {
		if err := tx.Table(table).Where(column+" = ? AND routing_group_id IN ?", source.ID, removed).Delete(map[string]any{}).Error; err != nil {
			return err
		}
	}
	return bumpRoutingGroupsTx(tx, append(ids, oldIDs...))
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
	var oldIDs []int64
	if err := tx.Table(table).Where(where).Where("routing_group_id IS NOT NULL").Pluck("routing_group_id", &oldIDs).Error; err != nil {
		return err
	}
	if err := tx.Table(table).Where(where).Update("routing_group_id", id).Error; err != nil {
		return err
	}
	return bumpRoutingGroupsTx(tx, append(oldIDs, id))
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

// routingGroupSchemaReady reports whether the routing-group tables and group
// reference columns the dual-write and creation paths depend on all exist.
func routingGroupSchemaReady(db *gorm.DB) error {
	if !db.Migrator().HasTable("routing_change_outbox") {
		return biz.ErrRoutingGroupMigrationRequired
	}
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
	return nil
}

// routingGroupBackfillRecorded reports whether an explicit legacy backfill was
// committed. Enabling dual writes requires it, because the relation projection
// would otherwise drift from the legacy CSVs. Creating a brand-new group does
// not: a fresh deployment may create its first group without ever running the
// legacy migration, so creation only needs the schema to exist.
func routingGroupBackfillRecorded(db *gorm.DB) bool {
	var count int64
	if db.Table("routing_group_backfills").Limit(1).Count(&count).Error != nil {
		return false
	}
	return count > 0
}

func (r *routingGroupRepo) SetRoutingGroupState(ctx context.Context, id, revision int64, status, access string) error {
	if r.data.db == nil {
		return biz.ErrRoutingGroupStorage
	}
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&routingGroupModel{}).Where("id = ? AND revision = ? AND status <> ?", id, revision, "archived").Updates(map[string]any{"status": status, "access_mode": access, "revision": revision + 1, "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return biz.ErrRoutingGroupStorage
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingGroupBaselineConflict
		}
		return routingoutbox.Enqueue(tx, "channel", "group", id, revision+1)
	})
}

func bumpRoutingGroupsTx(tx *gorm.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Model(&routingGroupModel{}).Where("id IN ?", ids).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now()}).Error; err != nil {
		return err
	}
	var groups []routingGroupModel
	if err := tx.Select("id", "revision").Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return err
	}
	for _, group := range groups {
		if err := routingoutbox.Enqueue(tx, "channel", "group", group.ID, group.Revision); err != nil {
			return err
		}
	}
	return nil
}

// SetRoutingGroupResourceOverrides upserts the nullable priority/weight
// overrides on one resource relation row. NULL clears the override (inherit).
// The group revision bump and routing-change outbox event commit in the same
// transaction, matching the membership write contract.
func (r *routingGroupRepo) SetRoutingGroupResourceOverrides(ctx context.Context, groupID int64, source routing.Source, priority, weight *int64) error {
	if r.data.db == nil {
		return biz.ErrRoutingGroupStorage
	}
	table, column := "channel_routing_groups", "channel_id"
	if source.Kind == routing.Subscription {
		table, column = "account_routing_groups", "subscription_account_id"
	}
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"priority_override": priority, "weight_override": weight}
		where := column + " = ? AND routing_group_id = ?"
		args := []any{source.ID, groupID}
		result := tx.Table(table).Where(where, args...).Updates(updates)
		if result.Error != nil {
			return biz.ErrRoutingGroupStorage
		}
		if result.RowsAffected != 1 {
			// MySQL counts rows *changed*: an identical re-PUT reports 0 even
			// though the membership exists. Confirm membership before failing.
			var memberships int64
			if err := tx.Table(table).Where(where, args...).Count(&memberships).Error; err != nil {
				return biz.ErrRoutingGroupStorage
			}
			if memberships == 0 {
				return biz.ErrRoutingGroupNotFound
			}
		}
		return bumpRoutingGroupsTx(tx, []int64{groupID})
	})
}
