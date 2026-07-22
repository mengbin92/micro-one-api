package biz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Model status constants mirroring the `models.status` column.
const (
	ModelStatusDisabled = 0
	ModelStatusEnabled  = 1
	ModelStatusTesting  = 2
)

// Batch actions accepted by ModelUsecase.BatchModels.
const (
	BatchActionEnable  = "enable"
	BatchActionDisable = "disable"
	BatchActionDelete  = "delete"
)

// Model is the domain object for the independent model registry (方案B).
// It carries no proto or storage tags — it is the pure biz model owned by biz.
type Model struct {
	ID            int64
	ModelID       string // unique identifier, e.g. gpt-4o
	DisplayName   string
	Description   string
	Provider      string
	ModelType     string
	ContextWindow int32
	PricingInput  float64
	PricingOutput float64
	Status        int32
	IsPublic      bool
	Capabilities  []string
	Tags          []string
	Category      string
	Tier          string
	Metadata      string
	CreatedAt     int64
	UpdatedAt     int64

	// Aggregated counts populated by list/detail queries; not persisted columns.
	ChannelCount      int32
	SubscriptionCount int32
}

// ModelAlias is an alternative name that resolves to a model.
type ModelAlias struct {
	ID        int64
	ModelPK   int64
	Alias     string
	IsPrimary bool
	CreatedAt int64
}

// ModelChannelMapping links a channel to a model with per-combo config.
type ModelChannelMapping struct {
	ID        int64
	ChannelID int64
	ModelPK   int64
	Enabled   bool
	Priority  int32
	Config    string
	CreatedAt int64
	UpdatedAt int64
}

// ModelSubscriptionMapping links a subscription account to a model.
type ModelSubscriptionMapping struct {
	ID                    int64
	SubscriptionAccountID int64
	ModelPK               int64
	GroupName             string
	Enabled               bool
	Priority              int32
	CreatedAt             int64
	UpdatedAt             int64
}

// ModelUsageStat is a daily usage aggregation for a model.
type ModelUsageStat struct {
	ID           int64
	ModelPK      int64
	Date         string // YYYY-MM-DD
	RequestCount int32
	TokenCount   int64
	ErrorCount   int32
	AvgLatency   int32
}

// ListModelsFilter holds the optional filters for listing models.
type ListModelsFilter struct {
	Keyword    string
	Provider   string
	ModelType  string
	Status     int32
	Category   string
	Tier       string
	PublicOnly bool
}

// Typed errors. The data layer maps driver errors onto these so callers
// above never branch on the driver.
var (
	ErrModelNotFound      = errors.New("model not found")
	ErrModelIDExists      = errors.New("model_id already exists")
	ErrAliasExists        = errors.New("model alias already exists")
	ErrAliasNotFound      = errors.New("model alias not found")
	ErrMappingNotFound    = errors.New("model mapping not found")
	ErrInvalidBatchAction = errors.New("invalid batch action")
)

// ModelRepo is the repository interface for the model registry, declared in
// biz (the inversion seam) and implemented by data. It is separate from
// ChannelRepo so the model domain can evolve independently.
type ModelRepo interface {
	ListModels(ctx context.Context, page, pageSize int32, filter ListModelsFilter) ([]*Model, int64, error)
	GetModel(ctx context.Context, modelPK int64) (*Model, error)
	GetModelByID(ctx context.Context, modelID string) (*Model, error)
	CreateModel(ctx context.Context, model *Model) error
	UpdateModel(ctx context.Context, model *Model) error
	DeleteModel(ctx context.Context, modelPK int64) error
	ChangeModelStatus(ctx context.Context, modelPK int64, status int32) error
	BatchChangeStatus(ctx context.Context, modelPKs []int64, status int32) (int32, error)
	BatchDelete(ctx context.Context, modelPKs []int64) (int32, error)

	ListModelAliases(ctx context.Context, modelPK int64) ([]*ModelAlias, error)
	CreateModelAlias(ctx context.Context, alias *ModelAlias) error
	DeleteModelAlias(ctx context.Context, aliasID int64) error

	ListChannelMappings(ctx context.Context, channelID int64) ([]*ModelChannelMapping, error)
	ListChannelMappingsByModel(ctx context.Context, modelPK int64) ([]*ModelChannelMapping, error)
	UpsertChannelMapping(ctx context.Context, m *ModelChannelMapping) error
	DeleteChannelMapping(ctx context.Context, channelID, modelPK int64) error

	ListSubscriptionMappings(ctx context.Context, accountID int64) ([]*ModelSubscriptionMapping, error)
	ListSubscriptionMappingsByModel(ctx context.Context, modelPK int64) ([]*ModelSubscriptionMapping, error)
	UpsertSubscriptionMapping(ctx context.Context, m *ModelSubscriptionMapping) error
	DeleteSubscriptionMapping(ctx context.Context, accountID, modelPK int64, groupName string) error
}

// ModelUsecase wraps ModelRepo with domain-level operations.
type ModelUsecase struct {
	repo ModelRepo
	now  func() time.Time
}

// NewModelUsecase creates a new ModelUsecase.
func NewModelUsecase(repo ModelRepo) *ModelUsecase {
	return &ModelUsecase{repo: repo, now: time.Now}
}

// Repo exposes the underlying repo (used by service for nil-safety checks).
func (uc *ModelUsecase) Repo() ModelRepo {
	if uc == nil {
		return nil
	}
	return uc.repo
}

func (uc *ModelUsecase) timestamp() int64 {
	if uc == nil || uc.now == nil {
		return time.Now().Unix()
	}
	return uc.now().Unix()
}

// ListModels returns a page of models matching the filter.
func (uc *ModelUsecase) ListModels(ctx context.Context, page, pageSize int32, filter ListModelsFilter) ([]*Model, int64, error) {
	if uc == nil || uc.repo == nil {
		return nil, 0, nil
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	return uc.repo.ListModels(ctx, page, pageSize, filter)
}

// GetModel returns a model by primary key, including its aliases and
// channel/subscription mappings.
func (uc *ModelUsecase) GetModel(ctx context.Context, modelPK int64) (*Model, []*ModelAlias, []*ModelChannelMapping, []*ModelSubscriptionMapping, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil, nil, nil, ErrModelNotFound
	}
	model, err := uc.repo.GetModel(ctx, modelPK)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	aliases, err := uc.repo.ListModelAliases(ctx, modelPK)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	chMappings, err := uc.repo.ListChannelMappingsByModel(ctx, modelPK)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	subMappings, err := uc.repo.ListSubscriptionMappingsByModel(ctx, modelPK)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return model, aliases, chMappings, subMappings, nil
}

// GetModelByID returns a model by its unique model_id string.
func (uc *ModelUsecase) GetModelByID(ctx context.Context, modelID string) (*Model, error) {
	if uc == nil || uc.repo == nil {
		return nil, ErrModelNotFound
	}
	return uc.repo.GetModelByID(ctx, modelID)
}

// CreateModel creates a new model record.
func (uc *ModelUsecase) CreateModel(ctx context.Context, model *Model) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if model.ModelID == "" {
		return fmt.Errorf("model_id is required")
	}
	if model.DisplayName == "" {
		model.DisplayName = model.ModelID
	}
	if model.ModelType == "" {
		model.ModelType = "chat"
	}
	if model.Status == 0 {
		model.Status = ModelStatusEnabled
	}
	now := uc.timestamp()
	if model.CreatedAt == 0 {
		model.CreatedAt = now
	}
	model.UpdatedAt = now
	return uc.repo.CreateModel(ctx, model)
}

// UpdateModel updates an existing model. model.ID must be set.
func (uc *ModelUsecase) UpdateModel(ctx context.Context, model *Model) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if model.ID == 0 {
		return ErrModelNotFound
	}
	model.UpdatedAt = uc.timestamp()
	return uc.repo.UpdateModel(ctx, model)
}

// DeleteModel removes a model and its mappings.
func (uc *ModelUsecase) DeleteModel(ctx context.Context, modelPK int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	return uc.repo.DeleteModel(ctx, modelPK)
}

// ChangeModelStatus sets the status of a single model.
func (uc *ModelUsecase) ChangeModelStatus(ctx context.Context, modelPK int64, status int32) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	return uc.repo.ChangeModelStatus(ctx, modelPK, status)
}

// BatchModels performs a batch enable/disable/delete on the given model pks.
func (uc *ModelUsecase) BatchModels(ctx context.Context, action string, modelPKs []int64) (int32, error) {
	if uc == nil || uc.repo == nil {
		return 0, nil
	}
	if len(modelPKs) == 0 {
		return 0, nil
	}
	switch action {
	case BatchActionEnable:
		return uc.repo.BatchChangeStatus(ctx, modelPKs, ModelStatusEnabled)
	case BatchActionDisable:
		return uc.repo.BatchChangeStatus(ctx, modelPKs, ModelStatusDisabled)
	case BatchActionDelete:
		return uc.repo.BatchDelete(ctx, modelPKs)
	default:
		return 0, ErrInvalidBatchAction
	}
}

// ListModelAliases returns aliases for a model.
func (uc *ModelUsecase) ListModelAliases(ctx context.Context, modelPK int64) ([]*ModelAlias, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil
	}
	return uc.repo.ListModelAliases(ctx, modelPK)
}

// CreateModelAlias adds an alias to a model.
func (uc *ModelUsecase) CreateModelAlias(ctx context.Context, alias *ModelAlias) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if alias.Alias == "" {
		return fmt.Errorf("alias is required")
	}
	if alias.CreatedAt == 0 {
		alias.CreatedAt = uc.timestamp()
	}
	return uc.repo.CreateModelAlias(ctx, alias)
}

// DeleteModelAlias removes an alias by id.
func (uc *ModelUsecase) DeleteModelAlias(ctx context.Context, aliasID int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	return uc.repo.DeleteModelAlias(ctx, aliasID)
}

// ListChannelMappings returns channel-model mappings for a channel (or all
// when channelID is 0).
func (uc *ModelUsecase) ListChannelMappings(ctx context.Context, channelID int64) ([]*ModelChannelMapping, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil
	}
	return uc.repo.ListChannelMappings(ctx, channelID)
}

// UpsertChannelMapping creates or updates a channel-model mapping.
func (uc *ModelUsecase) UpsertChannelMapping(ctx context.Context, m *ModelChannelMapping) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	now := uc.timestamp()
	// CreatedAt is only set when zero (new record). The data layer's update
	// path ignores created_at entirely, so existing records keep their
	// original creation timestamp.
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	return uc.repo.UpsertChannelMapping(ctx, m)
}

// DeleteChannelMapping removes a channel-model mapping.
func (uc *ModelUsecase) DeleteChannelMapping(ctx context.Context, channelID, modelPK int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	return uc.repo.DeleteChannelMapping(ctx, channelID, modelPK)
}

// ListSubscriptionMappings returns subscription-model mappings for an
// account (or all when accountID is 0).
func (uc *ModelUsecase) ListSubscriptionMappings(ctx context.Context, accountID int64) ([]*ModelSubscriptionMapping, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil
	}
	return uc.repo.ListSubscriptionMappings(ctx, accountID)
}

// UpsertSubscriptionMapping creates or updates a subscription-model mapping.
func (uc *ModelUsecase) UpsertSubscriptionMapping(ctx context.Context, m *ModelSubscriptionMapping) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if m.GroupName == "" {
		m.GroupName = "default"
	}
	now := uc.timestamp()
	// CreatedAt is only set when zero (new record). The data layer's update
	// path ignores created_at entirely, so existing records keep their
	// original creation timestamp.
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	return uc.repo.UpsertSubscriptionMapping(ctx, m)
}

// DeleteSubscriptionMapping removes a subscription-model mapping.
func (uc *ModelUsecase) DeleteSubscriptionMapping(ctx context.Context, accountID, modelPK int64, groupName string) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	return uc.repo.DeleteSubscriptionMapping(ctx, accountID, modelPK, groupName)
}
