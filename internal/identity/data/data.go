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
	ID            int64  `gorm:"column:id"`
	Username      string `gorm:"column:username;uniqueIndex"`
	DisplayName   string `gorm:"column:display_name"`
	Email         string `gorm:"column:email"`
	Group         string `gorm:"column:group"`
	Status        int32  `gorm:"column:status"`
	PasswordHash  string `gorm:"column:password_hash"`
	OAuthProvider string `gorm:"column:oauth_provider;index"`
	OAuthID       string `gorm:"column:oauth_id;index"`
	Quota         int64  `gorm:"column:quota"`
	AffCode       string `gorm:"column:aff_code;uniqueIndex"`
	InviterID     int64  `gorm:"column:inviter_id;index"`
}

func (userModel) TableName() string { return "users" }

type tokenModel struct {
	ID             int64   `gorm:"column:id"`
	UserID         int64   `gorm:"column:user_id"`
	Name           string  `gorm:"column:name"`
	Key            string  `gorm:"column:key"`
	Status         int32   `gorm:"column:status"`
	CreatedTime    int64   `gorm:"column:created_time"`
	AccessedTime   int64   `gorm:"column:accessed_time"`
	ExpiredTime    int64   `gorm:"column:expired_time"`
	RemainQuota    int64   `gorm:"column:remain_quota"`
	UnlimitedQuota bool    `gorm:"column:unlimited_quota"`
	UsedQuota      int64   `gorm:"column:used_quota"`
	Models         *string `gorm:"column:models"`
	Subnet         *string `gorm:"column:subnet"`
	CreatedAt      int64   `gorm:"column:created_at"`
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
	redisPassword := os.Getenv("REDIS_PASSWORD")
	rdb := xdb.NewRedisClient(redisAddr, redisPassword)
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
		usersByID:   make(map[int64]*biz.User),
		tokensByKey: make(map[string]*biz.Token),
	}
}

func NewMemoryRepositoryForTest() *Repository {
	return newMemoryRepository()
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

func (r *Repository) FindUserByEmail(ctx context.Context, email string) (*biz.User, error) {
	if r.db != nil {
		return r.findUserByEmailDB(ctx, email)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	for _, u := range r.usersByID {
		if u.Email == email {
			cloned := *u
			return &cloned, nil
		}
	}
	return nil, biz.ErrUserNotFound
}

func (r *Repository) FindUserByAffCode(ctx context.Context, affCode string) (*biz.User, error) {
	if r.db != nil {
		return r.findUserByAffCodeDB(ctx, affCode)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	for _, u := range r.usersByID {
		if u.AffCode == affCode {
			cloned := *u
			return &cloned, nil
		}
	}
	return nil, biz.ErrUserNotFound
}

func (r *Repository) FindUserByOAuth(ctx context.Context, provider, oauthID string) (*biz.User, error) {
	if r.db != nil {
		return r.findUserByOAuthDB(ctx, provider, oauthID)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	for _, u := range r.usersByID {
		if u.OAuthProvider == provider && u.OAuthID == oauthID {
			cloned := *u
			return &cloned, nil
		}
	}
	return nil, biz.ErrOAuthUserNotFound
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

func (r *Repository) IncreaseUserQuota(ctx context.Context, userID int64, amount int64) error {
	if r.db != nil {
		return r.increaseUserQuotaDB(ctx, userID, amount)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	user, ok := r.usersByID[userID]
	if !ok {
		return biz.ErrUserNotFound
	}
	user.Quota += amount
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

func (r *Repository) FindTokenByID(ctx context.Context, userID, tokenID int64) (*biz.Token, error) {
	if r.db != nil {
		return r.findTokenByIDDB(ctx, userID, tokenID)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	for _, token := range r.tokensByKey {
		if token.ID == tokenID && token.UserID == userID {
			cloned := *token
			cloned.Models = append([]string(nil), token.Models...)
			return &cloned, nil
		}
	}
	return nil, biz.ErrTokenNotFound
}

func (r *Repository) ListTokens(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*biz.Token, int64, error) {
	if r.db != nil {
		return r.listTokensDB(ctx, userID, page, pageSize, keyword)
	}
	r.identityLock.RLock()
	defer r.identityLock.RUnlock()
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	var tokens []*biz.Token
	for _, token := range r.tokensByKey {
		if token.UserID != userID {
			continue
		}
		if keyword != "" && !strings.Contains(token.Name, keyword) && !strings.Contains(token.Key, keyword) {
			continue
		}
		cloned := *token
		cloned.Models = append([]string(nil), token.Models...)
		tokens = append(tokens, &cloned)
	}
	total := int64(len(tokens))
	start := int((page - 1) * pageSize)
	if start >= len(tokens) {
		return []*biz.Token{}, total, nil
	}
	end := start + int(pageSize)
	if end > len(tokens) {
		end = len(tokens)
	}
	return tokens[start:end], total, nil
}

func (r *Repository) UpdateToken(ctx context.Context, token *biz.Token) error {
	if r.db != nil {
		return r.updateTokenDB(ctx, token)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	for key, existing := range r.tokensByKey {
		if existing.ID == token.ID && existing.UserID == token.UserID {
			if key != token.Key {
				delete(r.tokensByKey, key)
			}
			r.tokensByKey[token.Key] = token
			return nil
		}
	}
	return biz.ErrTokenNotFound
}

func (r *Repository) DeleteToken(ctx context.Context, userID, tokenID int64) error {
	if r.db != nil {
		return r.deleteTokenDB(ctx, userID, tokenID)
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	for key, token := range r.tokensByKey {
		if token.ID == tokenID && token.UserID == userID {
			delete(r.tokensByKey, key)
			return nil
		}
	}
	return biz.ErrTokenNotFound
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
	return tokenModelToBiz(model), nil
}

func (r *Repository) findTokenByIDDB(ctx context.Context, userID, tokenID int64) (*biz.Token, error) {
	var model tokenModel
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", tokenID, userID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrTokenNotFound
		}
		return nil, err
	}
	return tokenModelToBiz(model), nil
}

func (r *Repository) findUserByIDDB(ctx context.Context, userID int64) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("id = ?", userID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return userModelToBiz(model), nil
}

func (r *Repository) findUserByUsernameDB(ctx context.Context, username string) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return userModelToBiz(model), nil
}

func (r *Repository) findUserByEmailDB(ctx context.Context, email string) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return userModelToBiz(model), nil
}

func (r *Repository) findUserByAffCodeDB(ctx context.Context, affCode string) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("aff_code = ?", affCode).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return userModelToBiz(model), nil
}

func (r *Repository) findUserByOAuthDB(ctx context.Context, provider, oauthID string) (*biz.User, error) {
	var model userModel
	if err := r.db.WithContext(ctx).Where("oauth_provider = ? AND oauth_id = ?", provider, oauthID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrOAuthUserNotFound
		}
		return nil, err
	}
	return userModelToBiz(model), nil
}

func (r *Repository) createUserDB(ctx context.Context, user *biz.User) error {
	model := userModel{
		Username:      user.Username,
		DisplayName:   user.DisplayName,
		Email:         user.Email,
		Group:         user.Group,
		Status:        user.Status,
		PasswordHash:  user.PasswordHash,
		OAuthProvider: user.OAuthProvider,
		OAuthID:       user.OAuthID,
		Quota:         user.Quota,
		AffCode:       user.AffCode,
		InviterID:     user.InviterID,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	user.ID = model.ID
	return nil
}

func (r *Repository) updateUserDB(ctx context.Context, user *biz.User) error {
	return r.db.WithContext(ctx).Model(&userModel{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"username":      user.Username,
		"display_name":  user.DisplayName,
		"email":         user.Email,
		"group":         user.Group,
		"status":        user.Status,
		"password_hash": user.PasswordHash,
		"aff_code":      user.AffCode,
		"inviter_id":    user.InviterID,
	}).Error
}

func (r *Repository) increaseUserQuotaDB(ctx context.Context, userID int64, amount int64) error {
	result := r.db.WithContext(ctx).Model(&userModel{}).
		Where("id = ?", userID).
		Update("quota", gorm.Expr("quota + ?", amount))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrUserNotFound
	}
	return nil
}

func (r *Repository) deleteUserDB(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Where("id = ?", userID).Delete(&userModel{}).Error
}

func (r *Repository) createTokenDB(ctx context.Context, token *biz.Token) error {
	model := tokenModel{
		UserID:         token.UserID,
		Name:           token.Name,
		Key:            token.Key,
		Status:         token.Status,
		ExpiredTime:    token.ExpiredAt,
		RemainQuota:    token.RemainQuota,
		UnlimitedQuota: token.UnlimitedQuota,
		UsedQuota:      token.UsedQuota,
		Models:         strPtr(strings.Join(token.Models, ",")),
		Subnet:         strPtr(token.Subnet),
		CreatedAt:      token.CreatedAt,
		CreatedTime:    token.CreatedAt,
		AccessedTime:   token.AccessedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	token.ID = model.ID
	return nil
}

func (r *Repository) listTokensDB(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*biz.Token, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	var models []tokenModel
	query := r.db.WithContext(ctx).Model(&tokenModel{}).Where("user_id = ?", userID)
	if keyword != "" {
		like := "%" + escapeLike(keyword) + "%"
		query = query.Where("name LIKE ? OR `key` LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := query.Order("id DESC").Offset(int(offset)).Limit(int(pageSize)).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	tokens := make([]*biz.Token, len(models))
	for i, model := range models {
		tokens[i] = tokenModelToBiz(model)
	}
	return tokens, total, nil
}

func (r *Repository) updateTokenDB(ctx context.Context, token *biz.Token) error {
	return r.db.WithContext(ctx).Model(&tokenModel{}).
		Where("id = ? AND user_id = ?", token.ID, token.UserID).
		Updates(map[string]interface{}{
			"name":            token.Name,
			"status":          token.Status,
			"expired_time":    token.ExpiredAt,
			"remain_quota":    token.RemainQuota,
			"unlimited_quota": token.UnlimitedQuota,
			"used_quota":      token.UsedQuota,
			"models":          strings.Join(token.Models, ","),
			"subnet":          token.Subnet,
			"accessed_time":   token.AccessedAt,
		}).Error
}

func (r *Repository) deleteTokenDB(ctx context.Context, userID, tokenID int64) error {
	result := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", tokenID, userID).Delete(&tokenModel{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrTokenNotFound
	}
	return nil
}

func tokenModelToBiz(model tokenModel) *biz.Token {
	return &biz.Token{
		ID:             model.ID,
		UserID:         model.UserID,
		Name:           model.Name,
		Key:            model.Key,
		Status:         model.Status,
		ExpiredAt:      model.ExpiredTime,
		RemainQuota:    model.RemainQuota,
		UnlimitedQuota: model.UnlimitedQuota,
		UsedQuota:      model.UsedQuota,
		AccessedAt:     firstNonZero(model.AccessedTime, model.CreatedAt, model.CreatedTime),
		Subnet:         stringPtrValue(model.Subnet),
		Models:         biz.SplitCSVPtr(model.Models),
		CreatedAt:      firstNonZero(model.CreatedTime, model.CreatedAt),
	}
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func userModelToBiz(model userModel) *biz.User {
	return &biz.User{
		ID:            model.ID,
		Username:      model.Username,
		DisplayName:   model.DisplayName,
		Email:         model.Email,
		Group:         model.Group,
		Status:        model.Status,
		PasswordHash:  model.PasswordHash,
		OAuthProvider: model.OAuthProvider,
		OAuthID:       model.OAuthID,
		Quota:         model.Quota,
		AffCode:       model.AffCode,
		InviterID:     model.InviterID,
	}
}

func (r *Repository) listUsersDB(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*biz.User, int64, error) {
	var models []userModel
	query := r.db.WithContext(ctx).Model(&userModel{})
	if keyword != "" {
		query = query.Where("username LIKE ?", "%"+escapeLike(keyword)+"%")
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
			Quota:       m.Quota,
			AffCode:     m.AffCode,
			InviterID:   m.InviterID,
		}
	}
	return users, total, nil
}

func strPtr(s string) *string { return &s }

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
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
