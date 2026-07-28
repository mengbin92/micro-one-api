package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ── Model import/export (v0.11.0 Phase 4) ─────────────────────────────────
//
// The exchange payload is a versioned JSON document covering models, their
// aliases and their channel/subscription mappings. Prices are optional and
// gated behind an explicit flag plus the admin/root role check at admin-api.
// Export NEVER carries credentials (channel API keys, OAuth tokens) — only
// registry metadata and routing config.
//
// Layering: biz owns the exchange DOs and the repo interface (the inversion
// seam). data implements the bulk read/write. service is a DTO↔DO pass-through
// that validates limits and normalises canonical ids before delegating.

// ModelExchangeSchemaVersion is the version stamped on every export document.
// Import rejects a mismatched version rather than guessing the shape, so a
// future schema change is a coordinated upgrade, not a silent corruption.
const ModelExchangeSchemaVersion = "1.0.0"

// ModelExportModel is the domain shape of one exported model with its
// aliases and mappings. It mirrors the proto ModelExportModel but carries no
// proto tags — it is the pure biz model owned by biz.
type ModelExportModel struct {
	ModelID              string
	DisplayName          string
	Description          string
	Provider             string
	ModelType            string
	ContextWindow        int32
	PricingInput         float64 // zero when prices are not exported
	PricingOutput        float64
	Status               int32
	IsPublic             bool
	Capabilities         []string
	Tags                 []string
	Category             string
	Tier                 string
	Metadata             string
	Aliases              []*ModelAlias
	ChannelMappings      []*ModelChannelMapping
	SubscriptionMappings []*ModelSubscriptionMapping
}

// ModelExportResult is the output of ExportModels.
type ModelExportResult struct {
	SchemaVersion string
	ExportedAt    string // RFC3339
	Models        []*ModelExportModel
	ContentHash   string // SHA-256 of the canonical JSON of Models
}

// ImportConflictStrategy controls how an existing model with differing
// content is treated during import.
type ImportConflictStrategy string

const (
	ConflictStrategyReject ImportConflictStrategy = "reject" // default
	ConflictStrategyUpdate ImportConflictStrategy = "update"
)

// ImportOptions controls a single import invocation.
type ImportOptions struct {
	ConflictStrategy ImportConflictStrategy
	ImportPrices     bool
	DryRun           bool
}

// ImportRecordOutcome is the per-model result of an import (or dry-run).
type ImportRecordOutcome struct {
	ModelID string
	// Action: "create", "update", "skip", "conflict", "error".
	Action  string
	Message string
}

// ImportSummary aggregates the per-record outcomes.
type ImportSummary struct {
	Created   int32
	Updated   int32
	Skipped   int32
	Conflicts int32
	Errors    int32
	Results   []ImportRecordOutcome
}

// HasErrors reports whether the summary contains any conflict or error.
func (s *ImportSummary) HasErrors() bool {
	return s != nil && (s.Conflicts > 0 || s.Errors > 0)
}

// Typed errors for import/export. data maps driver errors onto these.
var (
	ErrImportSchemaVersion   = errors.New("import schema version mismatch")
	ErrImportRecordLimit     = errors.New("import exceeds record limit")
	ErrImportInvalidRecord   = errors.New("import contains an invalid record")
	ErrImportConflict        = errors.New("import conflict")
	ErrExportFailed          = errors.New("model export failed")
	ErrInvalidConflictStrategy = errors.New("invalid conflict strategy")
)

// MaxImportRecords is the hard cap on the number of models in a single import
// document. The registry is bounded (hundreds, not millions); a larger payload
// almost always means a wrong file. The limit is enforced before any write so
// a huge file cannot exhaust memory or hold a long transaction.
const MaxImportRecords = 5000

// ModelExchangeRepo extends ModelRepo with the bulk read/write operations
// needed for export/import. Keeping it on the same repository avoids a second
// DI seam while the interface stays the inversion boundary.
type ModelExchangeRepo interface {
	// ExportAllModels loads every model (paged internally) together with its
	// aliases and mappings, applying the filter. exportPrices is a hint for
	// the caller; the repo always returns the stored price and the service
	// layer zeroes it out when prices are not exported.
	ExportAllModels(ctx context.Context, filter ListModelsFilter) ([]*ModelExportModel, error)

	// ImportModels applies a batch of models in a single transaction. On the
	// first invalid record or unrecoverable conflict it returns an error and
	// the whole batch is rolled back — no partial writes. options.DryRun
	// performs validation and diff only and performs no writes; it returns
	// the same summary shape so the UI can render preview and result alike.
	ImportModels(ctx context.Context, models []*ModelExportModel, options ImportOptions) (*ImportSummary, error)
}

// ── Export usecase ─────────────────────────────────────────────────────────

// ExportModels exports the registry (optionally filtered) as a versioned
// document. When exportPrices is false, pricing fields are zeroed in the
// result so no price leaks into a config-migration export.
func (uc *ModelUsecase) ExportModels(ctx context.Context, filter ListModelsFilter, exportPrices bool) (*ModelExportResult, error) {
	if uc == nil {
		return nil, ErrExportFailed
	}
	exchangeRepo, ok := uc.repo.(ModelExchangeRepo)
	if !ok {
		return nil, fmt.Errorf("%w: repository does not support export", ErrExportFailed)
	}
	models, err := exchangeRepo.ExportAllModels(ctx, filter)
	if err != nil {
		return nil, err
	}
	if !exportPrices {
		for _, m := range models {
			if m == nil {
				continue
			}
			m.PricingInput = 0
			m.PricingOutput = 0
		}
	}
	// Deterministic ordering by canonical model id so re-export of the same
	// data produces a stable hash and diff.
	sort.Slice(models, func(i, j int) bool {
		if models[i] == nil || models[j] == nil {
			return false
		}
		return NormalizeModelID(models[i].ModelID) < NormalizeModelID(models[j].ModelID)
	})
	hash, err := ModelExchangeContentHash(models)
	if err != nil {
		return nil, err
	}
	return &ModelExportResult{
		SchemaVersion: ModelExchangeSchemaVersion,
		ExportedAt:    uc.now().UTC().Format(time.RFC3339),
		Models:        models,
		ContentHash:   hash,
	}, nil
}

// ── Import / dry-run usecase ───────────────────────────────────────────────

// DryRunImportModels validates an import document and returns the diff that a
// real import would produce, without writing anything.
func (uc *ModelUsecase) DryRunImportModels(ctx context.Context, models []*ModelExportModel, options ImportOptions) (*ImportSummary, error) {
	opts := options
	opts.DryRun = true
	return uc.importModels(ctx, models, opts)
}

// ImportModels applies an import document in one transaction. On the first
// invalid record or conflict (under the reject strategy) the whole batch is
// rolled back — no partial writes remain.
func (uc *ModelUsecase) ImportModels(ctx context.Context, models []*ModelExportModel, options ImportOptions) (*ImportSummary, error) {
	summary, err := uc.importModels(ctx, models, options)
	if err == nil && !options.DryRun {
		uc.invalidateChannelCache()
	}
	return summary, err
}

func (uc *ModelUsecase) importModels(ctx context.Context, models []*ModelExportModel, options ImportOptions) (*ImportSummary, error) {
	if uc == nil {
		return nil, ErrImportInvalidRecord
	}
	if options.ConflictStrategy == "" {
		options.ConflictStrategy = ConflictStrategyReject
	}
	switch options.ConflictStrategy {
	case ConflictStrategyReject, ConflictStrategyUpdate:
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidConflictStrategy, options.ConflictStrategy)
	}
	if len(models) > MaxImportRecords {
		return nil, fmt.Errorf("%w: %d records exceeds limit %d", ErrImportRecordLimit, len(models), MaxImportRecords)
	}
	// Pre-validate and canonicalise every record before touching storage, so a
	// dry-run and a real import see the same validation errors.
	for i := range models {
		if models[i] == nil {
			return nil, fmt.Errorf("%w: model at index %d is nil", ErrImportInvalidRecord, i)
		}
		if models[i].ModelID == "" {
			return nil, fmt.Errorf("%w: model at index %d has empty model_id", ErrImportInvalidRecord, i)
		}
		models[i].ModelID = NormalizeModelID(models[i].ModelID)
		for _, a := range models[i].Aliases {
			if a != nil {
				a.Alias = NormalizeModelID(a.Alias)
			}
		}
	}
	// Detect duplicate model_ids within the document itself — that is always
	// an error regardless of conflict strategy, because we cannot decide
	// which copy wins.
	if dups := duplicateModelIDs(models); len(dups) > 0 {
		return nil, fmt.Errorf("%w: duplicate model_id in document: %s", ErrImportInvalidRecord, strings.Join(dups, ", "))
	}
	exchangeRepo, ok := uc.repo.(ModelExchangeRepo)
	if !ok {
		return nil, fmt.Errorf("%w: repository does not support import", ErrImportInvalidRecord)
	}
	return exchangeRepo.ImportModels(ctx, models, options)
}

// duplicateModelIDs returns the canonical model_ids that appear more than
// once in the document.
func duplicateModelIDs(models []*ModelExportModel) []string {
	seen := make(map[string]int, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		seen[NormalizeModelID(m.ModelID)]++
	}
	var dups []string
	for id, n := range seen {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	return dups
}

// ModelExchangeContentHash returns the SHA-256 of a canonical JSON encoding of
// the models slice. The encoding sorts object keys so re-export of identical
// data yields the same hash — it is used both for integrity checks and for
// the audit trail.
func ModelExchangeContentHash(models []*ModelExportModel) (string, error) {
	// Sort by canonical model id first so the hash is stable regardless of
	// the input slice order.
	sorted := append([]*ModelExportModel(nil), models...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i] == nil || sorted[j] == nil {
			return false
		}
		return NormalizeModelID(sorted[i].ModelID) < NormalizeModelID(sorted[j].ModelID)
	})
	// Build a canonical JSON encoding with sorted keys so the hash is stable
	// regardless of map iteration order or field insertion order.
	raw, err := json.Marshal(canonicalExportView(sorted))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalExportView projects the models into a minimal, key-sorted map
// representation used purely for hashing. It excludes timestamps and DB-assigned
// ids so two exports of the same logical content hash equally.
type canonicalModelView struct {
	ModelID              string                 `json:"model_id"`
	DisplayName          string                 `json:"display_name"`
	Description          string                 `json:"description"`
	Provider             string                 `json:"provider"`
	ModelType            string                 `json:"model_type"`
	ContextWindow        int32                  `json:"context_window"`
	PricingInput         float64                `json:"pricing_input"`
	PricingOutput        float64                `json:"pricing_output"`
	Status               int32                  `json:"status"`
	IsPublic             bool                   `json:"is_public"`
	Capabilities         []string               `json:"capabilities"`
	Tags                 []string               `json:"tags"`
	Category             string                 `json:"category"`
	Tier                 string                 `json:"tier"`
	Metadata             string                 `json:"metadata"`
	Aliases              []string               `json:"aliases"`
	ChannelMappings      []canonicalMappingView `json:"channel_mappings"`
	SubscriptionMappings []canonicalMappingView `json:"subscription_mappings"`
}

type canonicalMappingView struct {
	ChannelID       int64  `json:"channel_id,omitempty"`
	AccountID       int64  `json:"account_id,omitempty"`
	GroupName       string `json:"group_name,omitempty"`
	Enabled         bool   `json:"enabled"`
	Priority        int32  `json:"priority"`
	Config          string `json:"config,omitempty"`
	UpstreamModelID string `json:"upstream_model_id,omitempty"`
}

func canonicalExportView(models []*ModelExportModel) []canonicalModelView {
	out := make([]canonicalModelView, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		view := canonicalModelView{
			ModelID:       NormalizeModelID(m.ModelID),
			DisplayName:   m.DisplayName,
			Description:   m.Description,
			Provider:      m.Provider,
			ModelType:     m.ModelType,
			ContextWindow: m.ContextWindow,
			PricingInput:  m.PricingInput,
			PricingOutput: m.PricingOutput,
			Status:        m.Status,
			IsPublic:      m.IsPublic,
			Capabilities:  sortedCopy(m.Capabilities),
			Tags:          sortedCopy(m.Tags),
			Category:      m.Category,
			Tier:          m.Tier,
			Metadata:      m.Metadata,
		}
		for _, a := range m.Aliases {
			if a != nil && a.Alias != "" {
				view.Aliases = append(view.Aliases, NormalizeModelID(a.Alias))
			}
		}
		sort.Strings(view.Aliases)
		for _, c := range m.ChannelMappings {
			if c == nil {
				continue
			}
			view.ChannelMappings = append(view.ChannelMappings, canonicalMappingView{
				ChannelID:       c.ChannelID,
				Enabled:         c.Enabled,
				Priority:        c.Priority,
				Config:          c.Config,
				UpstreamModelID: c.UpstreamModelID,
			})
		}
		sort.Slice(view.ChannelMappings, func(i, j int) bool {
			return view.ChannelMappings[i].ChannelID < view.ChannelMappings[j].ChannelID
		})
		for _, s := range m.SubscriptionMappings {
			if s == nil {
				continue
			}
			view.SubscriptionMappings = append(view.SubscriptionMappings, canonicalMappingView{
				AccountID:       s.SubscriptionAccountID,
				GroupName:       s.GroupName,
				Enabled:         s.Enabled,
				Priority:        s.Priority,
				UpstreamModelID: s.UpstreamModelID,
			})
		}
		sort.Slice(view.SubscriptionMappings, func(i, j int) bool {
			if view.SubscriptionMappings[i].AccountID != view.SubscriptionMappings[j].AccountID {
				return view.SubscriptionMappings[i].AccountID < view.SubscriptionMappings[j].AccountID
			}
			return view.SubscriptionMappings[i].GroupName < view.SubscriptionMappings[j].GroupName
		})
		out = append(out, view)
	}
	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
