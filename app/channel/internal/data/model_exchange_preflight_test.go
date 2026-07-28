package data

import (
	"context"
	"errors"
	"testing"

	"micro-one-api/app/channel/internal/biz"
)

func TestPreflightImport_DetectsZeroChannelID(t *testing.T) {
	r := &Repository{} // memory mode
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			ChannelMappings: []*biz.ModelChannelMapping{
				{ChannelID: 0, UpstreamModelID: "gpt-4o-2024"},
			},
		},
	}
	err := r.preflightImport(context.Background(), models, nil, nil)
	if err == nil {
		t.Fatal("expected error for zero channel_id, got nil")
	}
}

func TestPreflightImport_DetectsZeroSubscriptionAccountID(t *testing.T) {
	r := &Repository{}
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			SubscriptionMappings: []*biz.ModelSubscriptionMapping{
				{SubscriptionAccountID: 0},
			},
		},
	}
	err := r.preflightImport(context.Background(), models, nil, nil)
	if err == nil {
		t.Fatal("expected error for zero subscription_account_id, got nil")
	}
}

func TestPreflightImport_DetectsDuplicateAliasInDocument(t *testing.T) {
	r := &Repository{}
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			Aliases: []*biz.ModelAlias{{Alias: "gpt4"}},
		},
		{
			ModelID: "claude-3",
			Aliases: []*biz.ModelAlias{{Alias: "gpt4"}},
		},
	}
	err := r.preflightImport(context.Background(), models, nil, nil)
	if err == nil {
		t.Fatal("expected error for duplicate alias across models in document")
	}
}

func TestPreflightImport_DetectsDuplicateChannelMapping(t *testing.T) {
	r := &Repository{}
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			ChannelMappings: []*biz.ModelChannelMapping{
				{ChannelID: 5, UpstreamModelID: "gpt-4o"},
				{ChannelID: 5, UpstreamModelID: "gpt-4o-alt"},
			},
		},
	}
	err := r.preflightImport(context.Background(), models, nil, nil)
	if err == nil {
		t.Fatal("expected error for duplicate channel mapping")
	}
}

func TestPreflightImport_PassesValidDocument(t *testing.T) {
	// Memory mode with FK validation: the referenced channel and account must
	// exist in the memory store (code review MEDIUM-1: memory FK validation
	// is now enabled, matching DB semantics).
	r := &Repository{
		channels: map[int64]*biz.Channel{
			5: {ID: 5},
		},
		subAccounts: map[int64]*biz.SubscriptionAccount{
			3: {ID: 3},
		},
	}
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			Aliases: []*biz.ModelAlias{{Alias: "gpt4o"}},
			ChannelMappings: []*biz.ModelChannelMapping{
				{ChannelID: 5, UpstreamModelID: "gpt-4o"},
			},
			SubscriptionMappings: []*biz.ModelSubscriptionMapping{
				{SubscriptionAccountID: 3, GroupName: "default"},
			},
		},
	}
	err := r.preflightImport(context.Background(), models, nil, nil)
	if err != nil {
		t.Fatalf("expected no error for valid document, got %v", err)
	}
}

func TestPreflightImport_MemoryDetectsDanglingChannel(t *testing.T) {
	// Memory mode FK validation: channel_id 99 does not exist.
	r := &Repository{
		channels: map[int64]*biz.Channel{
			5: {ID: 5},
		},
	}
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			ChannelMappings: []*biz.ModelChannelMapping{
				{ChannelID: 99, UpstreamModelID: "gpt-4o"},
			},
		},
	}
	err := r.preflightImport(context.Background(), models, nil, nil)
	if err == nil {
		t.Fatal("expected dangling FK error for channel_id 99 in memory mode")
	}
}

// TestPreflightImport_SameOwnerAliasPasses verifies that an alias belonging to
// the SAME model being updated does NOT trigger a conflict (code review
// HIGH-3). This is the round-trip export→import case: a model exported and
// re-imported with its aliases intact must pass preflight.
func TestPreflightImport_SameOwnerAliasPasses(t *testing.T) {
	r := &Repository{
		models: map[int64]*biz.Model{
			1: {ID: 1, ModelID: "gpt-4o"},
		},
		modelAliases: map[int64]*biz.ModelAlias{
			10: {ID: 10, ModelPK: 1, Alias: "gpt4o"},
		},
	}
	// Build the alias-owner map and model-PK map that the caller would pass.
	aliasOwners, _ := r.loadExistingAliasOwners(context.Background())
	modelPKs, _ := r.loadExistingModelPKs(context.Background(), []*biz.ModelExportModel{
		{ModelID: "gpt-4o"},
	})

	// Re-import the same model with the same alias — must NOT conflict.
	models := []*biz.ModelExportModel{
		{
			ModelID: "gpt-4o",
			Aliases: []*biz.ModelAlias{{Alias: "gpt4o"}},
		},
	}
	err := r.preflightImport(context.Background(), models, aliasOwners, modelPKs)
	if err != nil {
		t.Fatalf("same-owner alias must pass preflight (round-trip case), got: %v", err)
	}
}

// TestPreflightImport_CrossModelAliasConflict verifies that an alias belonging
// to a DIFFERENT model IS correctly flagged as a conflict.
func TestPreflightImport_CrossModelAliasConflict(t *testing.T) {
	r := &Repository{
		models: map[int64]*biz.Model{
			1: {ID: 1, ModelID: "gpt-4o"},
			2: {ID: 2, ModelID: "claude-3"},
		},
		modelAliases: map[int64]*biz.ModelAlias{
			10: {ID: 10, ModelPK: 1, Alias: "gpt4o"}, // belongs to model PK 1
		},
	}
	aliasOwners, _ := r.loadExistingAliasOwners(context.Background())
	modelPKs, _ := r.loadExistingModelPKs(context.Background(), []*biz.ModelExportModel{
		{ModelID: "claude-3"},
	})

	// Import claude-3 with an alias that belongs to gpt-4o (PK 1) — must conflict.
	models := []*biz.ModelExportModel{
		{
			ModelID: "claude-3",
			Aliases: []*biz.ModelAlias{{Alias: "gpt4o"}},
		},
	}
	err := r.preflightImport(context.Background(), models, aliasOwners, modelPKs)
	if err == nil {
		t.Fatal("cross-model alias conflict must be detected")
	}
}

// TestImportModelsMemory_DryRunDoesNotWrite verifies that the memory import
// path does NOT mutate the store when DryRun=true (code review MEDIUM-1).
func TestImportModelsMemory_DryRunDoesNotWrite(t *testing.T) {
	r := &Repository{
		models: map[int64]*biz.Model{
			1: {ID: 1, ModelID: "gpt-4o", DisplayName: "Original"},
		},
	}
	// Dry-run an update that would change the display name.
	models := []*biz.ModelExportModel{
		{ModelID: "gpt-4o", DisplayName: "Changed"},
	}
	summary, err := r.importModelsMemory(models, biz.ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if summary.Updated != 1 {
		t.Fatalf("dry-run should classify 1 update, got %d", summary.Updated)
	}
	// The store must NOT have been mutated.
	if r.models[1].DisplayName != "Original" {
		t.Fatalf("dry-run mutated the store: DisplayName = %q, want %q", r.models[1].DisplayName, "Original")
	}
}

func TestImportModelsMemory_RejectConflictIsAtomic(t *testing.T) {
	r := &Repository{
		models: map[int64]*biz.Model{
			1: {ID: 1, ModelID: "existing", DisplayName: "Original"},
		},
		modelNextID: 1,
	}

	_, err := r.importModelsMemory([]*biz.ModelExportModel{
		{ModelID: "new-model", DisplayName: "New"},
		{ModelID: "existing", DisplayName: "Changed"},
	}, biz.ImportOptions{ConflictStrategy: biz.ConflictStrategyReject})
	if !errors.Is(err, biz.ErrImportConflict) {
		t.Fatalf("import error = %v, want ErrImportConflict", err)
	}
	if len(r.models) != 1 {
		t.Fatalf("reject import wrote partial data: model count = %d, want 1", len(r.models))
	}
	if r.modelNextID != 1 {
		t.Fatalf("reject import advanced model ID: got %d, want 1", r.modelNextID)
	}
	if r.models[1].DisplayName != "Original" {
		t.Fatalf("reject import changed existing model: got %q", r.models[1].DisplayName)
	}
}

func TestImportModelsMemory_UpdatePreservesCreatedAtAndPrices(t *testing.T) {
	r := &Repository{
		models: map[int64]*biz.Model{
			1: {
				ID:            1,
				ModelID:       "gpt-4o",
				DisplayName:   "Original",
				PricingInput:  1.25,
				PricingOutput: 2.5,
				CreatedAt:     123,
				UpdatedAt:     456,
			},
		},
		modelNextID: 1,
	}

	summary, err := r.importModelsMemory([]*biz.ModelExportModel{
		{
			ModelID:       "gpt-4o",
			DisplayName:   "Changed",
			PricingInput:  99,
			PricingOutput: 100,
		},
	}, biz.ImportOptions{ConflictStrategy: biz.ConflictStrategyUpdate, ImportPrices: false})
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if summary.Updated != 1 {
		t.Fatalf("updated = %d, want 1", summary.Updated)
	}
	got := r.models[1]
	if got.CreatedAt != 123 {
		t.Fatalf("CreatedAt = %d, want 123", got.CreatedAt)
	}
	if got.PricingInput != 1.25 || got.PricingOutput != 2.5 {
		t.Fatalf("prices = (%v, %v), want (1.25, 2.5)", got.PricingInput, got.PricingOutput)
	}
	if got.DisplayName != "Changed" {
		t.Fatalf("DisplayName = %q, want Changed", got.DisplayName)
	}
}

func TestExportAllModelsMemory_ReturnsDetachedStableSnapshot(t *testing.T) {
	r := &Repository{
		models: map[int64]*biz.Model{
			2: {ID: 2, ModelID: "z-model", Capabilities: []string{"chat"}},
			1: {ID: 1, ModelID: "a-model"},
		},
		modelAliases: map[int64]*biz.ModelAlias{
			2: {ID: 2, ModelPK: 2, Alias: "z2"},
			1: {ID: 1, ModelPK: 2, Alias: "z1"},
		},
	}

	got, err := r.exportAllModelsMemory(biz.ListModelsFilter{})
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if len(got) != 2 || got[0].ModelID != "a-model" || got[1].ModelID != "z-model" {
		t.Fatalf("model order = %#v, want a-model then z-model", got)
	}
	if len(got[1].Aliases) != 2 || got[1].Aliases[0].ID != 1 || got[1].Aliases[1].ID != 2 {
		t.Fatalf("alias order = %#v, want ascending IDs", got[1].Aliases)
	}
	got[1].Capabilities[0] = "mutated"
	got[1].Aliases[0].Alias = "mutated"
	if r.models[2].Capabilities[0] != "chat" || r.modelAliases[1].Alias != "z1" {
		t.Fatal("export returned pointers into the memory store")
	}
}
