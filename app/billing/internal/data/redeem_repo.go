package data

import (
	"context"
	"errors"
	"strings"
	"time"

	"micro-one-api/app/billing/internal/biz"
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

	return r.data.db.WithContext(ctx).Create(model).Error
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

	return r.data.db.WithContext(ctx).Create(&models).Error
}

func (r *redeemRepo) GetRedeemCode(ctx context.Context, code string) (*biz.RedeemCode, error) {
	var model redeemCodeModel
	if err := r.data.db.WithContext(ctx).Where("code = ?", code).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrRedeemCodeNotFound
		}
		return nil, err
	}

	return redeemCodeToBiz(&model)
}

func (r *redeemRepo) ListRedeemCodes(ctx context.Context, page, pageSize int32) ([]*biz.RedeemCode, int64, error) {
	var models []redeemCodeModel
	var total int64

	offset := (page - 1) * pageSize

	if err := r.data.db.WithContext(ctx).
		Model(&redeemCodeModel{}).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := r.data.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(int(pageSize)).
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

	err := r.data.db.WithContext(ctx).
		Where("code = ? OR name LIKE ? ESCAPE '!'", keyword, escapeLike(keyword)+"%").
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
	updates := map[string]interface{}{
		"updated_at": time.Now(),
	}

	if code.Name != "" {
		updates["name"] = code.Name
	}
	if code.Amount > 0 {
		updates["amount"] = code.Amount
	}
	if code.Status > 0 {
		updates["status"] = code.Status
	}

	return r.data.db.WithContext(ctx).
		Model(&redeemCodeModel{}).
		Where("code = ?", code.Code).
		Updates(updates).Error
}

func (r *redeemRepo) UpdateRedeemCodeCount(ctx context.Context, code string, delta int) error {
	// 先查询当前值
	var model redeemCodeModel
	if err := r.data.db.WithContext(ctx).Where("code = ?", code).First(&model).Error; err != nil {
		return err
	}

	// 检查是否有足够的数量
	if model.Count < delta {
		return errors.New("insufficient redeem code count")
	}

	// 更新数量
	return r.data.db.WithContext(ctx).Model(&redeemCodeModel{}).
		Where("code = ?", code).
		Update("count", model.Count-delta).Error
}

func (r *redeemRepo) DeleteRedeemCode(ctx context.Context, code string) error {
	return r.data.db.WithContext(ctx).
		Where("code = ?", code).
		Delete(&redeemCodeModel{}).Error
}

func (r *redeemRepo) CreateRedeemRecord(ctx context.Context, record *biz.RedeemRecord) error {
	model := &redeemRecordModel{
		UserID:        record.UserID,
		Code:          record.Code,
		Amount:        record.Amount,
		BalanceBefore: record.BalanceBefore,
		BalanceAfter:  record.BalanceAfter,
		CreatedAt:     time.Now(),
	}

	return r.data.db.WithContext(ctx).Create(model).Error
}

func redeemCodeModelFromBiz(code *biz.RedeemCode, now time.Time) (*redeemCodeModel, error) {
	status, err := safecast.Int32ToInt8(code.Status)
	if err != nil {
		return nil, err
	}
	return &redeemCodeModel{
		Code:      code.Code,
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
	return &biz.RedeemCode{
		Code:      model.Code,
		Name:      stringFromPtr(model.Name),
		Amount:    model.Amount,
		Count:     count,
		Status:    int32(model.Status),
		CreatedBy: stringFromPtr(model.CreatedBy),
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	s = strings.ReplaceAll(s, "_", "!_")
	return s
}
