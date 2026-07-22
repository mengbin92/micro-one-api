package biz

import (
	"context"
	"errors"
	"testing"
)

// fakeModelRepo is an in-memory ModelRepo for usecase tests.
type fakeModelRepo struct {
	models      map[int64]*Model
	aliases     map[int64]*ModelAlias
	channelMaps map[int64]*ModelChannelMapping
	subMaps     map[int64]*ModelSubscriptionMapping
	nextID      int64
	nextAliasID int64
	nextMapID   int64
}

func newFakeModelRepo() *fakeModelRepo {
	return &fakeModelRepo{
		models:      make(map[int64]*Model),
		aliases:     make(map[int64]*ModelAlias),
		channelMaps: make(map[int64]*ModelChannelMapping),
		subMaps:     make(map[int64]*ModelSubscriptionMapping),
	}
}

func (r *fakeModelRepo) ListModels(ctx context.Context, page, pageSize int32, filter ListModelsFilter) ([]*Model, int64, error) {
	var filtered []*Model
	for _, m := range r.models {
		if matchesFilter(m, filter) {
			filtered = append(filtered, m)
		}
	}
	total := int64(len(filtered))
	start := int((page - 1) * pageSize)
	if start >= len(filtered) {
		return []*Model{}, total, nil
	}
	end := start + int(pageSize)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, nil
}

func matchesFilter(m *Model, f ListModelsFilter) bool {
	if f.Provider != "" && m.Provider != f.Provider {
		return false
	}
	if f.Status != 0 && m.Status != f.Status {
		return false
	}
	return true
}

func (r *fakeModelRepo) GetModel(ctx context.Context, modelPK int64) (*Model, error) {
	m, ok := r.models[modelPK]
	if !ok {
		return nil, ErrModelNotFound
	}
	return m, nil
}

func (r *fakeModelRepo) GetModelByID(ctx context.Context, modelID string) (*Model, error) {
	for _, m := range r.models {
		if m.ModelID == modelID {
			return m, nil
		}
	}
	return nil, ErrModelNotFound
}

func (r *fakeModelRepo) CreateModel(ctx context.Context, model *Model) error {
	for _, m := range r.models {
		if m.ModelID == model.ModelID {
			return ErrModelIDExists
		}
	}
	r.nextID++
	model.ID = r.nextID
	r.models[model.ID] = model
	return nil
}

func (r *fakeModelRepo) UpdateModel(ctx context.Context, model *Model) error {
	if _, ok := r.models[model.ID]; !ok {
		return ErrModelNotFound
	}
	r.models[model.ID] = model
	return nil
}

func (r *fakeModelRepo) DeleteModel(ctx context.Context, modelPK int64) error {
	if _, ok := r.models[modelPK]; !ok {
		return ErrModelNotFound
	}
	delete(r.models, modelPK)
	return nil
}

func (r *fakeModelRepo) ChangeModelStatus(ctx context.Context, modelPK int64, status int32) error {
	m, ok := r.models[modelPK]
	if !ok {
		return ErrModelNotFound
	}
	m.Status = status
	return nil
}

func (r *fakeModelRepo) BatchChangeStatus(ctx context.Context, modelPKs []int64, status int32) (int32, error) {
	var n int32
	for _, pk := range modelPKs {
		if m, ok := r.models[pk]; ok {
			m.Status = status
			n++
		}
	}
	return n, nil
}

func (r *fakeModelRepo) BatchDelete(ctx context.Context, modelPKs []int64) (int32, error) {
	var n int32
	for _, pk := range modelPKs {
		if _, ok := r.models[pk]; ok {
			delete(r.models, pk)
			n++
		}
	}
	return n, nil
}

func (r *fakeModelRepo) ListModelAliases(ctx context.Context, modelPK int64) ([]*ModelAlias, error) {
	var out []*ModelAlias
	for _, a := range r.aliases {
		if modelPK == 0 || a.ModelPK == modelPK {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) CreateModelAlias(ctx context.Context, alias *ModelAlias) error {
	for _, a := range r.aliases {
		if a.Alias == alias.Alias {
			return ErrAliasExists
		}
	}
	r.nextAliasID++
	alias.ID = r.nextAliasID
	r.aliases[alias.ID] = alias
	return nil
}

func (r *fakeModelRepo) DeleteModelAlias(ctx context.Context, aliasID int64) error {
	if _, ok := r.aliases[aliasID]; !ok {
		return ErrAliasNotFound
	}
	delete(r.aliases, aliasID)
	return nil
}

func (r *fakeModelRepo) ListChannelMappings(ctx context.Context, channelID int64) ([]*ModelChannelMapping, error) {
	var out []*ModelChannelMapping
	for _, m := range r.channelMaps {
		if channelID == 0 || m.ChannelID == channelID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) ListChannelMappingsByModel(ctx context.Context, modelPK int64) ([]*ModelChannelMapping, error) {
	var out []*ModelChannelMapping
	for _, m := range r.channelMaps {
		if m.ModelPK == modelPK {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) UpsertChannelMapping(ctx context.Context, m *ModelChannelMapping) error {
	for _, ex := range r.channelMaps {
		if ex.ChannelID == m.ChannelID && ex.ModelPK == m.ModelPK {
			ex.Enabled = m.Enabled
			ex.Priority = m.Priority
			return nil
		}
	}
	r.nextMapID++
	m.ID = r.nextMapID
	r.channelMaps[m.ID] = m
	return nil
}

func (r *fakeModelRepo) DeleteChannelMapping(ctx context.Context, channelID, modelPK int64) error {
	for id, m := range r.channelMaps {
		if m.ChannelID == channelID && m.ModelPK == modelPK {
			delete(r.channelMaps, id)
			return nil
		}
	}
	return ErrMappingNotFound
}

func (r *fakeModelRepo) ListSubscriptionMappings(ctx context.Context, accountID int64) ([]*ModelSubscriptionMapping, error) {
	var out []*ModelSubscriptionMapping
	for _, m := range r.subMaps {
		if accountID == 0 || m.SubscriptionAccountID == accountID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) ListSubscriptionMappingsByModel(ctx context.Context, modelPK int64) ([]*ModelSubscriptionMapping, error) {
	var out []*ModelSubscriptionMapping
	for _, m := range r.subMaps {
		if m.ModelPK == modelPK {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) UpsertSubscriptionMapping(ctx context.Context, m *ModelSubscriptionMapping) error {
	for _, ex := range r.subMaps {
		if ex.SubscriptionAccountID == m.SubscriptionAccountID && ex.ModelPK == m.ModelPK && ex.GroupName == m.GroupName {
			ex.Enabled = m.Enabled
			ex.Priority = m.Priority
			return nil
		}
	}
	r.nextMapID++
	m.ID = r.nextMapID
	r.subMaps[m.ID] = m
	return nil
}

func (r *fakeModelRepo) DeleteSubscriptionMapping(ctx context.Context, accountID, modelPK int64, groupName string) error {
	for id, m := range r.subMaps {
		if m.SubscriptionAccountID == accountID && m.ModelPK == modelPK && (groupName == "" || m.GroupName == groupName) {
			delete(r.subMaps, id)
			return nil
		}
	}
	return ErrMappingNotFound
}

// ── Tests ──────────────────────────────────────────────────────────────────

func TestModelUsecase_CreateAndGet(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)

	m := &Model{ModelID: "gpt-4o", DisplayName: "GPT-4o", Provider: "openai"}
	if err := uc.CreateModel(context.Background(), m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}
	if m.Status != ModelStatusEnabled {
		t.Fatalf("expected default status enabled, got %d", m.Status)
	}
	if m.ModelType != "chat" {
		t.Fatalf("expected default model_type chat, got %s", m.ModelType)
	}

	got, err := uc.GetModelByID(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatalf("GetModelByID: %v", err)
	}
	if got.ModelID != "gpt-4o" {
		t.Fatalf("unexpected model_id: %s", got.ModelID)
	}
}

func TestModelUsecase_CreateDuplicate(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)

	_ = uc.CreateModel(context.Background(), &Model{ModelID: "dup", DisplayName: "Dup"})
	err := uc.CreateModel(context.Background(), &Model{ModelID: "dup", DisplayName: "Dup2"})
	if !errors.Is(err, ErrModelIDExists) {
		t.Fatalf("expected ErrModelIDExists, got %v", err)
	}
}

func TestModelUsecase_CreateRequiresModelID(t *testing.T) {
	uc := NewModelUsecase(newFakeModelRepo())
	err := uc.CreateModel(context.Background(), &Model{})
	if err == nil {
		t.Fatal("expected error for empty model_id")
	}
}

func TestModelUsecase_UpdateNotFound(t *testing.T) {
	uc := NewModelUsecase(newFakeModelRepo())
	err := uc.UpdateModel(context.Background(), &Model{ID: 999, DisplayName: "x"})
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("expected ErrModelNotFound, got %v", err)
	}
}

func TestModelUsecase_Delete(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)
	m := &Model{ModelID: "del", DisplayName: "Del"}
	_ = uc.CreateModel(context.Background(), m)

	if err := uc.DeleteModel(context.Background(), m.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if _, err := uc.GetModelByID(context.Background(), "del"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("expected ErrModelNotFound after delete, got %v", err)
	}
}

func TestModelUsecase_ChangeStatus(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)
	m := &Model{ModelID: "s", DisplayName: "S"}
	_ = uc.CreateModel(context.Background(), m)

	if err := uc.ChangeModelStatus(context.Background(), m.ID, ModelStatusDisabled); err != nil {
		t.Fatalf("ChangeModelStatus: %v", err)
	}
	got, _ := uc.GetModelByID(context.Background(), "s")
	if got.Status != ModelStatusDisabled {
		t.Fatalf("expected disabled, got %d", got.Status)
	}
}

func TestModelUsecase_BatchEnable(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)
	var pks []int64
	for i := 0; i < 3; i++ {
		m := &Model{ModelID: "m" + string(rune('A'+i)), DisplayName: "M"}
		_ = uc.CreateModel(context.Background(), m)
		pks = append(pks, m.ID)
	}
	// disable first
	_, _ = uc.BatchModels(context.Background(), BatchActionDisable, pks)
	affected, err := uc.BatchModels(context.Background(), BatchActionEnable, pks)
	if err != nil {
		t.Fatalf("BatchModels enable: %v", err)
	}
	if affected != int32(len(pks)) {
		t.Fatalf("expected %d affected, got %d", len(pks), affected)
	}
}

func TestModelUsecase_BatchInvalidAction(t *testing.T) {
	uc := NewModelUsecase(newFakeModelRepo())
	_, err := uc.BatchModels(context.Background(), "bogus", []int64{1})
	if !errors.Is(err, ErrInvalidBatchAction) {
		t.Fatalf("expected ErrInvalidBatchAction, got %v", err)
	}
}

func TestModelUsecase_AliasCRUD(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)
	m := &Model{ModelID: "alias-test", DisplayName: "AT"}
	_ = uc.CreateModel(context.Background(), m)

	alias := &ModelAlias{ModelPK: m.ID, Alias: "at"}
	if err := uc.CreateModelAlias(context.Background(), alias); err != nil {
		t.Fatalf("CreateModelAlias: %v", err)
	}
	if alias.ID == 0 {
		t.Fatal("expected alias ID")
	}

	aliases, _ := uc.ListModelAliases(context.Background(), m.ID)
	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}

	// duplicate alias
	if err := uc.CreateModelAlias(context.Background(), &ModelAlias{ModelPK: m.ID, Alias: "at"}); !errors.Is(err, ErrAliasExists) {
		t.Fatalf("expected ErrAliasExists, got %v", err)
	}

	if err := uc.DeleteModelAlias(context.Background(), alias.ID); err != nil {
		t.Fatalf("DeleteModelAlias: %v", err)
	}
	if err := uc.DeleteModelAlias(context.Background(), alias.ID); !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("expected ErrAliasNotFound, got %v", err)
	}
}

func TestModelUsecase_ChannelMappingUpsert(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)
	m := &Model{ModelID: "cm", DisplayName: "CM"}
	_ = uc.CreateModel(context.Background(), m)

	mapping := &ModelChannelMapping{ChannelID: 10, ModelPK: m.ID, Enabled: true, Priority: 5}
	if err := uc.UpsertChannelMapping(context.Background(), mapping); err != nil {
		t.Fatalf("UpsertChannelMapping: %v", err)
	}

	// upsert again — should update, not duplicate
	mapping2 := &ModelChannelMapping{ChannelID: 10, ModelPK: m.ID, Enabled: false, Priority: 9}
	if err := uc.UpsertChannelMapping(context.Background(), mapping2); err != nil {
		t.Fatalf("UpsertChannelMapping (update): %v", err)
	}
	mappings, _ := uc.ListChannelMappings(context.Background(), 10)
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping after upsert, got %d", len(mappings))
	}
	if mappings[0].Enabled {
		t.Fatal("expected enabled=false after update")
	}

	if err := uc.DeleteChannelMapping(context.Background(), 10, m.ID); err != nil {
		t.Fatalf("DeleteChannelMapping: %v", err)
	}
	if err := uc.DeleteChannelMapping(context.Background(), 10, m.ID); !errors.Is(err, ErrMappingNotFound) {
		t.Fatalf("expected ErrMappingNotFound, got %v", err)
	}
}

func TestModelUsecase_SubscriptionMappingUpsert(t *testing.T) {
	repo := newFakeModelRepo()
	uc := NewModelUsecase(repo)
	m := &Model{ModelID: "sm", DisplayName: "SM"}
	_ = uc.CreateModel(context.Background(), m)

	mapping := &ModelSubscriptionMapping{SubscriptionAccountID: 20, ModelPK: m.ID, GroupName: "default", Enabled: true}
	if err := uc.UpsertSubscriptionMapping(context.Background(), mapping); err != nil {
		t.Fatalf("UpsertSubscriptionMapping: %v", err)
	}
	if mapping.GroupName != "default" {
		t.Fatalf("expected default group, got %s", mapping.GroupName)
	}
	mappings, _ := uc.ListSubscriptionMappings(context.Background(), 20)
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	if err := uc.DeleteSubscriptionMapping(context.Background(), 20, m.ID, "default"); err != nil {
		t.Fatalf("DeleteSubscriptionMapping: %v", err)
	}
}

func TestModelUsecase_NilSafety(t *testing.T) {
	var uc *ModelUsecase
	if _, _, err := uc.ListModels(context.Background(), 1, 10, ListModelsFilter{}); err != nil {
		t.Fatalf("nil uc ListModels should return nil err, got %v", err)
	}
	if err := uc.CreateModel(context.Background(), &Model{ModelID: "x"}); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("nil uc CreateModel should return ErrModelNotFound, got %v", err)
	}
}
