package data

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// groupAuditRepo uses the production ability and registry queries in one
// caller-owned read-only transaction. Source lookups project routing metadata
// only: the audit never loads keys, tokens, names, or credential metadata.
type groupAuditRepo struct {
	*Repository
	usersTable    string
	optionsTable  string
	billingSchema string
}

// GroupAuditSchemas locates non-channel tables on a split-schema deployment.
// All reads still use the same caller-owned read-only transaction.
type GroupAuditSchemas struct {
	Identity string
	Options  string
	Billing  string
}

var auditSchemaIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func NewGroupAuditRepositories(tx *sql.Tx, driver string, schemas GroupAuditSchemas) (biz.ChannelRepo, biz.ModelRoutingRepo, biz.GroupInventoryRepo, error) {
	for _, schema := range []string{schemas.Identity, schemas.Options, schemas.Billing} {
		if schema != "" && ((driver != "mysql" && driver != "postgres") || !auditSchemaIdentifier.MatchString(schema)) {
			return nil, nil, nil, fmt.Errorf("audit schema overrides require MySQL/PostgreSQL identifiers")
		}
	}
	var dialector gorm.Dialector
	switch driver {
	case "mysql":
		dialector = mysql.New(mysql.Config{Conn: tx, SkipInitializeWithVersion: true})
	case "sqlite3":
		dialector = sqlite.New(sqlite.Config{Conn: tx})
	case "postgres":
		dialector = postgres.New(postgres.Config{Conn: tx})
	default:
		return nil, nil, nil, fmt.Errorf("audit supports mysql, postgres and sqlite3 snapshots")
	}
	db, err := gorm.Open(dialector, &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, nil, nil, err
	}
	r := &groupAuditRepo{Repository: &Repository{db: db}, usersTable: "users", optionsTable: "system_options", billingSchema: schemas.Billing}
	if schemas.Identity != "" {
		r.usersTable = schemas.Identity + ".users"
	}
	if schemas.Options != "" {
		r.optionsTable = schemas.Options + ".system_options"
	}
	return r, r, r, nil
}

func (r *groupAuditRepo) FindByID(ctx context.Context, id int64) (*biz.Channel, error) {
	var row channelModel
	result := r.db.WithContext(ctx).Select("id", "status").Where("id = ?", id).Find(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, biz.ErrChannelNotFound
	}
	return &biz.Channel{ID: row.ID, Status: row.Status}, nil
}

func (r *groupAuditRepo) FindSubscriptionAccountByID(ctx context.Context, id int64) (*biz.SubscriptionAccount, error) {
	var row subscriptionAccountModel
	result := r.db.WithContext(ctx).Select("id", "status", "platform").Where("id = ?", id).Find(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, biz.ErrSubscriptionAccountNotFound
	}
	return &biz.SubscriptionAccount{ID: row.ID, Status: row.Status, Platform: row.Platform}, nil
}

func (r *groupAuditRepo) ListUnrestrictedChannelsByGroup(ctx context.Context, group string) ([]*biz.Channel, error) {
	// Share the exact query predicate with the serving repository, but project
	// only ID and status so credentials never enter the audit process.
	rows, err := r.Repository.listUnrestrictedChannelsByGroupDB(ctx, group, "id", "status")
	return rows, err
}

func (r *groupAuditRepo) LoadGroupInventory(ctx context.Context) (*biz.GroupInventory, error) {
	db := r.db.WithContext(ctx)
	inventory := &biz.GroupInventory{}
	add := func(table string, id int64, group string, csv bool, source routing.Source, model string) {
		inventory.References = append(inventory.References, biz.GroupReference{Table: table, ID: id, Group: group, CSV: csv, Source: source, Model: model})
		if model != "" {
			inventory.Models = append(inventory.Models, model)
		}
	}
	// Missing tables/columns fail the audit; a partial snapshot is not a baseline.
	var users []struct {
		ID    int64
		Group string
	}
	if err := db.Table(r.usersTable).Select("id", "group").Find(&users).Error; err != nil {
		return nil, err
	}
	for _, row := range users {
		add("users", row.ID, row.Group, false, routing.Source{}, "")
	}
	tokensTable := strings.TrimSuffix(r.usersTable, "users") + "tokens"
	if err := db.Table(tokensTable).Select("id", "user_id").Find(&inventory.Tokens).Error; err != nil {
		return nil, err
	}
	var channels []channelModel
	if err := db.Select("id", "group", "models").Find(&channels).Error; err != nil {
		return nil, err
	}
	channelGroups := map[int64]string{}
	for _, row := range channels {
		source := routing.Source{Kind: routing.Channel, ID: row.ID}
		inventory.Sources = append(inventory.Sources, source)
		add("channels", row.ID, row.Group, true, source, "")
		channelGroups[row.ID] = row.Group
		inventory.Models = append(inventory.Models, strings.Split(row.Models, ",")...)
	}
	var accounts []subscriptionAccountModel
	if err := db.Select("id", "group", "models").Find(&accounts).Error; err != nil {
		return nil, err
	}
	for _, row := range accounts {
		source := routing.Source{Kind: routing.Subscription, ID: row.ID}
		inventory.Sources = append(inventory.Sources, source)
		add("subscription_accounts", row.ID, row.Group, true, source, "")
		inventory.Models = append(inventory.Models, strings.Split(row.Models, ",")...)
	}
	var abilities []abilityModel
	if err := db.Find(&abilities).Error; err != nil {
		return nil, err
	}
	for _, row := range abilities {
		add("abilities", row.ChannelID, row.Group, false, routing.Source{Kind: routing.Channel, ID: row.ChannelID}, row.Model)
	}
	var subAbilities []subscriptionAccountAbilityModel
	if err := db.Find(&subAbilities).Error; err != nil {
		return nil, err
	}
	for _, row := range subAbilities {
		add("subscription_account_abilities", row.ID, row.Group, false, routing.Source{Kind: routing.Subscription, ID: row.AccountID}, row.Model)
	}
	var models []modelModel
	if err := db.Select("id", "model_id").Find(&models).Error; err != nil {
		return nil, err
	}
	modelNames := map[int64]string{}
	for _, row := range models {
		modelNames[row.ID] = row.ModelID
		inventory.Models = append(inventory.Models, row.ModelID)
	}
	var mappings []modelSubscriptionMappingModel
	if err := db.Select("id", "model_id", "subscription_account_id", "group_name").Find(&mappings).Error; err != nil {
		return nil, err
	}
	for _, row := range mappings {
		add("model_subscription_mapping", row.ID, row.GroupName, false, routing.Source{Kind: routing.Subscription, ID: row.SubscriptionAccountID}, modelNames[row.ModelPK])
		inventory.References[len(inventory.References)-1].ModelPK = row.ModelPK
	}
	var channelMappings []modelChannelMappingModel
	if err := db.Select("id", "model_id", "channel_id").Find(&channelMappings).Error; err != nil {
		return nil, err
	}
	for _, row := range channelMappings {
		groups := routing.Groups(channelGroups[row.ChannelID])
		if len(groups) == 0 {
			groups = []string{""}
		}
		for _, group := range groups {
			add("model_channel_mapping", row.ID, group, false, routing.Source{Kind: routing.Channel, ID: row.ChannelID}, modelNames[row.ModelPK])
			inventory.References[len(inventory.References)-1].ModelPK = row.ModelPK
		}
	}
	var routings []modelRoutingModel
	if err := db.Select("id", "group_name", "model", "subscription_account_id").Find(&routings).Error; err != nil {
		return nil, err
	}
	for _, row := range routings {
		add("model_routings", row.ID, row.GroupName, false, routing.Source{Kind: routing.Subscription, ID: row.SubscriptionAccountID}, row.Model)
	}
	var options []struct{ OptionValue string }
	if err := db.Table(r.optionsTable).Select("option_value").Where("option_key = ?", "GroupRatio").Find(&options).Error; err != nil {
		return nil, err
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("multiple GroupRatio options")
	}
	if len(options) == 1 {
		inventory.GroupRatioJSON = options[0].OptionValue
	}
	if err := r.loadSubscriptionInventory(ctx, inventory); err != nil {
		return nil, err
	}
	return inventory, nil
}

func (r *groupAuditRepo) loadSubscriptionInventory(ctx context.Context, inventory *biz.GroupInventory) error {
	table := func(name string) string {
		if r.billingSchema != "" {
			return r.billingSchema + "." + name
		}
		return name
	}
	db := r.db.WithContext(ctx)
	if err := db.Table(table("subscription_groups")).Select("id", "status", "daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd", "rate_multiplier").Find(&inventory.QuotaPolicies).Error; err != nil {
		return err
	}
	if err := db.Table(table("subscription_plans")).Select("id, group_id AS quota_policy_id, for_sale").Find(&inventory.Plans).Error; err != nil {
		return err
	}
	if err := db.Table(table("user_subscriptions")).Select("id, user_id, group_id AS quota_policy_id, status, starts_at, expires_at").Find(&inventory.Subscriptions).Error; err != nil {
		return err
	}
	// Read purchase-contract metadata only. Provider payloads, transaction IDs,
	// payment URLs, subscription metadata and personal names are never selected.
	var orders []struct {
		ID               int64
		UserID           string
		GroupID          int64
		PlanID           int64
		SubscriptionID   int64
		Status           string
		AssetIssueStatus string
		PlanSnapshot     string
	}
	if err := db.Table(table("payment_orders")).Select("id", "user_id", "group_id", "plan_id", "subscription_id", "status", "asset_issue_status", "plan_snapshot").Where("asset_type = ? OR group_id <> 0 OR plan_id <> 0 OR subscription_id <> 0", "subscription").Find(&orders).Error; err != nil {
		return err
	}
	for _, row := range orders {
		order := biz.GroupAuditOrder{ID: row.ID, UserID: row.UserID, QuotaPolicyID: row.GroupID, PlanID: row.PlanID, SubscriptionID: row.SubscriptionID, Status: row.Status, AssetIssueStatus: row.AssetIssueStatus, SnapshotState: "absent"}
		if strings.TrimSpace(row.PlanSnapshot) != "" {
			// Decode only numeric references, never re-emit the raw JSON or plan
			// display strings (including on parse errors).
			var snapshot struct {
				PlanID       int64 `json:"plan_id"`
				GroupID      int64 `json:"group_id"`
				PriceQuota   int64 `json:"price_quota"`
				ValidityDays int32 `json:"validity_days"`
			}
			if err := jsonx.Unmarshal([]byte(row.PlanSnapshot), &snapshot); err != nil || snapshot.PlanID <= 0 || snapshot.GroupID <= 0 || snapshot.PriceQuota < 0 || snapshot.ValidityDays <= 0 {
				order.SnapshotState = "invalid"
			} else {
				order.SnapshotState = "present"
				order.SnapshotPlanID = snapshot.PlanID
				order.SnapshotQuotaPolicyID = snapshot.GroupID
			}
		}
		inventory.Orders = append(inventory.Orders, order)
	}
	return nil
}
