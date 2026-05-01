package data

import (
	"context"
	"errors"
	"os"
	"sync"

	"micro-one-api/internal/identity/biz"
	"micro-one-api/internal/pkg/xdb"

	"gorm.io/gorm"
)

type Repository struct {
	db           *gorm.DB
	usersByID    map[int64]*biz.User
	tokensByKey  map[string]*biz.Token
	identityLock sync.RWMutex
}

type userModel struct {
	ID          int64  `gorm:"column:id"`
	Username    string `gorm:"column:username"`
	DisplayName string `gorm:"column:display_name"`
	Group       string `gorm:"column:group"`
	Status      int32  `gorm:"column:status"`
}

func (userModel) TableName() string { return "users" }

type tokenModel struct {
	ID             int64   `gorm:"column:id"`
	UserID         int64   `gorm:"column:user_id"`
	Key            string  `gorm:"column:key"`
	Status         int32   `gorm:"column:status"`
	ExpiredTime    int64   `gorm:"column:expired_time"`
	RemainQuota    int64   `gorm:"column:remain_quota"`
	UnlimitedQuota bool    `gorm:"column:unlimited_quota"`
	Models         *string `gorm:"column:models"`
}

func (tokenModel) TableName() string { return "tokens" }

func NewRepositoryFromEnv() (*Repository, error) {
	dsn := os.Getenv("IDENTITY_SQL_DSN")
	if dsn == "" {
		dsn = os.Getenv("SQL_DSN")
	}
	if dsn == "" {
		return newMemoryRepository(), nil
	}
	db, err := xdb.OpenMySQL(dsn)
	if err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		usersByID: map[int64]*biz.User{
			1: {
				ID:          1,
				Username:    "root",
				DisplayName: "Root User",
				Group:       "default",
				Status:      biz.UserStatusEnabled,
			},
		},
		tokensByKey: map[string]*biz.Token{
			"demo-token": {
				ID:             1,
				UserID:         1,
				Key:            "demo-token",
				Status:         biz.TokenStatusEnabled,
				UnlimitedQuota: true,
				Models:         []string{"gpt-4o-mini", "gpt-4.1", "claude-3-5-sonnet"},
			},
		},
	}
}

func (r *Repository) FindTokenByKey(ctx context.Context, key string) (*biz.Token, error) {
	if r.db != nil {
		return r.findTokenByKeyDB(ctx, key)
	}
	return r.findTokenByKeyMemory(ctx, key)
}

func (r *Repository) FindUserByID(ctx context.Context, userID int64) (*biz.User, error) {
	if r.db != nil {
		return r.findUserByIDDB(ctx, userID)
	}
	return r.findUserByIDMemory(ctx, userID)
}

func (r *Repository) findTokenByKeyDB(ctx context.Context, key string) (*biz.Token, error) {
	var model tokenModel
	if err := r.db.WithContext(ctx).Where("`key` = ?", key).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrTokenNotFound
		}
		return nil, err
	}
	return &biz.Token{
		ID:             model.ID,
		UserID:         model.UserID,
		Key:            model.Key,
		Status:         model.Status,
		ExpiredAt:      model.ExpiredTime,
		RemainQuota:    model.RemainQuota,
		UnlimitedQuota: model.UnlimitedQuota,
		Models:         biz.SplitCSVPtr(model.Models),
	}, nil
}

func (r *Repository) findUserByIDDB(ctx context.Context, userID int64) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("id = ?", userID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return &biz.User{
		ID:          model.ID,
		Username:    model.Username,
		DisplayName: model.DisplayName,
		Group:       model.Group,
		Status:      model.Status,
	}, nil
}

func (r *Repository) findTokenByKeyMemory(_ context.Context, key string) (*biz.Token, error) {
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	token, ok := r.tokensByKey[key]
	if !ok {
		return nil, biz.ErrTokenNotFound
	}
	cloned := *token
	cloned.Models = append([]string(nil), token.Models...)
	return &cloned, nil
}

func (r *Repository) findUserByIDMemory(_ context.Context, userID int64) (*biz.User, error) {
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	user, ok := r.usersByID[userID]
	if !ok {
		return nil, biz.ErrUserNotFound
	}
	cloned := *user
	return &cloned, nil
}
