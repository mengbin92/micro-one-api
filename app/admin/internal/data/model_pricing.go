package data

import (
	"context"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
)

var _ biz.ModelPricingRepo = (*SystemOptionsRepo)(nil)

// MutateModelPrice gives the model editor a single-model write seam. It uses
// the authoritative option lock without granting whole-map system-option rights.
func (r *SystemOptionsRepo) MutateModelPrice(ctx context.Context, modelID string, fn func(string) (string, error)) error {
	if r == nil || r.gdb == nil {
		return authorization.ErrWriteStorageUnavailable
	}
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return biz.ErrModelPricingInvalid
	}
	var resourceID int64
	writeErr := authzquery.RunInTx(ctx, r.gdb, 3, func(current context.Context, tx *gorm.DB) error {
		var old systemOptionModel
		err := tx.Select("id", "option_key", "option_value", "revision").Clauses(clause.Locking{Strength: "UPDATE"}).Where("option_key = ?", "ModelPrice").Take(&old).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		facts, err := r.priceFacts(current, tx, "ModelPrice", modelID)
		if err != nil {
			return err
		}
		resourceID = facts.ResourceID
		if resourceID <= 0 {
			return authorization.ErrDenied
		}
		if _, iam := authorization.QueryScopeFromContext(current, "billing.pricing.update"); iam && strings.TrimSpace(authorization.WriteReason(current)) == "" {
			return authorization.ErrWritePrecondition
		}
		if err := authorization.Require(current, "billing.pricing.update", facts); err != nil {
			return err
		}
		prices, err := decodeOptionMap(old.OptionValue)
		if err != nil {
			return biz.ErrPricingOptionsInvalid
		}
		keys := make([]string, 0, len(prices))
		for key := range prices {
			if strings.ToLower(strings.TrimSpace(key)) == modelID {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		previous := ""
		if len(keys) > 0 {
			previous = string(prices[keys[0]])
		}
		if exact, exists := prices[modelID]; exists {
			previous = string(exact)
		}
		var fields map[string]jsonx.RawMessage
		if previous != "" && jsonx.Unmarshal([]byte(previous), &fields) != nil {
			return biz.ErrPricingOptionsInvalid
		}
		if fields == nil {
			previous = "" // A historical null is an unconfigured model.
		}
		nextPrice, err := fn(previous)
		if err != nil {
			return err
		}
		if nextPrice == previous {
			return nil
		}
		if jsonx.Unmarshal([]byte(nextPrice), &fields) != nil || fields == nil {
			return biz.ErrPricingOptionsInvalid
		}
		for _, key := range keys {
			delete(prices, key)
		}
		prices[modelID] = jsonx.RawMessage(nextPrice)
		value, err := jsonx.Marshal(prices)
		if err != nil {
			return err
		}
		next := map[string]any{"option_key": "ModelPrice", "option_value": string(value), "updated_at": r.optionUpdatedAt(), "revision": old.Revision + 1}
		if old.ID == 0 {
			if err := tx.Table("system_options").Create(&next).Error; err != nil {
				return err
			}
		} else {
			result := tx.Table("system_options").Where("id = ? AND revision = ?", old.ID, old.Revision).Updates(next)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return biz.ErrSystemOptionConflict
			}
		}
		return authzquery.AppendWriteAudit(current, tx, "billing.pricing.update", resourceID)
	})
	return authzquery.RecordWriteFailure(ctx, r.gdb, "billing.pricing.update", resourceID, mapSystemOptionWriteError(writeErr))
}
