package biz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// NormalizeModelID returns the canonical form of a model identifier used for
// case-insensitive comparison and deduplication. Model names from upstream
// providers may arrive in different cases (e.g. "GLM-5.2" vs "glm-5.2") but
// refer to the same model; normalising to lowercase ensures they are treated
// as identical throughout the system.
func NormalizeModelID(modelID string) string {
	return strings.ToLower(strings.TrimSpace(modelID))
}

// ModelIDEqual reports whether two model identifiers refer to the same model,
// ignoring case and surrounding whitespace.
func ModelIDEqual(a, b string) bool {
	return NormalizeModelID(a) == NormalizeModelID(b)
}

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
	// EnabledHasValue distinguishes "caller left enabled unchanged on update"
	// (false) from "caller explicitly set enabled=false" (true). When false,
	// UpsertChannelMapping keeps the existing row's enabled value on update
	// and defaults to true on insert (DB DEFAULT 1). When true, Enabled is
	// the authoritative value. This fixes the proto3 bare-bool trap where a
	// priority-only update silently disabled the mapping.
	Enabled         bool
	EnabledHasValue bool
	Priority        int32
	Config          string
	UpstreamModelID string
	CreatedAt       int64
	UpdatedAt       int64
}

// ModelSubscriptionMapping links a subscription account to a model.
type ModelSubscriptionMapping struct {
	ID                    int64
	SubscriptionAccountID int64
	ModelPK               int64
	GroupName             string
	Enabled               bool
	EnabledHasValue       bool
	Priority              int32
	UpstreamModelID       string
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

	// Sprint 4: usage statistics
	RecordModelUsage(ctx context.Context, modelPK int64, stat *ModelUsageStat) error
	ListModelUsageStats(ctx context.Context, modelPK int64, startDate, endDate string, page, pageSize int32) ([]*ModelUsageStat, int64, error)

	// v0.11.0 Phase 2 §2.1: canonical model ID governance.
	// CanonicalModelPreflight is read-only and safe to run any time.
	// MergeCanonicalModels runs in one transaction and returns
	// ErrCanonicalConflict without partial writes when re-pointing would
	// collide on the survivor's unique keys.
	CanonicalModelPreflight(ctx context.Context) (*PreflightReport, error)
	MergeCanonicalModels(ctx context.Context, group DuplicateModelGroup) (*MergeResult, error)
}

// ModelsListCacheInvalidator is the optional seam ModelUsecase uses to drop
// the ChannelUsecase /v1/models L1 cache after a registry/mapping mutation.
// The two usecases are siblings in the biz package; ModelUsecase does NOT
// import ChannelUsecase — it only depends on this narrow interface, wired at
// composition time (see app/channel/cmd/channel/wire.go). Without it, admin
// edits to the model registry or its channel/subscription mappings stay stale
// for up to the 15s L1 TTL — the "改了不生效" gap. See
// docs/design/model-management-code-review.md.
type ModelsListCacheInvalidator interface {
	// invalidateModelsListCache drops the /v1/models L1 cache. Unexported
	// because both ModelUsecase and ChannelUsecase live in package biz; the
	// seam stays package-private so it cannot leak to other layers.
	invalidateModelsListCache()
}

// ModelUsecase wraps ModelRepo with domain-level operations.
type ModelUsecase struct {
	repo             ModelRepo
	cacheInvalidator ModelsListCacheInvalidator
	now              func() time.Time
}

// NewModelUsecase creates a new ModelUsecase.
func NewModelUsecase(repo ModelRepo) *ModelUsecase {
	return &ModelUsecase{repo: repo, now: time.Now}
}

// SetCacheInvalidator wires the optional ChannelUsecase cache invalidator.
// nil (the default) is a no-op, so deployments/tests that don't wire it keep
// working — mutations just rely on the L1 TTL to converge.
func (uc *ModelUsecase) SetCacheInvalidator(inv ModelsListCacheInvalidator) {
	if uc == nil {
		return
	}
	uc.cacheInvalidator = inv
}

// invalidateChannelCache drops the /v1/models L1 cache after a successful
// registry/mapping mutation so ListAvailableModels refetches immediately.
func (uc *ModelUsecase) invalidateChannelCache() {
	if uc == nil || uc.cacheInvalidator == nil {
		return
	}
	uc.cacheInvalidator.invalidateModelsListCache()
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
	return uc.repo.GetModelByID(ctx, NormalizeModelID(modelID))
}

// CreateModel creates a new model record.
func (uc *ModelUsecase) CreateModel(ctx context.Context, model *Model) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if model.ModelID == "" {
		return fmt.Errorf("model_id is required")
	}
	// Normalise the model_id to lowercase so that "GLM-5.2" and "glm-5.2"
	// are treated as the same model throughout the system.
	model.ModelID = NormalizeModelID(model.ModelID)
	if model.DisplayName == "" {
		model.DisplayName = model.ModelID
	}
	if model.ModelType == "" {
		model.ModelType = "chat"
	}
	now := uc.timestamp()
	if model.CreatedAt == 0 {
		model.CreatedAt = now
	}
	model.UpdatedAt = now
	if err := uc.repo.CreateModel(ctx, model); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
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
	if err := uc.repo.UpdateModel(ctx, model); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
}

// DeleteModel removes a model and its mappings.
func (uc *ModelUsecase) DeleteModel(ctx context.Context, modelPK int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if err := uc.repo.DeleteModel(ctx, modelPK); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
}

// ChangeModelStatus sets the status of a single model.
func (uc *ModelUsecase) ChangeModelStatus(ctx context.Context, modelPK int64, status int32) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if err := uc.repo.ChangeModelStatus(ctx, modelPK, status); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
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
		n, err := uc.repo.BatchChangeStatus(ctx, modelPKs, ModelStatusEnabled)
		if err == nil {
			uc.invalidateChannelCache()
		}
		return n, err
	case BatchActionDisable:
		n, err := uc.repo.BatchChangeStatus(ctx, modelPKs, ModelStatusDisabled)
		if err == nil {
			uc.invalidateChannelCache()
		}
		return n, err
	case BatchActionDelete:
		n, err := uc.repo.BatchDelete(ctx, modelPKs)
		if err == nil {
			uc.invalidateChannelCache()
		}
		return n, err
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
	alias.Alias = NormalizeModelID(alias.Alias)
	if alias.CreatedAt == 0 {
		alias.CreatedAt = uc.timestamp()
	}
	if err := uc.repo.CreateModelAlias(ctx, alias); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
}

// DeleteModelAlias removes an alias by id.
func (uc *ModelUsecase) DeleteModelAlias(ctx context.Context, aliasID int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if err := uc.repo.DeleteModelAlias(ctx, aliasID); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
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
	if err := uc.repo.UpsertChannelMapping(ctx, m); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
}

// DeleteChannelMapping removes a channel-model mapping.
func (uc *ModelUsecase) DeleteChannelMapping(ctx context.Context, channelID, modelPK int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if err := uc.repo.DeleteChannelMapping(ctx, channelID, modelPK); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
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
	if err := uc.repo.UpsertSubscriptionMapping(ctx, m); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
}

// DeleteSubscriptionMapping removes a subscription-model mapping.
func (uc *ModelUsecase) DeleteSubscriptionMapping(ctx context.Context, accountID, modelPK int64, groupName string) error {
	if uc == nil || uc.repo == nil {
		return ErrModelNotFound
	}
	if err := uc.repo.DeleteSubscriptionMapping(ctx, accountID, modelPK, groupName); err != nil {
		return err
	}
	uc.invalidateChannelCache()
	return nil
}

// ── Sprint 4: Usage statistics ─────────────────────────────────────────────

// RecordModelUsage records a usage event for a model identified by its model_id
// string. The model_id is normalised to lowercase before lookup. If the model
// is not found, the event is silently dropped (best-effort: usage recording
// must never fail the request path).
func (uc *ModelUsecase) RecordModelUsage(ctx context.Context, modelID string, requestCount int32, tokenCount int64, errorCount int32, avgLatency int32, date string) error {
	if uc == nil || uc.repo == nil {
		return nil
	}
	if modelID == "" {
		return nil
	}
	model, err := uc.repo.GetModelByID(ctx, NormalizeModelID(modelID))
	if err != nil {
		return nil // best-effort: model not registered, skip
	}
	if date == "" {
		date = uc.now().Format("2006-01-02")
	}
	stat := &ModelUsageStat{
		ModelPK:      model.ID,
		Date:         date,
		RequestCount: requestCount,
		TokenCount:   tokenCount,
		ErrorCount:   errorCount,
		AvgLatency:   avgLatency,
	}
	return uc.repo.RecordModelUsage(ctx, model.ID, stat)
}

// ListModelUsageStats returns paginated usage statistics for a model (or all
// models when modelPK is 0) within an optional date range.
func (uc *ModelUsecase) ListModelUsageStats(ctx context.Context, modelPK int64, startDate, endDate string, page, pageSize int32) ([]*ModelUsageStat, int64, error) {
	if uc == nil || uc.repo == nil {
		return nil, 0, nil
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	return uc.repo.ListModelUsageStats(ctx, modelPK, startDate, endDate, page, pageSize)
}

// ── v0.11.0 Phase 2 §2.1: canonical model ID governance ────────────────────
//
// The public model_id MUST be unique after NormalizeModelID (trim+lowercase).
// Legacy data may contain case-only duplicates (e.g. "GLM-5.2" and "glm-5.2")
// that the original case-sensitive unique key allowed through. Phase 2
// introduces a read-only preflight report, a transactional merge that
// re-points every foreign key and statistic before deleting the loser row,
// and a database-level canonical unique constraint applied only after the
// merge succeeds. See docs/design/v0.11.0-roadmap.md §2.1.

// DuplicateModelRef counts how many dependent rows point at each duplicate
// model row, so the operator can pick a safe merge target and review the
// blast radius before any write.
type DuplicateModelRef struct {
	ModelPK   int64
	ModelID   string // original (pre-normalisation) spelling as stored
	IsPrimary bool   // true = this row already carries the canonical spelling
	Aliases             int32
	ChannelMappings     int32
	SubscriptionMappings int32
	UsageStatDays       int32
	UsageRequestTotal   int64
	UsageTokenTotal     int64
	// PriceReferences lists the pricing-config keys (ModelPrice /
	// UpstreamModelPrice) that reference this member's stored spelling. Populated
	// by AttachPriceReferences from a caller-supplied key set so channel biz does
	// not import billing/admin pricing storage. Empty when the caller did not
	// supply pricing keys.
	PriceReferences []string
}

// DuplicateModelGroup is one canonical-id collision: every member normalises
// to the same CanonicalID but occupies a distinct models.id. The operator
// designates one SurvivingPK (defaults to the member whose stored model_id
// already matches the canonical spelling) and the merge re-points all other
// members' dependents onto it.
type DuplicateModelGroup struct {
	CanonicalID  string
	Members      []DuplicateModelRef
	SurvivingPK  int64 // 0 => biz picks the primary-spelling member (or lowest id)
}

// PreflightReport is the read-only output of CanonicalModelPreflight. It
// lists every canonical collision plus the number of dependent rows hanging
// off each duplicate. Conflicts blocks the merge; Merge runs only when
// Conflicts is empty. PriceKeyCount is populated by AttachPriceReferences from
// a caller-supplied pricing-config key set so the operator can see which
// ModelPrice/UpstreamModelPrice entries reference the duplicate ids.
type PreflightReport struct {
	Groups    []DuplicateModelGroup
	Conflicts []string // human-readable blockers (e.g. price-key already canonical)
}

// AttachPriceReferences annotates each duplicate member with the pricing-config
// keys (ModelPrice / UpstreamModelPrice) that reference its stored spelling.
// priceKeys is the full set of keys from both config maps (already loaded by
// the caller, e.g. admin-api which owns system_options). The function is pure:
// channel biz stays decoupled from billing/admin pricing storage. After this
// call, each member whose stored ModelID appears as a pricing key (after
// canonicalisation) carries it in PriceReferences.
func (r *PreflightReport) AttachPriceReferences(priceKeys map[string]struct{}) {
	if r == nil || len(priceKeys) == 0 {
		return
	}
	// Index pricing keys by their canonical form so a stored "GLM-5.2" matches
	// a pricing key "glm-5.2".
	canonicalToOriginal := make(map[string][]string, len(priceKeys))
	for k := range priceKeys {
		ck := NormalizeModelID(k)
		canonicalToOriginal[ck] = append(canonicalToOriginal[ck], k)
	}
	for i := range r.Groups {
		for j := range r.Groups[i].Members {
			m := &r.Groups[i].Members[j]
			// The member's stored spelling may be non-canonical; index both
			// the stored spelling and its canonical form so a stored "GLM-5.2"
			// matches a pricing key "GLM-5.2" (exact) and "glm-5.2" (canonical).
			refs := append([]string{}, canonicalToOriginal[m.ModelID]...)
			refs = append(refs, canonicalToOriginal[NormalizeModelID(m.ModelID)]...)
			// Also index the canonical id of the whole group, in case the
			// member's stored spelling is an unrelated variant that still
			// normalises to the group's canonical id.
			refs = append(refs, canonicalToOriginal[r.Groups[i].CanonicalID]...)
			if len(refs) > 0 {
				// Deduplicate into a FRESH slice (refs[:0] aliases refs and
				// corrupts the range above when duplicates are dropped).
				seen := map[string]struct{}{}
				out := make([]string, 0, len(refs))
				for _, ref := range refs {
					if _, ok := seen[ref]; ok {
						continue
					}
					seen[ref] = struct{}{}
					out = append(out, ref)
				}
				m.PriceReferences = out
			}
		}
	}
}

// MergeResult summarises a completed (or attempted) merge transaction.
type MergeResult struct {
	CanonicalID    string
	SurvivingPK    int64
	MergedModelPKs []int64
	// Counts of rows re-pointed onto the survivor, for the audit log.
	AliasesRepointed              int32
	ChannelMappingsRepointed      int32
	SubscriptionMappingsRepointed int32
	UsageStatsRepointed           int32
}

// ErrCanonicalConflict signals that a merge cannot proceed without operator
// intervention (e.g. two members carry conflicting channel mappings). The
// merge transaction is rolled back; no partial writes remain.
var ErrCanonicalConflict = errors.New("canonical model id conflict")

// CanonicalModelPreflight scans the model registry and reports every set of
// rows whose model_id collides after NormalizeModelID, together with the
// dependent-row counts the operator needs to assess the merge. It performs
// NO writes and is safe to run at any time. Returns an empty report when the
// registry is already canonical-clean.
func (uc *ModelUsecase) CanonicalModelPreflight(ctx context.Context) (*PreflightReport, error) {
	if uc == nil || uc.repo == nil {
		return &PreflightReport{}, nil
	}
	return uc.repo.CanonicalModelPreflight(ctx)
}

// MergeCanonicalModels merges a single duplicate group onto SurvivingPK (or
// the primary-spelling member when SurvivingPK is 0). The whole operation
// runs in one transaction: every alias / channel mapping / subscription
// mapping / usage-stat row pointing at a loser is re-pointed to the survivor,
// then the loser rows are deleted. If re-pointing would create a duplicate
// unique key on the survivor (a real conflict, not just a case duplicate),
// the transaction is rolled back and ErrCanonicalConflict is returned — no
// INSERT-IGNORE style silent overwrite.
func (uc *ModelUsecase) MergeCanonicalModels(ctx context.Context, group DuplicateModelGroup) (*MergeResult, error) {
	if uc == nil || uc.repo == nil {
		return nil, ErrModelNotFound
	}
	if len(group.Members) < 2 {
		return nil, fmt.Errorf("merge requires at least two members")
	}
	if group.CanonicalID == "" {
		return nil, fmt.Errorf("canonical_id is required")
	}
	// Reject a caller-supplied canonical_id that does not match the
	// normalisation of its own members — the operator must not be able to
	// rename a model mid-merge.
	want := NormalizeModelID(group.CanonicalID)
	if want != group.CanonicalID {
		return nil, fmt.Errorf("canonical_id must be pre-normalised (got %q, want %q)", group.CanonicalID, want)
	}
	// Default survivor: the member already carrying the canonical spelling;
	// fall back to the lowest PK for determinism.
	if group.SurvivingPK == 0 {
		for _, m := range group.Members {
			if m.IsPrimary {
				group.SurvivingPK = m.ModelPK
				break
			}
		}
	}
	if group.SurvivingPK == 0 {
		min := group.Members[0].ModelPK
		for _, m := range group.Members {
			if m.ModelPK < min {
				min = m.ModelPK
			}
		}
		group.SurvivingPK = min
	}
	res, err := uc.repo.MergeCanonicalModels(ctx, group)
	if err != nil {
		return nil, err
	}
	uc.invalidateChannelCache()
	return res, nil
}

// ── v0.11.0 Phase 2 §2.2: unpriced-model audit ─────────────────────────────
//
// A model is "routed but unpriced" when it is public, enabled, has at least
// one enabled channel or subscription mapping, but carries no entry in the
// user-facing ModelPrice config. Unpriced does NOT block routing — the
// request still succeeds and is billed via the legacy ratio path — but it
// must surface as a visible status so operators do not silently serve a
// model at zero cost. See docs/design/v0.11.0-roadmap.md §2.2.

// RoutedModelSummary is the per-model slice returned by UnpricedRoutedModels.
type RoutedModelSummary struct {
	ModelID           string
	DisplayName       string
	Provider          string
	ChannelCount      int32
	SubscriptionCount int32
}

// UnpricedRoutedModels returns the subset of `models` that are public and
// enabled, have at least one enabled channel OR subscription mapping, and are
// NOT present in `pricedModelIDs` (the keys of the user-facing ModelPrice
// config, already canonicalised). The result is sorted by model_id for stable
// output. This is a pure function: callers fetch the model list and the
// priced set from their respective stores and pass them in, so the audit
// logic stays out of the storage layer.
//
// pricedModelIDs must already be canonicalised (NormalizeModelID); this
// function does not re-normalise so it can be used with config loaded from a
// store that is already lowercase.
func UnpricedRoutedModels(models []*Model, pricedModelIDs map[string]struct{}) []RoutedModelSummary {
	if len(models) == 0 {
		return nil
	}
	out := make([]RoutedModelSummary, 0)
	for _, m := range models {
		if m == nil {
			continue
		}
		// Only public + enabled models are billable routes. Private/testing
		// discoveries are intentionally excluded — they are not user-facing.
		if !m.IsPublic || m.Status != ModelStatusEnabled {
			continue
		}
		// Must have at least one active upstream to be "routed".
		if m.ChannelCount == 0 && m.SubscriptionCount == 0 {
			continue
		}
		// Canonical lookup against the priced set.
		if _, priced := pricedModelIDs[NormalizeModelID(m.ModelID)]; priced {
			continue
		}
		out = append(out, RoutedModelSummary{
			ModelID:           m.ModelID,
			DisplayName:       m.DisplayName,
			Provider:          m.Provider,
			ChannelCount:      m.ChannelCount,
			SubscriptionCount: m.SubscriptionCount,
		})
	}
	// Deterministic order for stable UI/audit output.
	sort.Slice(out, func(i, j int) bool { return out[i].ModelID < out[j].ModelID })
	return out
}

// ListUnpricedRoutedModels computes the v0.11.0 Phase 2 §2.2 unpriced audit:
// public, enabled models that have at least one active channel or subscription
// mapping but are NOT in pricedModelIDs. pricedModelIDs must already be
// canonicalised. This is a read-only query; it never blocks a price save.
func (uc *ModelUsecase) ListUnpricedRoutedModels(ctx context.Context, pricedModelIDs map[string]struct{}) ([]RoutedModelSummary, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil
	}
	// Page through the whole registry. The model count is bounded (hundreds,
	// not millions), so a single large page is simpler than cursoring and
	// keeps the audit atomic with respect to concurrent edits.
	page := int32(1)
	pageSize := int32(500)
	priced := pricedModelIDs
	if priced == nil {
		priced = map[string]struct{}{}
	}
	var collected []*Model
	for {
		models, total, err := uc.repo.ListModels(ctx, page, pageSize, ListModelsFilter{})
		if err != nil {
			return nil, err
		}
		collected = append(collected, models...)
		if int64(len(collected)) >= total || len(models) == 0 {
			break
		}
		page++
	}
	return UnpricedRoutedModels(collected, priced), nil
}
