package data

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"micro-one-api/internal/identity/biz"
	"micro-one-api/internal/pkg/xdb"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Repository struct {
	db           *gorm.DB
	redis        *redis.Client
	usersByID    map[int64]*biz.User
	tokensByKey  map[string]*biz.Token
	identityLock sync.RWMutex
}

type userModel struct {
	ID           int64  `gorm:"column:id"`
	Username     string `gorm:"column:username;uniqueIndex"`
	DisplayName  string `gorm:"column:display_name"`
	Email        string `gorm:"column:email"`
	Group        string `gorm:"column:group"`
	Status       int32  `gorm:"column:status"`
	PasswordHash string `gorm:"column:password_hash"`
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

func NewRepositoryFromEnv(dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("IDENTITY_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
	}
	if dbDSN == "" {
		return newMemoryRepository(), nil
	}
	db, err := xdb.OpenMySQL(dbDSN)
	if err != nil {
		return nil, err
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	rdb := xdb.NewRedisClient(redisAddr)
	if rdb != nil {
		if pingErr := rdb.Ping(context.Background()).Err(); pingErr != nil {
			rdb.Close()
			rdb = nil
		}
	}
	return &Repository{db: db, redis: rdb}, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		usersByID: map[int64]*biz.User{
			1: {
				ID:           1,
				Username:     "root",
				DisplayName:  "Root User",
				Group:        "default",
				Status:       biz.UserStatusEnabled,
				PasswordHash: "$2a$10$PizUqaAa4Zkpmbt0zcR3ouRiWZunVYRrA7I3UD64K0Qqcdh2Cq132", // "password"
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

func (r *Repository) FindUserByUsername(ctx context.Context, username string) (*biz.User, error) {
	if r.db != nil {
		return r.findUserByUsernameDB(ctx, username)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	for _, u := range r.usersByID {
		if u.Username == username {
			cloned := *u
			return &cloned, nil
		}
	}
	return nil, biz.ErrUserNotFound
}

func (r *Repository) CreateUser(ctx context.Context, user *biz.User) error {
	if r.db != nil {
		return r.createUserDB(ctx, user)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	user.ID = int64(len(r.usersByID) + 1)
	r.usersByID[user.ID] = user
	return nil
}

func (r *Repository) UpdateUser(ctx context.Context, user *biz.User) error {
	if r.db != nil {
		return r.updateUserDB(ctx, user)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	if _, ok := r.usersByID[user.ID]; !ok {
		return biz.ErrUserNotFound
	}
	r.usersByID[user.ID] = user
	return nil
}

func (r *Repository) DeleteUser(ctx context.Context, userID int64) error {
	if r.db != nil {
		return r.deleteUserDB(ctx, userID)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	if _, ok := r.usersByID[userID]; !ok {
		return biz.ErrUserNotFound
	}
	delete(r.usersByID, userID)
	return nil
}

func (r *Repository) CreateToken(ctx context.Context, token *biz.Token) error {
	if r.db != nil {
		return r.createTokenDB(ctx, token)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	token.ID = int64(len(r.tokensByKey) + 1)
	r.tokensByKey[token.Key] = token
	return nil
}

func (r *Repository) ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*biz.User, int64, error) {
	if r.db != nil {
		return r.listUsersDB(ctx, page, pageSize, keyword, group, status)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	var users []*biz.User
	for _, u := range r.usersByID {
		if keyword != "" && !strings.Contains(u.Username, keyword) {
			continue
		}
		if group != "" && u.Group != group {
			continue
		}
		if status != 0 && u.Status != status {
			continue
		}
		cloned := *u
		users = append(users, &cloned)
	}
	return users, int64(len(users)), nil
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
		ID:           model.ID,
		Username:     model.Username,
		DisplayName:  model.DisplayName,
		Email:        model.Email,
		Group:        model.Group,
		Status:       model.Status,
		PasswordHash: model.PasswordHash,
	}, nil
}

func (r *Repository) findUserByUsernameDB(ctx context.Context, username string) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return &biz.User{
		ID:           model.ID,
		Username:     model.Username,
		DisplayName:  model.DisplayName,
		Email:        model.Email,
		Group:        model.Group,
		Status:       model.Status,
		PasswordHash: model.PasswordHash,
	}, nil
}

func (r *Repository) createUserDB(ctx context.Context, user *biz.User) error {
	model := userModel{
		Username:     user.Username,
		DisplayName:  user.DisplayName,
		Email:        user.Email,
		Group:        user.Group,
		Status:       user.Status,
		PasswordHash: user.PasswordHash,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	user.ID = model.ID
	return nil
}

func (r *Repository) updateUserDB(ctx context.Context, user *biz.User) error {
	return r.db.WithContext(ctx).Model(&userModel{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"display_name": user.DisplayName,
		"email":       user.Email,
		"group":       user.Group,
		"status":      user.Status,
	}).Error
}

func (r *Repository) deleteUserDB(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Where("id = ?", userID).Delete(&userModel{}).Error
}

func (r *Repository) createTokenDB(ctx context.Context, token *biz.Token) error {
	model := tokenModel{
		UserID:         token.UserID,
		Key:            token.Key,
		Status:         token.Status,
		ExpiredTime:    token.ExpiredAt,
		RemainQuota:    token.RemainQuota,
		UnlimitedQuota: token.UnlimitedQuota,
		Models:         strPtr(strings.Join(token.Models, ",")),
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	token.ID = model.ID
	return nil
}

func (r *Repository) listUsersDB(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*biz.User, int64, error) {
	var models []userModel
	query := r.db.WithContext(ctx).Model(&userModel{})
	if keyword != "" {
		query = query.Where("username LIKE ?", "%"+keyword+"%")
	}
	if group != "" {
		query = query.Where("`group` = ?", group)
	}
	if status != 0 {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	users := make([]*biz.User, len(models))
	for i, m := range models {
		users[i] = &biz.User{
			ID:          m.ID,
			Username:    m.Username,
			DisplayName: m.DisplayName,
			Email:       m.Email,
			Group:       m.Group,
			Status:      m.Status,
		}
	}
	return users, total, nil
}

func strPtr(s string) *string { return &s }

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
