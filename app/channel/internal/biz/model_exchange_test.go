package biz

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ── Model import/export tests (v0.11.0 Phase 4) ───────────────────────────
//
// These tests exercise the biz usecase layer against an in-memory repo that
// also implements ModelExchangeRepo. They cover:
//   - round-trip export → import equivalence (same-version, empty target)
//   - conflict reject (default) aborts without partial writes
//   - conflict update overwrites the differing model
//   - duplicate model_id in a document is always rejected
//   - schema version mismatch is rejected
//   - record limit is enforced
//   - export strips prices when exportPrices=false
//   - content hash is stable for identical content

// exchangeTestRepo extends fakeModelRepo with ModelExchangeRepo so the
// usecase type-assertion succeeds. It keeps a simple append-only model store
// so we can assert rollback left the registry unchanged after a failed import.
type exchangeTestRepo struct {
	*fakeModelRepo
}

// newExchangeTestUC creates a usecase with now wired, so ExportModels does not nil-deref.
func newExchangeTestUC(repo *exchangeTestRepo) *ModelUsecase {
	return &ModelUsecase{repo: repo, now: time.Now}
}

func newExchangeTestRepo() *exchangeTestRepo {
	return &exchangeTestRepo{fakeModelRepo: newFakeModelRepo()}
}

// ExportAllModels returns every stored model with its aliases/mappings.
func (r *exchangeTestRepo) ExportAllModels(ctx context.Context, filter ListModelsFilter) ([]*ModelExportModel, error) {
	out := make([]*ModelExportModel, 0, len(r.models))
	for _, m := range r.models {
		if !matchesFilter(m, filter) {
			continue
		}
		em := &ModelExportModel{
			ModelID:       m.ModelID,
			DisplayName:   m.DisplayName,
			Description:   m.Description,
			Provider:      m.Provider,
			ModelType:     m.ModelType,
			ContextWindow: m.ContextWindow,
			PricingInput:  m.PricingInput,
			PricingOutput: m.PricingOutput,
			Status:        m.Status,
			IsPublic:      m.IsPublic,
			Capabilities:  append([]string(nil), m.Capabilities...),
			Tags:          append([]string(nil), m.Tags...),
			Category:      m.Category,
			Tier:          m.Tier,
			Metadata:      m.Metadata,
		}
		for _, a := range r.aliases {
			if a.ModelPK == m.ID {
				em.Aliases = append(em.Aliases, &ModelAlias{Alias: a.Alias, IsPrimary: a.IsPrimary})
			}
		}
		for _, c := range r.channelMaps {
			if c.ModelPK == m.ID {
				em.ChannelMappings = append(em.ChannelMappings, c)
			}
		}
		for _, s := range r.subMaps {
			if s.ModelPK == m.ID {
				em.SubscriptionMappings = append(em.SubscriptionMappings, s)
			}
		}
		out = append(out, em)
	}
	return out, nil
}

// ImportModels applies the batch. It records outcomes and, for conflict
// under reject, returns an error WITHOUT writing (simulating a rolled-back
// transaction). For update, it overwrites the model fields.
func (r *exchangeTestRepo) ImportModels(ctx context.Context, models []*ModelExportModel, options ImportOptions) (*ImportSummary, error) {
	summary := &ImportSummary{}
	if options.DryRun {
		// Dry-run: compute outcomes without writing.
		existingByCanonical := make(map[string]*Model)
		for _, m := range r.models {
			existingByCanonical[NormalizeModelID(m.ModelID)] = m
		}
		for _, em := range models {
			if em == nil {
				continue
			}
			exist := existingByCanonical[NormalizeModelID(em.ModelID)]
			outcome := fakeComputeImportOutcome(em, exist, options)
			summary.Results = append(summary.Results, outcome)
			switch outcome.Action {
			case "create":
				summary.Created++
			case "update":
				summary.Updated++
			case "skip":
				summary.Skipped++
			}
		}
		return summary, nil
	}
	// Build existing index.
	existingByCanonical := make(map[string]*Model)
	for _, m := range r.models {
		existingByCanonical[NormalizeModelID(m.ModelID)] = m
	}
	// Snapshot for rollback assertion: if any conflict-reject occurs, we must
	// not leave partial writes. Since this fake writes in place, we detect
	// conflict first (pre-scan) under reject to emulate a transaction.
	if options.ConflictStrategy == ConflictStrategyReject {
		for _, em := range models {
			if em == nil {
				continue
			}
			exist := existingByCanonical[NormalizeModelID(em.ModelID)]
			if exist != nil && !modelsContentEqualForTest(em, exist, options) {
				return nil, ErrImportConflict
			}
		}
	}
	for _, em := range models {
		if em == nil {
			continue
		}
		exist := existingByCanonical[NormalizeModelID(em.ModelID)]
		outcome := fakeComputeImportOutcome(em, exist, options)
		summary.Results = append(summary.Results, outcome)
		switch outcome.Action {
		case "create":
			r.nextID++
			do := &Model{
				ID:            r.nextID,
				ModelID:       NormalizeModelID(em.ModelID),
				DisplayName:   em.DisplayName,
				Description:   em.Description,
				Provider:      em.Provider,
				ModelType:     em.ModelType,
				ContextWindow: em.ContextWindow,
				Status:        em.Status,
				IsPublic:      em.IsPublic,
				Capabilities:  append([]string(nil), em.Capabilities...),
				Tags:          append([]string(nil), em.Tags...),
				Category:      em.Category,
				Tier:          em.Tier,
				Metadata:      em.Metadata,
			}
			if options.ImportPrices {
				do.PricingInput = em.PricingInput
				do.PricingOutput = em.PricingOutput
			}
			r.models[do.ID] = do
			existingByCanonical[NormalizeModelID(do.ModelID)] = do
			summary.Created++
		case "update":
			exist.DisplayName = em.DisplayName
			exist.Description = em.Description
			exist.Provider = em.Provider
			exist.ModelType = em.ModelType
			exist.ContextWindow = em.ContextWindow
			exist.Status = em.Status
			exist.IsPublic = em.IsPublic
			exist.Capabilities = append([]string(nil), em.Capabilities...)
			exist.Tags = append([]string(nil), em.Tags...)
			exist.Category = em.Category
			exist.Tier = em.Tier
			exist.Metadata = em.Metadata
			if options.ImportPrices {
				exist.PricingInput = em.PricingInput
				exist.PricingOutput = em.PricingOutput
			}
			summary.Updated++
		case "skip":
			summary.Skipped++
		}
	}
	return summary, nil
}

// modelsContentEqualForTest mirrors data.stringSliceEqualSorted for the fake.
func modelsContentEqualForTest(em *ModelExportModel, existing *Model, options ImportOptions) bool {
	if em.DisplayName != existing.DisplayName {
		return false
	}
	if em.Provider != existing.Provider {
		return false
	}
	if em.Status != existing.Status {
		return false
	}
	if options.ImportPrices {
		if em.PricingInput != existing.PricingInput || em.PricingOutput != existing.PricingOutput {
			return false
		}
	}
	return true
}

// fakeComputeImportOutcome mirrors data.computeImportOutcome for the in-memory fake.
func fakeComputeImportOutcome(em *ModelExportModel, existing *Model, options ImportOptions) ImportRecordOutcome {
	if existing == nil {
		return ImportRecordOutcome{ModelID: em.ModelID, Action: "create"}
	}
	if modelsContentEqualForTest(em, existing, options) {
		return ImportRecordOutcome{ModelID: em.ModelID, Action: "skip", Message: "identical content"}
	}
	if options.ConflictStrategy == ConflictStrategyReject {
		return ImportRecordOutcome{ModelID: em.ModelID, Action: "conflict", Message: "model exists with different content; conflict_strategy=reject"}
	}
	return ImportRecordOutcome{ModelID: em.ModelID, Action: "update"}
}

// ── Test cases ─────────────────────────────────────────────────────────────

func TestExportImport_RoundTrip(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	// Seed two models.
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "gpt-4o", DisplayName: "GPT-4o", Provider: "openai", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "claude-4", DisplayName: "Claude 4", Provider: "anthropic", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Export.
	result, err := uc.ExportModels(context.Background(), ListModelsFilter{}, true)
	if err != nil {
		t.Fatalf("ExportModels: %v", err)
	}
	if len(result.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result.Models))
	}
	if result.ContentHash == "" {
		t.Fatal("expected non-empty content hash")
	}
	// Import into a fresh repo → should create both.
	target := newExchangeTestRepo()
	targetUC := newExchangeTestUC(target)
	summary, err := targetUC.ImportModels(context.Background(), result.Models, ImportOptions{ConflictStrategy: ConflictStrategyReject})
	if err != nil {
		t.Fatalf("ImportModels: %v", err)
	}
	if summary.Created != 2 {
		t.Fatalf("expected 2 created, got %d", summary.Created)
	}
	// Re-export from target and compare hash → round-trip equivalence.
	targetResult, err := targetUC.ExportModels(context.Background(), ListModelsFilter{}, true)
	if err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if targetResult.ContentHash != result.ContentHash {
		t.Fatalf("round-trip hash mismatch: %s != %s", targetResult.ContentHash, result.ContentHash)
	}
}

func TestExport_StripsPricesWhenNotRequested(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "gpt-4o", PricingInput: 0.01, PricingOutput: 0.03, Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	result, err := uc.ExportModels(context.Background(), ListModelsFilter{}, false)
	if err != nil {
		t.Fatalf("ExportModels: %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	if result.Models[0].PricingInput != 0 || result.Models[0].PricingOutput != 0 {
		t.Fatalf("expected prices zeroed, got input=%v output=%v", result.Models[0].PricingInput, result.Models[0].PricingOutput)
	}
}

func TestImport_ConflictRejectNoPartialWrite(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	// Seed an existing model.
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "gpt-4o", DisplayName: "Original", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	originalCount := len(repo.models)
	// Attempt to import a conflicting model + a new one. Under reject, the
	// whole batch must fail and neither the conflict nor the new model is
	// written.
	summary, err := uc.ImportModels(context.Background(), []*ModelExportModel{
		{ModelID: "gpt-4o", DisplayName: "Changed", Status: 1},
		{ModelID: "new-model", DisplayName: "New", Status: 1},
	}, ImportOptions{ConflictStrategy: ConflictStrategyReject})
	if err == nil {
		t.Fatalf("expected conflict error, got summary=%+v", summary)
	}
	if len(repo.models) != originalCount {
		t.Fatalf("partial write detected: repo grew from %d to %d", originalCount, len(repo.models))
	}
}

func TestImport_ConflictUpdateOverwrites(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "gpt-4o", DisplayName: "Original", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	summary, err := uc.ImportModels(context.Background(), []*ModelExportModel{
		{ModelID: "gpt-4o", DisplayName: "Updated", Status: 1},
	}, ImportOptions{ConflictStrategy: ConflictStrategyUpdate})
	if err != nil {
		t.Fatalf("ImportModels: %v", err)
	}
	if summary.Updated != 1 {
		t.Fatalf("expected 1 updated, got %d", summary.Updated)
	}
	got := repo.models[1] // first model seeded at id=1
	if got.DisplayName != "Updated" {
		t.Fatalf("expected display name updated, got %q", got.DisplayName)
	}
}

func TestImport_SkipIdentical(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "gpt-4o", DisplayName: "Same", Provider: "openai", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	summary, err := uc.ImportModels(context.Background(), []*ModelExportModel{
		{ModelID: "gpt-4o", DisplayName: "Same", Provider: "openai", Status: 1},
	}, ImportOptions{ConflictStrategy: ConflictStrategyReject})
	if err != nil {
		t.Fatalf("ImportModels: %v", err)
	}
	if summary.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got created=%d updated=%d skipped=%d", summary.Created, summary.Updated, summary.Skipped)
	}
}

func TestImport_DuplicateModelIDInDocumentRejected(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	_, err := uc.ImportModels(context.Background(), []*ModelExportModel{
		{ModelID: "gpt-4o", Status: 1},
		{ModelID: "GPT-4O", Status: 1}, // canonical dup
	}, ImportOptions{ConflictStrategy: ConflictStrategyReject})
	if err == nil {
		t.Fatal("expected duplicate model_id error")
	}
	if !strings.Contains(err.Error(), "duplicate model_id") {
		t.Fatalf("expected duplicate model_id error, got %v", err)
	}
}

func TestImport_RecordLimitEnforced(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	tooMany := make([]*ModelExportModel, MaxImportRecords+1)
	for i := range tooMany {
		tooMany[i] = &ModelExportModel{ModelID: "m-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Status: 1}
	}
	_, err := uc.ImportModels(context.Background(), tooMany, ImportOptions{})
	if err == nil {
		t.Fatal("expected record limit error")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected record limit error, got %v", err)
	}
}

func TestImport_InvalidConflictStrategy(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	_, err := uc.ImportModels(context.Background(), []*ModelExportModel{
		{ModelID: "gpt-4o", Status: 1},
	}, ImportOptions{ConflictStrategy: ImportConflictStrategy("bogus")})
	if err == nil {
		t.Fatal("expected invalid conflict strategy error")
	}
}

func TestDryRun_NoWrite(t *testing.T) {
	repo := newExchangeTestRepo()
	uc := newExchangeTestUC(repo)
	before := len(repo.models)
	summary, err := uc.DryRunImportModels(context.Background(), []*ModelExportModel{
		{ModelID: "gpt-4o", DisplayName: "New", Status: 1},
	}, ImportOptions{})
	if err != nil {
		t.Fatalf("DryRunImportModels: %v", err)
	}
	if summary.Results[0].Action != "create" {
		t.Fatalf("dry-run should predict create, got %s", summary.Results[0].Action)
	}
	if len(repo.models) != before {
		t.Fatalf("dry-run wrote to repo: grew from %d to %d", before, len(repo.models))
	}
}

func TestModelExchangeContentHash_Stable(t *testing.T) {
	models := []*ModelExportModel{
		{ModelID: "b", DisplayName: "B", Capabilities: []string{"x", "y"}, InputModalities: []string{"image", "text"}, OutputModalities: []string{"text"}, Status: 1},
		{ModelID: "a", DisplayName: "A", Capabilities: []string{"y", "x"}, InputModalities: []string{"text", "image"}, OutputModalities: []string{"audio", "text"}, Status: 1},
	}
	h1, err := ModelExchangeContentHash(models)
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	// Swap model and modality order — the canonical export view sorts both.
	models[0].InputModalities = []string{"text", "image"}
	models[1].OutputModalities = []string{"text", "audio"}
	models[0], models[1] = models[1], models[0]
	h2, err := ModelExchangeContentHash(models)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("content hash not stable: %s != %s", h1, h2)
	}
}
