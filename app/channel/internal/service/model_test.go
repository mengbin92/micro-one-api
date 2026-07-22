package service

import (
	"context"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
)

// modelServiceRepo embeds the channel test repo so it satisfies ChannelRepo,
// and adds the ModelRepo methods for model service handler tests.
type modelServiceRepo struct {
	channelServiceRepo
	models  map[int64]*biz.Model
	nextID  int64
	aliases map[int64]*biz.ModelAlias
}

func newModelServiceRepo() *modelServiceRepo {
	return &modelServiceRepo{
		channelServiceRepo: channelServiceRepo{channel: &biz.Channel{ID: 1, Status: biz.ChannelStatusEnabled}},
		models:             make(map[int64]*biz.Model),
		aliases:            make(map[int64]*biz.ModelAlias),
	}
}

func (r *modelServiceRepo) ListModels(ctx context.Context, page, pageSize int32, filter biz.ListModelsFilter) ([]*biz.Model, int64, error) {
	var out []*biz.Model
	for _, m := range r.models {
		out = append(out, m)
	}
	return out, int64(len(out)), nil
}

func (r *modelServiceRepo) GetModel(ctx context.Context, modelPK int64) (*biz.Model, error) {
	m, ok := r.models[modelPK]
	if !ok {
		return nil, biz.ErrModelNotFound
	}
	return m, nil
}

func (r *modelServiceRepo) GetModelByID(ctx context.Context, modelID string) (*biz.Model, error) {
	for _, m := range r.models {
		if m.ModelID == modelID {
			return m, nil
		}
	}
	return nil, biz.ErrModelNotFound
}

func (r *modelServiceRepo) CreateModel(ctx context.Context, model *biz.Model) error {
	r.nextID++
	model.ID = r.nextID
	r.models[model.ID] = model
	return nil
}

func (r *modelServiceRepo) UpdateModel(ctx context.Context, model *biz.Model) error {
	if _, ok := r.models[model.ID]; !ok {
		return biz.ErrModelNotFound
	}
	r.models[model.ID] = model
	return nil
}

func (r *modelServiceRepo) DeleteModel(ctx context.Context, modelPK int64) error {
	delete(r.models, modelPK)
	return nil
}

func (r *modelServiceRepo) ChangeModelStatus(ctx context.Context, modelPK int64, status int32) error {
	if m, ok := r.models[modelPK]; ok {
		m.Status = status
	}
	return nil
}

func (r *modelServiceRepo) BatchChangeStatus(ctx context.Context, pks []int64, status int32) (int32, error) {
	var n int32
	for _, pk := range pks {
		if m, ok := r.models[pk]; ok {
			m.Status = status
			n++
		}
	}
	return n, nil
}

func (r *modelServiceRepo) BatchDelete(ctx context.Context, pks []int64) (int32, error) {
	var n int32
	for _, pk := range pks {
		if _, ok := r.models[pk]; ok {
			delete(r.models, pk)
			n++
		}
	}
	return n, nil
}

func (r *modelServiceRepo) ListModelAliases(ctx context.Context, modelPK int64) ([]*biz.ModelAlias, error) {
	var out []*biz.ModelAlias
	for _, a := range r.aliases {
		if modelPK == 0 || a.ModelPK == modelPK {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *modelServiceRepo) CreateModelAlias(ctx context.Context, alias *biz.ModelAlias) error {
	r.nextID++
	alias.ID = r.nextID
	r.aliases[alias.ID] = alias
	return nil
}

func (r *modelServiceRepo) DeleteModelAlias(ctx context.Context, aliasID int64) error {
	delete(r.aliases, aliasID)
	return nil
}

func (r *modelServiceRepo) ListChannelMappings(ctx context.Context, channelID int64) ([]*biz.ModelChannelMapping, error) {
	return nil, nil
}

func (r *modelServiceRepo) ListChannelMappingsByModel(ctx context.Context, modelPK int64) ([]*biz.ModelChannelMapping, error) {
	return nil, nil
}

func (r *modelServiceRepo) UpsertChannelMapping(ctx context.Context, m *biz.ModelChannelMapping) error {
	return nil
}

func (r *modelServiceRepo) DeleteChannelMapping(ctx context.Context, channelID, modelPK int64) error {
	return nil
}

func (r *modelServiceRepo) ListSubscriptionMappings(ctx context.Context, accountID int64) ([]*biz.ModelSubscriptionMapping, error) {
	return nil, nil
}

func (r *modelServiceRepo) ListSubscriptionMappingsByModel(ctx context.Context, modelPK int64) ([]*biz.ModelSubscriptionMapping, error) {
	return nil, nil
}

func (r *modelServiceRepo) UpsertSubscriptionMapping(ctx context.Context, m *biz.ModelSubscriptionMapping) error {
	return nil
}

func (r *modelServiceRepo) DeleteSubscriptionMapping(ctx context.Context, accountID, modelPK int64, groupName string) error {
	return nil
}

// Compile-time check.
var _ biz.ModelRepo = (*modelServiceRepo)(nil)

func newModelService() *ChannelService {
	repo := newModelServiceRepo()
	svc := NewChannelService(biz.NewChannelUsecase(repo, nil))
	svc.SetModelUsecase(biz.NewModelUsecase(repo))
	return svc
}

func TestChannelService_ListModels(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	// Empty list when no models.
	resp, err := svc.ListModels(ctx, &channelv1.ListModelsRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(resp.Models) != 0 || resp.Total != 0 {
		t.Fatalf("expected empty list, got %d models total=%d", len(resp.Models), resp.Total)
	}

	// Create a model.
	_, err = svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:     "gpt-4o",
		DisplayName: "GPT-4o",
		Provider:    "openai",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	resp, err = svc.ListModels(ctx, &channelv1.ListModelsRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if resp.Total != 1 || len(resp.Models) != 1 {
		t.Fatalf("expected 1 model, got total=%d len=%d", resp.Total, len(resp.Models))
	}
	if resp.Models[0].ModelId != "gpt-4o" {
		t.Fatalf("unexpected model_id: %s", resp.Models[0].ModelId)
	}
}

func TestChannelService_CreateAndGetModel(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	createResp, err := svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:       "claude-3-5-sonnet",
		DisplayName:   "Claude 3.5 Sonnet",
		Provider:      "anthropic",
		ModelType:     "chat",
		ContextWindow: 200000,
		Capabilities:  []string{"vision", "function_calling"},
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if !createResp.Success || createResp.ModelPk == 0 {
		t.Fatalf("expected success with model_pk, got %+v", createResp)
	}

	getResp, err := svc.GetModel(ctx, &channelv1.GetModelRequest{ModelPk: createResp.ModelPk})
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if getResp.Model.ModelId != "claude-3-5-sonnet" {
		t.Fatalf("unexpected model_id: %s", getResp.Model.ModelId)
	}
	if getResp.Model.ContextWindow != 200000 {
		t.Fatalf("unexpected context_window: %d", getResp.Model.ContextWindow)
	}
	if len(getResp.Model.Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(getResp.Model.Capabilities))
	}
}

func TestChannelService_GetModelByID(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	_, _ = svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:     "byid-test",
		DisplayName: "ByID",
	})

	resp, err := svc.GetModel(ctx, &channelv1.GetModelRequest{ModelId: "byid-test"})
	if err != nil {
		t.Fatalf("GetModel by id: %v", err)
	}
	if resp.Model.ModelId != "byid-test" {
		t.Fatalf("unexpected model_id: %s", resp.Model.ModelId)
	}
}

func TestChannelService_UpdateModel(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	createResp, _ := svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:     "upd",
		DisplayName: "Upd",
	})

	updateResp, err := svc.UpdateModel(ctx, &channelv1.UpdateModelRequest{
		ModelPk:     createResp.ModelPk,
		DisplayName: "Updated",
		Description: "new desc",
	})
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if !updateResp.Success {
		t.Fatalf("expected success: %+v", updateResp)
	}

	getResp, _ := svc.GetModel(ctx, &channelv1.GetModelRequest{ModelPk: createResp.ModelPk})
	if getResp.Model.DisplayName != "Updated" {
		t.Fatalf("expected Updated, got %s", getResp.Model.DisplayName)
	}
	if getResp.Model.Description != "new desc" {
		t.Fatalf("expected new desc, got %s", getResp.Model.Description)
	}
}

func TestChannelService_DeleteModel(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	createResp, _ := svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:     "del",
		DisplayName: "Del",
	})

	resp, err := svc.DeleteModel(ctx, &channelv1.DeleteModelRequest{ModelPk: createResp.ModelPk})
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success: %+v", resp)
	}
}

func TestChannelService_ChangeModelStatus(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	createResp, _ := svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:     "stat",
		DisplayName: "Stat",
		Status:      biz.ModelStatusEnabled,
	})

	resp, err := svc.ChangeModelStatus(ctx, &channelv1.ChangeModelStatusRequest{
		ModelPk: createResp.ModelPk,
		Status:  biz.ModelStatusDisabled,
	})
	if err != nil {
		t.Fatalf("ChangeModelStatus: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success: %+v", resp)
	}

	getResp, _ := svc.GetModel(ctx, &channelv1.GetModelRequest{ModelPk: createResp.ModelPk})
	if getResp.Model.Status != biz.ModelStatusDisabled {
		t.Fatalf("expected disabled, got %d", getResp.Model.Status)
	}
}

func TestChannelService_BatchModels(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	var pks []int64
	for i := 0; i < 3; i++ {
		resp, _ := svc.CreateModel(ctx, &channelv1.CreateModelRequest{
			ModelId:     "batch" + string(rune('A'+i)),
			DisplayName: "B",
			Status:      biz.ModelStatusEnabled,
		})
		pks = append(pks, resp.ModelPk)
	}

	resp, err := svc.BatchModels(ctx, &channelv1.BatchModelsRequest{
		Action:   "disable",
		ModelPks: pks,
	})
	if err != nil {
		t.Fatalf("BatchModels: %v", err)
	}
	if !resp.Success || resp.Affected != 3 {
		t.Fatalf("expected 3 affected, got %+v", resp)
	}
}

func TestChannelService_BatchModelsInvalidAction(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	resp, err := svc.BatchModels(ctx, &channelv1.BatchModelsRequest{
		Action:   "bogus",
		ModelPks: []int64{1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure for invalid action")
	}
}

func TestChannelService_AliasCRUD(t *testing.T) {
	svc := newModelService()
	ctx := context.Background()

	createResp, _ := svc.CreateModel(ctx, &channelv1.CreateModelRequest{
		ModelId:     "alias-svc",
		DisplayName: "AS",
	})

	aliasResp, err := svc.CreateModelAlias(ctx, &channelv1.CreateModelAliasRequest{
		ModelPk:   createResp.ModelPk,
		Alias:     "as",
		IsPrimary: true,
	})
	if err != nil {
		t.Fatalf("CreateModelAlias: %v", err)
	}
	if !aliasResp.Success || aliasResp.AliasId == 0 {
		t.Fatalf("expected success with alias_id: %+v", aliasResp)
	}

	listResp, err := svc.ListModelAliases(ctx, &channelv1.ListModelAliasesRequest{ModelPk: createResp.ModelPk})
	if err != nil {
		t.Fatalf("ListModelAliases: %v", err)
	}
	if len(listResp.Aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(listResp.Aliases))
	}

	delResp, err := svc.DeleteModelAlias(ctx, &channelv1.DeleteModelAliasRequest{AliasId: aliasResp.AliasId})
	if err != nil {
		t.Fatalf("DeleteModelAlias: %v", err)
	}
	if !delResp.Success {
		t.Fatal("expected success")
	}
}

func TestChannelService_NilModelUsecase(t *testing.T) {
	// A service without SetModelUsecase should return safe empty responses.
	repo := newModelServiceRepo()
	svc := NewChannelService(biz.NewChannelUsecase(repo, nil))
	// modelUC is nil

	resp, err := svc.ListModels(context.Background(), &channelv1.ListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels with nil uc should not error: %v", err)
	}
	if resp == nil || len(resp.Models) != 0 {
		t.Fatal("expected empty list with nil uc")
	}
}
