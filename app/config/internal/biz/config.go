package biz

import (
	"context"
	"errors"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
	"strings"
	"time"
)

var (
	ErrConfigNotFound         = errors.New("config not found")
	ErrConfigExists           = errors.New("config already exists")
	ErrInvalidKey             = errors.New("invalid config key")
	ErrConfigRevisionConflict = errors.New("config revision conflict")
	ErrConfigMutationRequired = errors.New("config expected_revision and reason required")
)

// ConfigEntry represents a dynamic configuration entry.
type ConfigEntry struct {
	ID        int64
	Revision  int64
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
	authorization authorization.Resolver
	repo          ConfigRepo
}

func NewConfigUsecase(repo ConfigRepo) *ConfigUsecase {
	return &ConfigUsecase{repo: repo}
}

func (uc *ConfigUsecase) GetConfig(ctx context.Context, namespace, key string) (*ConfigEntry, error) {
	var err error
	ctx, err = uc.authorize(ctx, "system.options.read", "system.option.read")
	if err != nil {
		return nil, err
	}
	if err := authorization.Require(ctx, "system.option.read", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, ErrInvalidKey
	}
	entry, err := uc.repo.Get(ctx, namespace, key)
	if authorization.External(ctx) && !serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		entry = configView(entry)
	}
	return entry, err
}

func (uc *ConfigUsecase) ListConfigs(ctx context.Context, namespace string, page, pageSize int32) ([]*ConfigEntry, int64, error) {
	var err error
	ctx, err = uc.authorize(ctx, "system.options.read", "system.option.read")
	if err != nil {
		return nil, 0, err
	}
	if err := authorization.Require(ctx, "system.option.read", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	entries, total, err := uc.repo.List(ctx, namespace, page, pageSize)
	if authorization.External(ctx) && !serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		for i, e := range entries {
			entries[i] = configView(e)
		}
	}
	return entries, total, err
}

func (uc *ConfigUsecase) SetConfig(ctx context.Context, namespace, key, value, comment string) error {
	_, err := uc.setConfig(ctx, namespace, key, value, comment)
	return err
}
func (uc *ConfigUsecase) setConfig(ctx context.Context, namespace, key, value, comment string) (*ConfigEntry, error) {
	op := ConfigWriteOperation(namespace, key)
	point := "system.options.update"
	if strings.HasPrefix(op, "system.content.") {
		point = "system.content"
	}
	var err error
	if strings.HasPrefix(op, "system.option.") && op != "system.option.update" {
		ctx, err = uc.authorize(ctx, "system.options.update", "system.option.update")
		if err != nil {
			return nil, err
		}
		if err = authorization.Require(ctx, "system.option.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
			return nil, err
		}
	}
	ctx, err = uc.authorize(ctx, point, op)
	if err != nil {
		return nil, err
	}
	if err := authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, ErrInvalidKey
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, op); iam {
		if _, ok := authorization.ExpectedResourceRevision(ctx); !ok || strings.TrimSpace(authorization.WriteReason(ctx)) == "" {
			return nil, ErrConfigMutationRequired
		}
	}
	entry := &ConfigEntry{
		Namespace: namespace,
		Key:       key,
		Value:     value,
		Comment:   comment,
		UpdatedAt: time.Now(),
	}
	if err := uc.repo.Set(ctx, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

func (uc *ConfigUsecase) DeleteConfig(ctx context.Context, namespace, key string) error {
	op := ConfigWriteOperation(namespace, key)
	point := "system.options.update"
	if strings.HasPrefix(op, "system.content.") {
		point = "system.content"
	}
	var err error
	if strings.HasPrefix(op, "system.option.") && op != "system.option.update" {
		ctx, err = uc.authorize(ctx, "system.options.update", "system.option.update")
		if err != nil {
			return err
		}
		if err = authorization.Require(ctx, "system.option.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
			return err
		}
	}
	ctx, err = uc.authorize(ctx, point, op)
	if err != nil {
		return err
	}
	if err := authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return err
	}
	if key == "" {
		return ErrInvalidKey
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, op); iam {
		if _, ok := authorization.ExpectedResourceRevision(ctx); !ok || strings.TrimSpace(authorization.WriteReason(ctx)) == "" {
			return ErrConfigMutationRequired
		}
	}
	if err := uc.repo.Delete(ctx, namespace, key); err != nil {
		return err
	}
	return nil
}

// PublicContent exposes only the three fixed public presentation values.
// It deliberately does not reuse the privileged generic configuration read.
func (uc *ConfigUsecase) PublicContent(ctx context.Context, key string) (*ConfigEntry, error) {
	switch key {
	case "notice", "about", "home_page_content":
		return uc.repo.Get(ctx, "system", key)
	}
	return nil, ErrInvalidKey
}

func (uc *ConfigUsecase) SetConfigEntry(ctx context.Context, namespace, key, value, comment string) (*ConfigEntry, error) {
	return uc.setConfig(ctx, namespace, key, value, comment)
}
