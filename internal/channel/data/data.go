package data

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"

	"micro-one-api/internal/channel/biz"
	appcrypto "micro-one-api/internal/pkg/crypto"
	"micro-one-api/internal/pkg/xdb"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Repository struct {
	db       *gorm.DB
	redis    *redis.Client
	channels map[int64]*biz.Channel
	lock     sync.RWMutex
	encKey   []byte // AES key for encrypting API keys at rest (nil = no encryption)
}

type channelModel struct {
	ID       int64   `gorm:"column:id"`
	Type     int32   `gorm:"column:type"`
	Key      string  `gorm:"column:key"`
	Status   int32   `gorm:"column:status"`
	Name     string  `gorm:"column:name"`
	BaseURL  *string `gorm:"column:base_url"`
	Models   string  `gorm:"column:models"`
	Group    string  `gorm:"column:group"`
	Priority *int64  `gorm:"column:priority"`
	Config   string  `gorm:"column:config"`
}

func (channelModel) TableName() string { return "channels" }

type abilityModel struct {
	Group     string `gorm:"column:group"`
	Model     string `gorm:"column:model"`
	ChannelID int64  `gorm:"column:channel_id"`
	Enabled   bool   `gorm:"column:enabled"`
	Priority  *int64 `gorm:"column:priority"`
}

func (abilityModel) TableName() string { return "abilities" }

func NewRepositoryFromEnv(dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("CHANNEL_SQL_DSN")
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
	repo := &Repository{db: db, redis: rdb}
	if key := os.Getenv("CHANNEL_ENCRYPTION_KEY"); key != "" {
		repo.encKey = []byte(key)
	}
	return repo, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		channels: make(map[int64]*biz.Channel),
	}
}

func (r *Repository) FindByID(ctx context.Context, channelID int64) (*biz.Channel, error) {
	if r.db != nil {
		return r.findByIDDB(ctx, channelID)
	}
	return r.findByIDMemory(ctx, channelID)
}

func (r *Repository) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]biz.Ability, error) {
	if r.db != nil {
		return r.listAbilitiesByGroupAndModelDB(ctx, group, model)
	}
	return r.listAbilitiesByGroupAndModelMemory(ctx, group, model)
}

func (r *Repository) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	if r.db != nil {
		return r.listAvailableModelsDB(ctx, group)
	}
	return r.listAvailableModelsMemory(ctx, group)
}

func (r *Repository) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
	if r.db != nil {
		return r.listChannelsDB(ctx, page, pageSize, keyword, group, status, chType)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.Channel
	for _, ch := range r.channels {
		result = append(result, ch)
	}
	return result, int64(len(result)), nil
}

func (r *Repository) CreateChannel(ctx context.Context, channel *biz.Channel) error {
	if r.db != nil {
		return r.createChannelDB(ctx, channel)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	channel.ID = int64(len(r.channels) + 1)
	r.channels[channel.ID] = channel
	return nil
}

func (r *Repository) UpdateChannel(ctx context.Context, channel *biz.Channel) error {
	if r.db != nil {
		return r.updateChannelDB(ctx, channel)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.channels[channel.ID]; !ok {
		return biz.ErrChannelNotFound
	}
	r.channels[channel.ID] = channel
	return nil
}

func (r *Repository) DeleteChannel(ctx context.Context, channelID int64) error {
	if r.db != nil {
		return r.deleteChannelDB(ctx, channelID)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.channels, channelID)
	return nil
}

func (r *Repository) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	if r.db != nil {
		return r.changeStatusDB(ctx, channelID, status)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	ch, ok := r.channels[channelID]
	if !ok {
		return biz.ErrChannelNotFound
	}
	ch.Status = status
	return nil
}

func (r *Repository) findByIDDB(ctx context.Context, channelID int64) (*biz.Channel, error) {
	var model channelModel
	if err := r.db.WithContext(ctx).Where("id = ?", channelID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	baseURL := ""
	if model.BaseURL != nil {
		baseURL = *model.BaseURL
	}
	priority := int64(0)
	if model.Priority != nil {
		priority = *model.Priority
	}
	return &biz.Channel{
		ID:       model.ID,
		Type:     model.Type,
		Name:     model.Name,
		Status:   model.Status,
		BaseURL:  baseURL,
		Group:    model.Group,
		Models:   biz.SplitCSV(model.Models),
		Priority: priority,
		Key:      r.decryptKey(model.Key),
		Config:   biz.DecodeChannelConfig(model.Config),
	}, nil
}

func (r *Repository) listAbilitiesByGroupAndModelDB(ctx context.Context, group, model string) ([]biz.Ability, error) {
	var rows []abilityModel
	if err := r.db.WithContext(ctx).
		Where("`group` = ? AND model = ? AND enabled = ?", group, model, true).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	abilities := make([]biz.Ability, 0, len(rows))
	for _, row := range rows {
		priority := int64(0)
		if row.Priority != nil {
			priority = *row.Priority
		}
		abilities = append(abilities, biz.Ability{
			Group:     row.Group,
			Model:     row.Model,
			ChannelID: row.ChannelID,
			Enabled:   row.Enabled,
			Priority:  priority,
		})
	}
	return abilities, nil
}

func (r *Repository) listAvailableModelsDB(ctx context.Context, group string) ([]string, error) {
	var models []string
	if err := r.db.WithContext(ctx).
		Model(&abilityModel{}).
		Where("`group` = ? AND enabled = ?", group, true).
		Distinct("model").
		Pluck("model", &models).Error; err != nil {
		return nil, err
	}
	sort.Strings(models)
	return models, nil
}

func (r *Repository) findByIDMemory(_ context.Context, channelID int64) (*biz.Channel, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	channel, ok := r.channels[channelID]
	if !ok {
		return nil, biz.ErrChannelNotFound
	}
	cloned := *channel
	cloned.Models = append([]string(nil), channel.Models...)
	return &cloned, nil
}

func (r *Repository) listAbilitiesByGroupAndModelMemory(_ context.Context, group, model string) ([]biz.Ability, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	abilities := make([]biz.Ability, 0)
	for _, channel := range r.channels {
		if channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		for _, channelGroup := range biz.SplitCSV(channel.Group) {
			if channelGroup != group {
				continue
			}
			for _, channelModel := range channel.Models {
				if channelModel != model {
					continue
				}
				abilities = append(abilities, biz.Ability{
					Group:     group,
					Model:     model,
					ChannelID: channel.ID,
					Enabled:   true,
					Priority:  channel.Priority,
				})
			}
		}
	}
	return abilities, nil
}

func (r *Repository) listAvailableModelsMemory(_ context.Context, group string) ([]string, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	seen := make(map[string]struct{})
	for _, channel := range r.channels {
		if channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		for _, channelGroup := range biz.SplitCSV(channel.Group) {
			if channelGroup != group {
				continue
			}
			for _, model := range channel.Models {
				seen[model] = struct{}{}
			}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func (r *Repository) listChannelsDB(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
	query := r.db.WithContext(ctx).Model(&channelModel{})
	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+escapeLike(keyword)+"%")
	}
	if group != "" {
		query = query.Where("`group` = ?", group)
	}
	if status != 0 {
		query = query.Where("status = ?", status)
	}
	if chType != 0 {
		query = query.Where("type = ?", chType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []channelModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Channel, len(models))
	for i, m := range models {
		result[i] = r.modelToChannel(&m)
	}
	return result, total, nil
}

func (r *Repository) createChannelDB(ctx context.Context, channel *biz.Channel) error {
	model := r.channelToModel(channel)
	return r.db.WithContext(ctx).Create(model).Error
}

func (r *Repository) updateChannelDB(ctx context.Context, channel *biz.Channel) error {
	model := r.channelToModel(channel)
	return r.db.WithContext(ctx).Model(&channelModel{}).Where("id = ?", channel.ID).Updates(map[string]interface{}{
		"name":     model.Name,
		"base_url": model.BaseURL,
		"key":      model.Key,
		"models":   model.Models,
		"group":    model.Group,
		"priority": model.Priority,
		"config":   model.Config,
	}).Error
}

func (r *Repository) deleteChannelDB(ctx context.Context, channelID int64) error {
	return r.db.WithContext(ctx).Where("id = ?", channelID).Delete(&channelModel{}).Error
}

func (r *Repository) changeStatusDB(ctx context.Context, channelID int64, status int32) error {
	return r.db.WithContext(ctx).Model(&channelModel{}).Where("id = ?", channelID).Update("status", status).Error
}

// encryptKey encrypts an API key for storage. Returns plaintext if no encryption key is set.
func (r *Repository) encryptKey(key string) string {
	if r.encKey == nil || key == "" {
		return key
	}
	encrypted, err := appcrypto.Encrypt(key, r.encKey)
	if err != nil {
		// Log error but return plaintext to avoid data loss
		return key
	}
	return encrypted
}

// decryptKey decrypts an API key from storage. Returns as-is if no encryption key is set.
func (r *Repository) decryptKey(key string) string {
	if r.encKey == nil || key == "" {
		return key
	}
	decrypted, err := appcrypto.Decrypt(key, r.encKey)
	if err != nil {
		// If decryption fails, assume it's stored as plaintext (migration scenario)
		return key
	}
	return decrypted
}

func (r *Repository) modelToChannel(m *channelModel) *biz.Channel {
	baseURL := ""
	if m.BaseURL != nil {
		baseURL = *m.BaseURL
	}
	priority := int64(0)
	if m.Priority != nil {
		priority = *m.Priority
	}
	return &biz.Channel{
		ID:       m.ID,
		Type:     m.Type,
		Name:     m.Name,
		Status:   m.Status,
		BaseURL:  baseURL,
		Group:    m.Group,
		Models:   biz.SplitCSV(m.Models),
		Priority: priority,
		Key:      r.decryptKey(m.Key),
		Config:   biz.DecodeChannelConfig(m.Config),
	}
}

func (r *Repository) channelToModel(ch *biz.Channel) *channelModel {
	return &channelModel{
		ID:      ch.ID,
		Type:    ch.Type,
		Name:    ch.Name,
		Status:  ch.Status,
		BaseURL: strPtr(ch.BaseURL),
		Models:  ch.ModelsCSV(),
		Group:   ch.Group,
		Priority: int64Ptr(ch.Priority),
		Key:     r.encryptKey(ch.Key),
		Config:  "{}",
	}
}

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
