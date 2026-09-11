package data

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/billing/internal/biz"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

type commerceReceipt struct {
	UserID      int64  `gorm:"primaryKey"`
	RequestID   string `gorm:"primaryKey"`
	RequestHash string
	Result      string
}

func (commerceReceipt) TableName() string { return "subscription_commerce_receipts" }

type subscriptionCommerceRepo struct{ data *Data }

func NewSubscriptionCommerceRepo(d *Data) biz.SubscriptionCommerceRepo {
	return &subscriptionCommerceRepo{d}
}
func (r *subscriptionCommerceRepo) Execute(ctx context.Context, user int64, request, hash string, fn func(context.Context, subscriptionbiz.Tx) (*biz.SubscriptionCommerceResult, error)) (*biz.SubscriptionCommerceResult, error) {
	var result *biz.SubscriptionCommerceResult
	err := r.data.DB().WithContext(ctx).Transaction(func(db *gorm.DB) error {
		row := commerceReceipt{UserID: user, RequestID: request, RequestHash: hash}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND request_id = ?", user, request).Take(&row).Error; err != nil {
			return err
		}
		if row.RequestHash != hash {
			return biz.ErrRoutingContextConflict
		}
		if row.Result != "" {
			if err := jsonx.Unmarshal([]byte(row.Result), &result); err != nil {
				return err
			}
			return nil
		}
		var err error
		result, err = fn(ctx, &gormTx{db: db})
		if err != nil {
			return err
		}
		raw, err := jsonx.Marshal(result)
		if err != nil {
			return err
		}
		return db.Model(&commerceReceipt{}).Where("user_id = ? AND request_id = ?", user, request).Update("result", string(raw)).Error
	})
	return result, err
}
