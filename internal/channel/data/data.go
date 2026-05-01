package data

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"

	"micro-one-api/internal/channel/biz"
	"micro-one-api/internal/pkg/xdb"

	"gorm.io/gorm"
)

type Repository struct {
	db       *gorm.DB
	channels map[int64]*biz.Channel
	lock     sync.RWMutex
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

func NewRepositoryFromEnv() (*Repository, error) {
	dsn := os.Getenv("CHANNEL_SQL_DSN")
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
		channels: map[int64]*biz.Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "openai-primary",
				Status:   biz.ChannelStatusEnabled,
				BaseURL:  "https://api.openai.com/v1",
				Group:    "default",
				Models:   []string{"gpt-4o-mini", "gpt-4.1"},
				Priority: 10,
				Key:      "upstream-openai-key",
			},
			2: {
				ID:       2,
				Type:     2,
				Name:     "anthropic-backup",
				Status:   biz.ChannelStatusEnabled,
				BaseURL:  "https://api.anthropic.com",
				Group:    "default",
				Models:   []string{"claude-3-5-sonnet"},
				Priority: 5,
				Key:      "upstream-anthropic-key",
			},
		},
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
		Key:      model.Key,
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
