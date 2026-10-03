package data

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type systemOptionModel struct {
	ID          int64
	OptionKey   string `gorm:"uniqueIndex"`
	OptionValue string
	Revision    int64
}

func (systemOptionModel) TableName() string { return "system_options" }
func (r *SystemOptionsRepo) Mutate(ctx context.Context, key string, fn func(string) (string, error)) error {
	if r.gdb == nil {
		return errors.New("system options transaction unavailable")
	}
	writeErr := authzquery.RunInTx(ctx, r.gdb, 3, func(ctx context.Context, tx *gorm.DB) error { return r.mutateOptionInTx(ctx, tx, key, fn) })
	return recordOptionWriteFailure(ctx, r.gdb, []string{key}, writeErr)
}
func (r *SystemOptionsRepo) mutateOptionInTx(ctx context.Context, tx *gorm.DB, key string, fn func(string) (string, error)) error {
	var old systemOptionModel
	err := tx.Select("id", "option_key", "option_value", "revision").Clauses(clause.Locking{Strength: "UPDATE"}).Where("option_key = ?", key).Take(&old).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, biz.SystemOptionWriteOperation(key)); iam {
		expected, supplied := biz.ExpectedOptionRevision(ctx)
		if !supplied || expected != old.Revision {
			return biz.ErrSystemOptionConflict
		}
	}
	value, err := fn(old.OptionValue)
	if err != nil {
		return err
	}
	if err = r.requireOptionWrite(ctx, tx, key, old.OptionValue, value); err != nil {
		return err
	}
	next := map[string]any{"option_key": key, "option_value": value, "updated_at": r.optionUpdatedAt(), "revision": old.Revision + 1}
	if err = tx.Table("system_options").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "option_key"}}, DoUpdates: clause.AssignmentColumns([]string{"option_value", "updated_at", "revision"})}).Create(&next).Error; err != nil {
		return err
	}
	if old.ID == 0 {
		if err = tx.Select("id").Where("option_key = ?", key).Take(&old).Error; err != nil {
			return err
		}
	}
	for _, op := range optionWriteOperations(key) {
		if err = authzquery.AppendWriteAudit(ctx, tx, op, old.ID); err != nil {
			return err
		}
	}
	return r.auditPriceChanges(ctx, tx, key, old.OptionValue, value)
}
func (r *SystemOptionsRepo) SetMany(ctx context.Context, values map[string]string, revisions map[string]int64) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	writeErr := authzquery.RunInTx(ctx, r.gdb, 3, func(ctx context.Context, tx *gorm.DB) error {
		for _, key := range keys {
			rowCtx := ctx
			if revision, ok := revisions[key]; ok {
				rowCtx = biz.WithExpectedOptionRevision(ctx, revision)
			}
			value := values[key]
			if err := r.mutateOptionInTx(rowCtx, tx, key, func(string) (string, error) { return value, nil }); err != nil {
				return err
			}
		}
		return nil
	})
	return recordOptionWriteFailure(ctx, r.gdb, keys, writeErr)
}

func (r *SystemOptionsRepo) requireOptionWrite(ctx context.Context, tx *gorm.DB, key, old, value string) error {
	facts := authorization.ObjectFacts{Context: authorization.Platform()}
	for _, op := range optionWriteOperations(key) {
		if _, iam := authorization.QueryScopeFromContext(ctx, op); iam && strings.TrimSpace(authorization.WriteReason(ctx)) == "" {
			return authorization.ErrDenied
		}
		if err := authorization.Require(ctx, op, facts); err != nil {
			return err
		}
	}
	if !biz.IsPricingOption(key) {
		return nil
	}
	if !biz.IsPricingMapOption(key) {
		return authorization.Require(ctx, "billing.pricing.update", facts)
	}
	return r.eachPriceChange(ctx, tx, key, old, value, false)
}
func decodeOptionMap(raw string) (map[string]jsonx.RawMessage, error) {
	m := map[string]jsonx.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		if err := jsonx.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
	}
	if m == nil {
		m = map[string]jsonx.RawMessage{}
	}
	return m, nil
}
func sameOptionValue(a, b jsonx.RawMessage) bool {
	var av, bv any
	if jsonx.Unmarshal(a, &av) != nil || jsonx.Unmarshal(b, &bv) != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}
func (r *SystemOptionsRepo) eachPriceChange(ctx context.Context, tx *gorm.DB, key, old, value string, audit bool) error {
	if _, iam := authorization.QueryScopeFromContext(ctx, "system.option.update"); !iam {
		return nil
	}
	before, err := decodeOptionMap(old)
	if err != nil {
		return err
	}
	after, err := decodeOptionMap(value)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	for k := range keys {
		a, existsA := before[k]
		b, existsB := after[k]
		if existsA && existsB && sameOptionValue(a, b) {
			continue
		}
		op := "billing.pricing.update"
		if key == "GroupRatio" {
			op = "billing.routing_policy.publish"
		}
		if key == "UpstreamModelPrice" {
			action := "update"
			if !existsA {
				action = "create"
			} else if !existsB {
				action = "delete"
			}
			op = "billing.upstream_cost." + action
		}
		facts, err := r.priceFacts(ctx, tx, key, k)
		if err != nil {
			return err
		}
		checkedFacts := facts
		if key == "UpstreamModelPrice" && !existsA {
			checkedFacts.ResourceID = 0
		}
		if err = authorization.Require(ctx, op, checkedFacts); err != nil {
			return err
		}
		if key == "UpstreamModelPrice" && biz.IsUpstreamCostMigration(ctx) {
			migrationFacts := facts
			if !existsA {
				source, bound := biz.UpstreamCostMigrationSource(ctx, k)
				if !bound {
					return authorization.ErrDenied
				}
				sourceValue, exists := before[source]
				_, retained := after[source]
				if !exists || retained || !sameOptionValue(sourceValue, b) {
					return authorization.ErrDenied
				}
				migrationFacts, err = r.priceFacts(ctx, tx, key, source)
				if err != nil {
					return err
				}
			}
			if err = authorization.Require(ctx, "billing.upstream_cost.migrate", migrationFacts); err != nil {
				return err
			}
			if audit {
				if err = authzquery.AppendWriteAudit(ctx, tx, "billing.upstream_cost.migrate", migrationFacts.ResourceID); err != nil {
					return err
				}
			}
		}
		if audit {
			if err = authzquery.AppendWriteAudit(ctx, tx, op, facts.ResourceID); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *SystemOptionsRepo) auditPriceChanges(ctx context.Context, tx *gorm.DB, key, old, value string) error {
	if !biz.IsPricingOption(key) {
		return nil
	}
	if !biz.IsPricingMapOption(key) {
		return authzquery.AppendWriteAudit(ctx, tx, "billing.pricing.update", 0)
	}
	return r.eachPriceChange(ctx, tx, key, old, value, true)
}
func (r *SystemOptionsRepo) priceFacts(ctx context.Context, tx *gorm.DB, key, name string) (authorization.ObjectFacts, error) {
	facts := authorization.ObjectFacts{Context: authorization.Platform()}
	if key == "GroupRatio" {
		var row struct{ ID int64 }
		err := tx.Table("routing_groups").Select("id").Where(map[string]any{"key": name}).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return facts, nil
		}
		if err != nil {
			return facts, err
		}
		facts.ResourceID = row.ID
		facts.RoutingGroupIDs = []int64{row.ID}
		return facts, nil
	}
	if key == "UpstreamModelPrice" {
		if len(name) > 512 || strings.TrimSpace(name) == "" {
			return facts, authorization.ErrDenied
		}
		parts := strings.SplitN(name, ":", 3)
		if len(parts) == 3 && (parts[0] == "channel" || parts[0] == "subscription") {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || id <= 0 {
				return facts, authorization.ErrDenied
			}
			table := "channels"
			if parts[0] == "subscription" {
				table = "subscription_accounts"
			}
			var n int64
			if err = tx.Table(table).Where("id = ?", id).Count(&n).Error; err != nil {
				return facts, err
			}
			if n != 1 {
				return facts, authorization.ErrDenied
			}
		}
		id, err := ensureCostResource(tx, name)
		facts.ResourceID = id
		return facts, err
	}
	// Price scopes use the canonical model primary key, matching channel owner.
	var row struct{ ID int64 }
	err := tx.Table("models").Select("id").Where("model_id = ?", strings.ToLower(strings.TrimSpace(name))).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return facts, nil
	}
	if err != nil {
		return facts, err
	}
	facts.ResourceID = row.ID
	return facts, nil
}
func (r *SystemOptionsRepo) priceView(ctx context.Context, key, raw string) (string, error) {
	op := "billing.pricing.read"
	if key == "GroupRatio" {
		op = "billing.routing_policy.read"
	}
	if key == "UpstreamModelPrice" {
		op = "billing.upstream_cost.read"
	}
	if _, ok := authorization.QueryScopeFromContext(ctx, op); !ok {
		return raw, nil
	}
	if !biz.IsPricingMapOption(key) {
		if authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()}) != nil {
			return "", nil
		}
		return raw, nil
	}
	values, err := decodeOptionMap(raw)
	if err != nil {
		return "", err
	}
	out := map[string]jsonx.RawMessage{}
	for name, value := range values {
		var facts authorization.ObjectFacts
		err := r.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var err error; facts, err = r.priceFacts(ctx, tx, key, name); return err })
		if err != nil {
			return "", err
		}
		if authorization.Require(ctx, op, facts) == nil {
			out[name] = value
		}
	}
	encoded, err := jsonx.Marshal(out)
	return string(encoded), err
}

func (r *SystemOptionsRepo) GetRevision(ctx context.Context, key string) (int64, error) {
	var revision int64
	err := r.gdb.WithContext(ctx).Table("system_options").Select("revision").Where("option_key = ?", key).Scan(&revision).Error
	return revision, err
}
func (r *SystemOptionsRepo) optionUpdatedAt() any {
	if r.pgBind {
		return time.Now().Unix()
	}
	return time.Now()
}

type upstreamCostResourceModel struct {
	ID      int64
	CostKey string `gorm:"uniqueIndex"`
}

func (upstreamCostResourceModel) TableName() string { return "upstream_cost_resources" }
func ensureCostResource(tx *gorm.DB, key string) (int64, error) {
	row := upstreamCostResourceModel{CostKey: key}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "cost_key"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return 0, err
	}
	if err := tx.Where("cost_key = ?", key).Take(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}
func (r *SystemOptionsRepo) CostResourceID(ctx context.Context, key string) (int64, error) {
	var id int64
	err := r.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var raw string
		if err := tx.Table("system_options").Select("option_value").Where("option_key = ?", "UpstreamModelPrice").Scan(&raw).Error; err != nil {
			return err
		}
		values, err := decodeOptionMap(raw)
		if err != nil {
			return err
		}
		if _, exists := values[key]; !exists {
			return authorization.ErrDenied
		}
		facts, err := r.priceFacts(ctx, tx, "UpstreamModelPrice", key)
		if err != nil {
			return err
		}
		if err = authorization.Require(ctx, "billing.upstream_cost.read", facts); err != nil {
			return err
		}
		id = facts.ResourceID
		return nil
	})
	return id, err
}

func optionWriteOperations(key string) []string {
	op := biz.SystemOptionWriteOperation(key)
	if op == "system.option.update" {
		return []string{op}
	}
	return []string{"system.option.update", op}
}

func recordOptionWriteFailure(ctx context.Context, db *gorm.DB, keys []string, writeErr error) error {
	if writeErr == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, key := range keys {
		for _, operation := range optionWriteOperations(key) {
			if seen[operation] {
				continue
			}
			seen[operation] = true
			writeErr = authzquery.RecordWriteFailure(ctx, db, operation, 0, writeErr)
		}
	}
	return writeErr
}
