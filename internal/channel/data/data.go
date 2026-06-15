package data

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"micro-one-api/internal/channel/biz"
	appcrypto "micro-one-api/internal/pkg/crypto"
	"micro-one-api/internal/pkg/safecast"
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
	ID                                int64   `gorm:"column:id"`
	Type                              int32   `gorm:"column:type"`
	Key                               string  `gorm:"column:key"`
	Status                            int32   `gorm:"column:status"`
	Name                              string  `gorm:"column:name"`
	Weight                            *uint   `gorm:"column:weight"`
	CreatedTime                       int64   `gorm:"column:created_time"`
	TestTime                          int64   `gorm:"column:test_time"`
	ResponseTime                      int64   `gorm:"column:response_time"`
	BaseURL                           *string `gorm:"column:base_url"`
	Balance                           float64 `gorm:"column:balance"`
	BalanceUpdatedTime                int64   `gorm:"column:balance_updated_time"`
	BalanceRefreshLastError           *string `gorm:"column:balance_refresh_last_error"`
	BalanceRefreshLastSuccessTime     int64   `gorm:"column:balance_refresh_last_success_time"`
	ConsecutiveBalanceRefreshFailures int32   `gorm:"column:consecutive_balance_refresh_failures"`
	HealthStatus                      string  `gorm:"column:health_status"`
	HealthLastError                   *string `gorm:"column:health_last_error"`
	HealthLastSuccessTime             int64   `gorm:"column:health_last_success_time"`
	HealthLastFailureTime             int64   `gorm:"column:health_last_failure_time"`
	HealthConsecutiveFailures         int32   `gorm:"column:health_consecutive_failures"`
	CircuitOpenedUntil                int64   `gorm:"column:circuit_opened_until"`
	Models                            string  `gorm:"column:models"`
	Group                             string  `gorm:"column:group"`
	UsedQuota                         int64   `gorm:"column:used_quota"`
	ModelMapping                      *string `gorm:"column:model_mapping"`
	Priority                          *int64  `gorm:"column:priority"`
	Config                            string  `gorm:"column:config"`
	SystemPrompt                      *string `gorm:"column:system_prompt"`
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

func (r *Repository) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if quota <= 0 {
		return nil
	}
	if r.db != nil {
		return r.recordUsageDB(ctx, channelID, quota)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	channel, ok := r.channels[channelID]
	if !ok {
		return biz.ErrChannelNotFound
	}
	channel.UsedQuota += quota
	return nil
}

func (r *Repository) RecordHealth(ctx context.Context, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*biz.Channel, error) {
	if r.db != nil {
		return r.recordHealthDB(ctx, event, threshold, cooldown)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	channel, ok := r.channels[event.ChannelID]
	if !ok {
		return nil, biz.ErrChannelNotFound
	}
	applyHealthEvent(channel, event, threshold, cooldown)
	cloned := *channel
	cloned.Models = append([]string(nil), channel.Models...)
	return &cloned, nil
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

func (r *Repository) recordUsageDB(ctx context.Context, channelID int64, quota int64) error {
	tx := r.db.WithContext(ctx).Model(&channelModel{}).
		Where("id = ?", channelID).
		UpdateColumn("used_quota", gorm.Expr("used_quota + ?", quota))
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return biz.ErrChannelNotFound
	}
	return nil
}

func (r *Repository) recordHealthDB(ctx context.Context, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*biz.Channel, error) {
	var updated *biz.Channel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model channelModel
		if err := tx.Where("id = ?", event.ChannelID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrChannelNotFound
			}
			return err
		}
		channel := r.modelToChannel(&model)
		applyHealthEvent(channel, event, threshold, cooldown)
		if err := tx.Model(&channelModel{}).Where("id = ?", event.ChannelID).Updates(map[string]interface{}{
			"test_time":                   channel.TestTime,
			"response_time":               channel.ResponseTime,
			"health_status":               channel.EffectiveHealthStatus(),
			"health_last_error":           stringPtr(channel.HealthLastError),
			"health_last_success_time":    channel.HealthLastSuccessTime,
			"health_last_failure_time":    channel.HealthLastFailureTime,
			"health_consecutive_failures": channel.HealthConsecutiveFailures,
			"circuit_opened_until":        channel.CircuitOpenedUntil,
		}).Error; err != nil {
			return err
		}
		updated = channel
		return nil
	})
	return updated, err
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
		ID:                                model.ID,
		Type:                              model.Type,
		Name:                              model.Name,
		Status:                            model.Status,
		BaseURL:                           baseURL,
		Group:                             model.Group,
		Models:                            biz.SplitCSV(model.Models),
		Priority:                          priority,
		Key:                               r.decryptKey(model.Key),
		Weight:                            derefUint(model.Weight),
		CreatedTime:                       model.CreatedTime,
		TestTime:                          model.TestTime,
		ResponseTime:                      model.ResponseTime,
		Balance:                           model.Balance,
		BalanceUpdatedTime:                model.BalanceUpdatedTime,
		BalanceRefreshLastError:           derefString(model.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     model.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: model.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      model.HealthStatus,
		HealthLastError:                   derefString(model.HealthLastError),
		HealthLastSuccessTime:             model.HealthLastSuccessTime,
		HealthLastFailureTime:             model.HealthLastFailureTime,
		HealthConsecutiveFailures:         model.HealthConsecutiveFailures,
		CircuitOpenedUntil:                model.CircuitOpenedUntil,
		UsedQuota:                         model.UsedQuota,
		ModelMapping:                      derefString(model.ModelMapping),
		SystemPrompt:                      derefString(model.SystemPrompt),
		Config:                            biz.DecodeChannelConfig(model.Config),
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
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := r.channelToModel(channel)
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		channel.ID = model.ID
		return r.syncAbilitiesTx(tx, channel)
	})
}

func (r *Repository) updateChannelDB(ctx context.Context, channel *biz.Channel) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := r.channelToModel(channel)
		if err := tx.Model(&channelModel{}).Where("id = ?", channel.ID).Updates(map[string]interface{}{
			"name":                                 model.Name,
			"base_url":                             model.BaseURL,
			"key":                                  model.Key,
			"models":                               model.Models,
			"group":                                model.Group,
			"priority":                             model.Priority,
			"weight":                               model.Weight,
			"model_mapping":                        model.ModelMapping,
			"system_prompt":                        model.SystemPrompt,
			"config":                               model.Config,
			"balance":                              model.Balance,
			"balance_updated_time":                 model.BalanceUpdatedTime,
			"balance_refresh_last_error":           model.BalanceRefreshLastError,
			"balance_refresh_last_success_time":    model.BalanceRefreshLastSuccessTime,
			"consecutive_balance_refresh_failures": model.ConsecutiveBalanceRefreshFailures,
			"health_status":                        model.HealthStatus,
			"health_last_error":                    model.HealthLastError,
			"health_last_success_time":             model.HealthLastSuccessTime,
			"health_last_failure_time":             model.HealthLastFailureTime,
			"health_consecutive_failures":          model.HealthConsecutiveFailures,
			"circuit_opened_until":                 model.CircuitOpenedUntil,
		}).Error; err != nil {
			return err
		}
		return r.syncAbilitiesTx(tx, channel)
	})
}

func (r *Repository) deleteChannelDB(ctx context.Context, channelID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", channelID).Delete(&channelModel{}).Error; err != nil {
			return err
		}
		return tx.Where("channel_id = ?", channelID).Delete(&abilityModel{}).Error
	})
}

func (r *Repository) changeStatusDB(ctx context.Context, channelID int64, status int32) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&channelModel{}).Where("id = ?", channelID).Update("status", status).Error; err != nil {
			return err
		}
		enabled := status == biz.ChannelStatusEnabled
		return tx.Model(&abilityModel{}).Where("channel_id = ?", channelID).Update("enabled", enabled).Error
	})
}

// syncAbilitiesTx rewrites ability rows for one channel: every old row for
// channel_id is deleted, then a fresh row is inserted for each (group, model)
// pair derived from the channel. Caller MUST pass an active gorm transaction.
func (r *Repository) syncAbilitiesTx(tx *gorm.DB, channel *biz.Channel) error {
	if err := tx.Where("channel_id = ?", channel.ID).Delete(&abilityModel{}).Error; err != nil {
		return err
	}
	enabled := channel.Status == biz.ChannelStatusEnabled
	priority := channel.Priority
	rows := make([]abilityModel, 0)
	for _, group := range biz.SplitCSV(channel.Group) {
		if group == "" {
			continue
		}
		for _, model := range channel.Models {
			if model == "" {
				continue
			}
			rows = append(rows, abilityModel{
				Group:     group,
				Model:     model,
				ChannelID: channel.ID,
				Enabled:   enabled,
				Priority:  &priority,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
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
		ID:                                m.ID,
		Type:                              m.Type,
		Name:                              m.Name,
		Status:                            m.Status,
		BaseURL:                           baseURL,
		Group:                             m.Group,
		Models:                            biz.SplitCSV(m.Models),
		Priority:                          priority,
		Key:                               r.decryptKey(m.Key),
		Weight:                            derefUint(m.Weight),
		CreatedTime:                       m.CreatedTime,
		TestTime:                          m.TestTime,
		ResponseTime:                      m.ResponseTime,
		Balance:                           m.Balance,
		BalanceUpdatedTime:                m.BalanceUpdatedTime,
		BalanceRefreshLastError:           derefString(m.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     m.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: m.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      m.HealthStatus,
		HealthLastError:                   derefString(m.HealthLastError),
		HealthLastSuccessTime:             m.HealthLastSuccessTime,
		HealthLastFailureTime:             m.HealthLastFailureTime,
		HealthConsecutiveFailures:         m.HealthConsecutiveFailures,
		CircuitOpenedUntil:                m.CircuitOpenedUntil,
		UsedQuota:                         m.UsedQuota,
		ModelMapping:                      derefString(m.ModelMapping),
		SystemPrompt:                      derefString(m.SystemPrompt),
		Config:                            biz.DecodeChannelConfig(m.Config),
	}
}

func (r *Repository) channelToModel(ch *biz.Channel) *channelModel {
	return &channelModel{
		ID:                                ch.ID,
		Type:                              ch.Type,
		Name:                              ch.Name,
		Status:                            ch.Status,
		BaseURL:                           strPtr(ch.BaseURL),
		Weight:                            uintPtr(ch.Weight),
		CreatedTime:                       ch.CreatedTime,
		TestTime:                          ch.TestTime,
		ResponseTime:                      ch.ResponseTime,
		Balance:                           ch.Balance,
		BalanceUpdatedTime:                ch.BalanceUpdatedTime,
		BalanceRefreshLastError:           stringPtr(ch.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     ch.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: ch.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      ch.EffectiveHealthStatus(),
		HealthLastError:                   stringPtr(ch.HealthLastError),
		HealthLastSuccessTime:             ch.HealthLastSuccessTime,
		HealthLastFailureTime:             ch.HealthLastFailureTime,
		HealthConsecutiveFailures:         ch.HealthConsecutiveFailures,
		CircuitOpenedUntil:                ch.CircuitOpenedUntil,
		Models:                            ch.ModelsCSV(),
		Group:                             ch.Group,
		UsedQuota:                         ch.UsedQuota,
		ModelMapping:                      stringPtr(ch.ModelMapping),
		Priority:                          int64Ptr(ch.Priority),
		Key:                               r.encryptKey(ch.Key),
		Config:                            "{}",
		SystemPrompt:                      stringPtr(ch.SystemPrompt),
	}
}

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }
func uintPtr(i uint32) *uint {
	v := uint(i)
	return &v
}
func stringPtr(s string) *string { return &s }
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func derefUint(u *uint) uint32 {
	if u == nil {
		return 0
	}
	v, err := safecast.UintToUint32(*u)
	if err != nil {
		return 0
	}
	return v
}

func applyHealthEvent(channel *biz.Channel, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) {
	if event.CheckedAt.IsZero() {
		event.CheckedAt = time.Now()
	}
	checkedAt := event.CheckedAt.Unix()
	channel.TestTime = checkedAt
	channel.ResponseTime = event.ResponseTime
	if event.Success {
		channel.HealthStatus = biz.ChannelHealthHealthy
		channel.HealthLastError = ""
		channel.HealthLastSuccessTime = checkedAt
		channel.HealthConsecutiveFailures = 0
		channel.CircuitOpenedUntil = 0
		return
	}
	channel.HealthLastError = event.Error
	channel.HealthLastFailureTime = checkedAt
	channel.HealthConsecutiveFailures++
	if threshold <= 0 {
		threshold = 1
	}
	if channel.HealthConsecutiveFailures >= threshold {
		channel.HealthStatus = biz.ChannelHealthUnavailable
		if cooldown > 0 {
			channel.CircuitOpenedUntil = event.CheckedAt.Add(cooldown).Unix()
		}
		return
	}
	channel.HealthStatus = biz.ChannelHealthDegraded
	channel.CircuitOpenedUntil = 0
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
