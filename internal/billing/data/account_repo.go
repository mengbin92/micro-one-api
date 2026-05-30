package data

import (
	"context"
	"errors"
	"strconv"

	"micro-one-api/internal/billing/biz"

	"gorm.io/gorm"
)

type accountRepo struct {
	data *Data
}

func NewAccountRepo(data *Data) biz.AccountRepo {
	return &accountRepo{data: data}
}

func (r *accountRepo) GetAccountSnapshot(ctx context.Context, userID string) (*biz.Account, error) {
	var user struct {
		ID           int64  `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		DisplayName  string `gorm:"column:display_name"`
		Group        string `gorm:"column:group"`
		Quota        int64  `gorm:"column:quota"`
		UsedQuota    int64  `gorm:"column:used_quota"`
		RequestCount int64  `gorm:"column:request_count"`
		FrozenQuota  int64  `gorm:"column:frozen_quota"`
		Status       int32  `gorm:"column:status"`
	}

	if err := r.data.db.WithContext(ctx).Table("users").Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrAccountNotFound
		}
		return nil, err
	}

	return &biz.Account{
		UserID:       userID,
		Username:     user.Username,
		DisplayName:  user.DisplayName,
		Group:        user.Group,
		Quota:        user.Quota,
		UsedQuota:    user.UsedQuota,
		RequestCount: user.RequestCount,
		FrozenQuota:  user.FrozenQuota,
		Status:       user.Status,
	}, nil
}

func (r *accountRepo) UpdateQuota(ctx context.Context, userID string, delta int64, operationType string) (int64, error) {
	var account struct {
		Quota int64 `gorm:"column:quota"`
	}

	tx := r.data.db.WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Table("users").Where("id = ?", userID).First(&account).Error; err != nil {
		tx.Rollback()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, biz.ErrAccountNotFound
		}
		return 0, err
	}

	newQuota := account.Quota + delta
	if newQuota < 0 {
		tx.Rollback()
		return 0, biz.ErrInsufficientQuota
	}

	if err := tx.Table("users").Where("id = ?", userID).Update("quota", newQuota).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	return newQuota, nil
}

func (r *accountRepo) UpdateUsage(ctx context.Context, userID string, usedQuotaDelta, requestCountDelta int64) error {
	return r.data.db.WithContext(ctx).Table("users").
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"used_quota":    gorm.Expr("used_quota + ?", usedQuotaDelta),
			"request_count": gorm.Expr("request_count + ?", requestCountDelta),
		}).Error
}

func (r *accountRepo) UpdateFrozenQuota(ctx context.Context, userID string, delta int64) error {
	// 先查询当前值
	var account struct {
		FrozenQuota int64 `gorm:"column:frozen_quota"`
	}
	if err := r.data.db.WithContext(ctx).Table("users").Where("id = ?", userID).First(&account).Error; err != nil {
		return err
	}

	// 计算新值
	newFrozenQuota := account.FrozenQuota + delta
	return r.data.db.WithContext(ctx).Table("users").Where("id = ?", userID).Update("frozen_quota", newFrozenQuota).Error
}

func (r *accountRepo) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*biz.Account, error) {
	if len(userIDs) == 0 {
		return map[string]*biz.Account{}, nil
	}

	var users []struct {
		ID           int64  `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		DisplayName  string `gorm:"column:display_name"`
		Group        string `gorm:"column:group"`
		Quota        int64  `gorm:"column:quota"`
		UsedQuota    int64  `gorm:"column:used_quota"`
		RequestCount int64  `gorm:"column:request_count"`
		FrozenQuota  int64  `gorm:"column:frozen_quota"`
		Status       int32  `gorm:"column:status"`
	}

	if err := r.data.db.WithContext(ctx).Table("users").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}

	result := make(map[string]*biz.Account, len(users))
	for _, user := range users {
		userID := strconv.FormatInt(user.ID, 10)
		result[userID] = &biz.Account{
			UserID:       userID,
			Username:     user.Username,
			DisplayName:  user.DisplayName,
			Group:        user.Group,
			Quota:        user.Quota,
			UsedQuota:    user.UsedQuota,
			RequestCount: user.RequestCount,
			FrozenQuota:  user.FrozenQuota,
			Status:       user.Status,
		}
	}

	return result, nil
}
