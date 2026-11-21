package data

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm/clause"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/safecast"

	"gorm.io/gorm"
)

type redeemRepo struct {
	data *Data
}

func NewRedeemRepo(data *Data) biz.RedeemRepo {
	return &redeemRepo{data: data}
}

func (r *redeemRepo) CreateRedeemCode(ctx context.Context, code *biz.RedeemCode) error {
	model, err := redeemCodeModelFromBiz(code, time.Now())
	if err != nil {
		return err
	}

	writeErr := authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := biz.RequireWrite(ctx, "billing.redemption.create", redeemFacts(0)); err != nil {
			return err
		}
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		id, err := safecast.UintToInt64(model.ID)
		if err != nil {
			return err
		}
		return authzquery.AppendWriteAudit(ctx, tx, "billing.redemption.create", id)
	})
	return authzquery.RecordWriteFailure(ctx, r.data.db, "billing.redemption.create", 0, writeErr)
}

func (r *redeemRepo) CreateRedeemCodesBatch(ctx context.Context, codes []*biz.RedeemCode) error {
	if len(codes) == 0 {
		return nil
	}

	models := make([]redeemCodeModel, len(codes))
	now := time.Now()
	for i, code := range codes {
		model, err := redeemCodeModelFromBiz(code, now)
		if err != nil {
			return err
		}
		models[i] = *model
	}

	writeErr := authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := biz.RequireWrite(ctx, "billing.redemption.batch_create", redeemFacts(0)); err != nil {
			return err
		}
		if err := tx.Create(&models).Error; err != nil {
			return err
		}
		for _, row := range models {
			id, err := safecast.UintToInt64(row.ID)
			if err != nil {
				return err
			}
			if err := authzquery.AppendWriteAudit(ctx, tx, "billing.redemption.batch_create", id); err != nil {
				return err
			}
		}
		return nil
	})
	return authzquery.RecordWriteFailure(ctx, r.data.db, "billing.redemption.batch_create", 0, writeErr)
}

func (r *redeemRepo) GetRedeemCode(ctx context.Context, code string) (*biz.RedeemCode, error) {
	return getRedeemCode(ctx, r.data.db, code)
}

func (r *redeemRepo) GetRedeemCodeForUpdate(ctx context.Context, tx subscriptionbiz.Tx, code string) (*biz.RedeemCode, error) {
	return getRedeemCode(ctx, txDB(tx).Clauses(clause.Locking{Strength: "UPDATE"}), code)
}

func getRedeemCode(ctx context.Context, db *gorm.DB, code string) (*biz.RedeemCode, error) {
	var model redeemCodeModel
	if err := db.WithContext(ctx).Where("code = ?", code).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrRedeemCodeNotFound
		}
		return nil, err
	}

	id, err := safecast.UintToInt64(model.ID)
	if err != nil {
		return nil, err
	}
	if err := authorization.Require(ctx, "billing.redemption.read", redeemFacts(id)); err != nil {
		return nil, err
	}
	return redeemCodeToBiz(&model)
}

const redeemStatusSQL = "(CASE WHEN status = 1 AND count <= 0 THEN 2 ELSE status END)"

func (r *redeemRepo) ListRedeemCodes(ctx context.Context, options biz.RedeemCodeListOptions) ([]*biz.RedeemCode, int64, error) {
	var models []redeemCodeModel
	var total int64

	offset := max(int64(options.Page)-1, 0) * int64(options.PageSize)

	query, err := authzquery.ApplyContext(ctx, r.data.db.WithContext(ctx).Model(&redeemCodeModel{}), authzquery.Columns{Resource: "id"}, "billing.redemption.list", "billing.redemption.export")
	if err != nil {
		return nil, 0, err
	}
	if options.Keyword != "" {
		query = redeemKeyword(query, options.Keyword)
	}
	if options.Status != nil {
		query = query.Where(redeemStatusSQL+" = ?", *options.Status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if offset >= total {
		return nil, total, nil
	}

	column := "created_at"
	switch options.Sort {
	case "code", "name", "amount":
		column = options.Sort
	case "status":
		column = redeemStatusSQL
	}
	direction := " DESC"
	if options.Order == "asc" {
		direction = " ASC"
	}
	if err := query.Order(column + direction + ", id" + direction).
		Limit(int(options.PageSize)).
		Offset(int(offset)).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	codes := make([]*biz.RedeemCode, len(models))
	for i, model := range models {
		code, err := redeemCodeToBiz(&model)
		if err != nil {
			return nil, 0, err
		}
		codes[i] = code
	}

	return codes, total, nil
}

func (r *redeemRepo) SearchRedeemCodes(ctx context.Context, keyword string) ([]*biz.RedeemCode, error) {
	var models []redeemCodeModel

	query, err := authzquery.ApplyContext(ctx, r.data.db.WithContext(ctx).Model(&redeemCodeModel{}), authzquery.Columns{Resource: "id"}, "billing.redemption.list")
	if err != nil {
		return nil, err
	}
	err = redeemKeyword(query, keyword).
		Order("created_at DESC").
		Find(&models).Error

	if err != nil {
		return nil, err
	}

	codes := make([]*biz.RedeemCode, len(models))
	for i, model := range models {
		code, err := redeemCodeToBiz(&model)
		if err != nil {
			return nil, err
		}
		codes[i] = code
	}

	return codes, nil
}

func (r *redeemRepo) UpdateRedeemCode(ctx context.Context, code *biz.RedeemCode) error {
	updates := map[string]any{
		"updated_at": time.Now(),
	}

	if code.Name != "" {
		updates["name"] = code.Name
	}
	if code.Amount > 0 {
		updates["amount"] = code.Amount
	}
	if code.Status >= 0 {
		updates["status"] = code.Status
	}

	return r.mutateRedeem(ctx, code.Code, "billing.redemption.update", func(tx *gorm.DB) error {
		return tx.Model(&redeemCodeModel{}).Where("code = ?", code.Code).Updates(updates).Error
	})
}

func (r *redeemRepo) ClaimRedemptionInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, code *biz.RedeemCode) error {
	db := txDB(tx).WithContext(ctx)
	// Records also cover redemptions made before ledger keys were scoped per user.
	prior := db.Model(&redeemRecordModel{}).Select("1").Where("user_id = ? AND code = ?", userID, code.Code)
	result := db.Model(&redeemCodeModel{}).Where("id = ? AND count > 0 AND NOT EXISTS (?)", code.ID, prior).
		UpdateColumn("count", gorm.Expr("count - 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrDuplicateRequest
	}
	return nil
}

func (r *redeemRepo) DeleteRedeemCode(ctx context.Context, code string) error {
	return r.mutateRedeem(ctx, code, "billing.redemption.delete", func(tx *gorm.DB) error { return tx.Where("code = ?", code).Delete(&redeemCodeModel{}).Error })
}

func (r *redeemRepo) CreateRedeemRecordInTx(ctx context.Context, tx subscriptionbiz.Tx, record *biz.RedeemRecord) error {
	db := txDB(tx)
	model := &redeemRecordModel{
		UserID:        record.UserID,
		Code:          record.Code,
		Amount:        record.Amount,
		BalanceBefore: record.BalanceBefore,
		BalanceAfter:  record.BalanceAfter,
		CreatedAt:     time.Now(),
	}

	return db.WithContext(ctx).Create(model).Error
}

func redeemCodeModelFromBiz(code *biz.RedeemCode, now time.Time) (*redeemCodeModel, error) {
	status, err := safecast.Int32ToInt8(code.Status)
	if err != nil {
		return nil, err
	}
	return &redeemCodeModel{
		Revision: 1, Code: code.Code,
		Name:      stringPtr(code.Name),
		Amount:    code.Amount,
		Count:     int(code.Count),
		Status:    status,
		CreatedBy: stringPtr(code.CreatedBy),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func redeemCodeToBiz(model *redeemCodeModel) (*biz.RedeemCode, error) {
	count, err := safecast.IntToInt32(model.Count)
	if err != nil {
		return nil, err
	}
	id, err := safecast.UintToInt64(model.ID)
	if err != nil {
		return nil, err
	}
	status := int32(model.Status)
	if status == biz.RedeemCodeStatusEnabled && count <= 0 {
		status = biz.RedeemCodeStatusUsed
	}
	return &biz.RedeemCode{
		ID:        id,
		Revision:  model.Revision,
		Code:      model.Code,
		Name:      stringFromPtr(model.Name),
		Amount:    model.Amount,
		Count:     count,
		Status:    status,
		CreatedBy: stringFromPtr(model.CreatedBy),
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}, nil
}

func redeemKeyword(query *gorm.DB, keyword string) *gorm.DB {
	return query.Where("(code = ? OR name LIKE ? ESCAPE '!')", keyword, escapeLike(keyword)+"%")
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	s = strings.ReplaceAll(s, "_", "!_")
	return s
}

func redeemFacts(id int64) authorization.ObjectFacts {
	return authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}
}
func (r *redeemRepo) mutateRedeem(ctx context.Context, code, op string, write func(*gorm.DB) error) error {
	writeErr := authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var row redeemCodeModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", code).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrRedeemCodeNotFound
			}
			return err
		}
		id, err := safecast.UintToInt64(row.ID)
		if err != nil {
			return err
		}
		if err := biz.RequireWrite(ctx, op, redeemFacts(id)); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, op); iam {
			expected, supplied := biz.ExpectedWriteVersion(ctx)
			if !supplied || expected != row.Revision {
				return biz.ErrRoutingContextConflict
			}
		}
		if err := write(tx); err != nil {
			return err
		}
		if op == "billing.redemption.update" {
			if err := tx.Model(&redeemCodeModel{}).Where("id = ?", row.ID).Update("revision", row.Revision+1).Error; err != nil {
				return err
			}
		}
		return authzquery.AppendWriteAudit(ctx, tx, op, id)
	})
	return authzquery.RecordWriteFailure(ctx, r.data.db, op, 0, writeErr)
}
