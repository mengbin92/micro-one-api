package biz

import (
	"context"
	"errors"
	"time"

	"micro-one-api/internal/pkg/events"
)

var (
	ErrConfigNotFound = errors.New("config not found")
	ErrConfigExists   = errors.New("config already exists")
	ErrInvalidKey     = errors.New("invalid config key")
)

// ConfigEntry represents a dynamic configuration entry.
type ConfigEntry struct {
	ID        int64
	Namespace string
	Key       string
	Value     string
	Comment   string
	UpdatedAt time.Time
}

// ConfigRepo is the repository interface for config persistence.
type ConfigRepo interface {
	Get(ctx context.Context, namespace, key string) (*ConfigEntry, error)
	List(ctx context.Context, namespace string, page, pageSize int32) ([]*ConfigEntry, int64, error)
	Set(ctx context.Context, entry *ConfigEntry) error
	Delete(ctx context.Context, namespace, key string) error
}

// ConfigUsecase implements business logic for config-service.
type ConfigUsecase struct {
	repo     ConfigRepo
	eventBus events.EventBus
}

func NewConfigUsecase(repo ConfigRepo, eventBus events.EventBus) *ConfigUsecase {
	if eventBus == nil {
		eventBus = events.NewMemoryEventBus()
	}
	return &ConfigUsecase{repo: repo, eventBus: eventBus}
}

func (uc *ConfigUsecase) GetConfig(ctx context.Context, namespace, key string) (*ConfigEntry, error) {
	if key == "" {
		return nil, ErrInvalidKey
	}
	return uc.repo.Get(ctx, namespace, key)
}

func (uc *ConfigUsecase) ListConfigs(ctx context.Context, namespace string, page, pageSize int32) ([]*ConfigEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return uc.repo.List(ctx, namespace, page, pageSize)
}

func (uc *ConfigUsecase) SetConfig(ctx context.Context, namespace, key, value, comment string) error {
	if key == "" {
		return ErrInvalidKey
	}
	entry := &ConfigEntry{
		Namespace: namespace,
		Key:       key,
		Value:     value,
		Comment:   comment,
		UpdatedAt: time.Now(),
	}
	if err := uc.repo.Set(ctx, entry); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicConfigChanged, entry)
	return nil
}

func (uc *ConfigUsecase) DeleteConfig(ctx context.Context, namespace, key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	if err := uc.repo.Delete(ctx, namespace, key); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicConfigChanged, &ConfigEntry{Namespace: namespace, Key: key})
	return nil
}
