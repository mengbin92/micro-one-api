package service

import (
	"context"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/errors"
)

// ── DTO ↔ DO conversion helpers ────────────────────────────────────────────

func toModelInfo(m *biz.Model) *channelv1.ModelInfo {
	if m == nil {
		return nil
	}
	return &channelv1.ModelInfo{
		Id:                m.ID,
		ModelId:           m.ModelID,
		DisplayName:       m.DisplayName,
		Description:       m.Description,
		Provider:          m.Provider,
		ModelType:         m.ModelType,
		ContextWindow:     m.ContextWindow,
		PricingInput:      m.PricingInput,
		PricingOutput:     m.PricingOutput,
		Status:            m.Status,
		IsPublic:          m.IsPublic,
		Capabilities:      append([]string(nil), m.Capabilities...),
		Tags:              append([]string(nil), m.Tags...),
		Category:          m.Category,
		Tier:              m.Tier,
		Metadata:          m.Metadata,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
		ChannelCount:      m.ChannelCount,
		SubscriptionCount: m.SubscriptionCount,
	}
}

func toModelSummary(m *biz.Model) *channelv1.ModelSummary {
	if m == nil {
		return nil
	}
	return &channelv1.ModelSummary{
		Id:                m.ID,
		ModelId:           m.ModelID,
		DisplayName:       m.DisplayName,
		Provider:          m.Provider,
		ModelType:         m.ModelType,
		Status:            m.Status,
		Category:          m.Category,
		Tier:              m.Tier,
		IsPublic:          m.IsPublic,
		ChannelCount:      m.ChannelCount,
		SubscriptionCount: m.SubscriptionCount,
	}
}

func toModelAliasProto(a *biz.ModelAlias) *channelv1.ModelAlias {
	if a == nil {
		return nil
	}
	return &channelv1.ModelAlias{
		Id:        a.ID,
		ModelPk:   a.ModelPK,
		Alias:     a.Alias,
		IsPrimary: a.IsPrimary,
		CreatedAt: a.CreatedAt,
	}
}

func toChannelMappingProto(m *biz.ModelChannelMapping) *channelv1.ModelChannelMapping {
	if m == nil {
		return nil
	}
	return &channelv1.ModelChannelMapping{
		Id:        m.ID,
		ChannelId: m.ChannelID,
		ModelPk:   m.ModelPK,
		Enabled:   m.Enabled,
		Priority:  m.Priority,
		Config:    m.Config,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func toSubscriptionMappingProto(m *biz.ModelSubscriptionMapping) *channelv1.ModelSubscriptionMapping {
	if m == nil {
		return nil
	}
	return &channelv1.ModelSubscriptionMapping{
		Id:                    m.ID,
		SubscriptionAccountId: m.SubscriptionAccountID,
		ModelPk:               m.ModelPK,
		GroupName:             m.GroupName,
		Enabled:               m.Enabled,
		Priority:              m.Priority,
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
	}
}

func toUsageStatProto(s *biz.ModelUsageStat) *channelv1.ModelUsageStat {
	if s == nil {
		return nil
	}
	return &channelv1.ModelUsageStat{
		Id:           s.ID,
		ModelPk:      s.ModelPK,
		Date:         s.Date,
		RequestCount: s.RequestCount,
		TokenCount:   s.TokenCount,
		ErrorCount:   s.ErrorCount,
		AvgLatency:   s.AvgLatency,
	}
}

func mapModelError(err error) error {
	return errors.MapChannelError(err)
}

// ── gRPC handlers ──────────────────────────────────────────────────────────

func (s *ChannelService) modelUc() *biz.ModelUsecase {
	if s == nil || s.modelUC == nil {
		return nil
	}
	return s.modelUC
}

func (s *ChannelService) ListModels(ctx context.Context, req *channelv1.ListModelsRequest) (*channelv1.ListModelsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ListModelsResponse{Models: []*channelv1.ModelSummary{}, Total: 0}, nil
	}
	models, total, err := uc.ListModels(ctx, req.Page, req.PageSize, biz.ListModelsFilter{
		Keyword:    req.Keyword,
		Provider:   req.Provider,
		ModelType:  req.ModelType,
		Status:     req.Status,
		Category:   req.Category,
		Tier:       req.Tier,
		PublicOnly: req.PublicOnly,
	})
	if err != nil {
		return nil, mapModelError(err)
	}
	result := make([]*channelv1.ModelSummary, 0, len(models))
	for _, m := range models {
		result = append(result, toModelSummary(m))
	}
	return &channelv1.ListModelsResponse{Models: result, Total: total}, nil
}

func (s *ChannelService) GetModel(ctx context.Context, req *channelv1.GetModelRequest) (*channelv1.GetModelResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return nil, mapModelError(biz.ErrModelNotFound)
	}
	var (
		model       *biz.Model
		aliases     []*biz.ModelAlias
		chMappings  []*biz.ModelChannelMapping
		subMappings []*biz.ModelSubscriptionMapping
		err         error
	)
	if req.ModelPk > 0 {
		model, aliases, chMappings, subMappings, err = uc.GetModel(ctx, req.ModelPk)
	} else if req.ModelId != "" {
		model, err = uc.GetModelByID(ctx, req.ModelId)
		if err == nil {
			aliases, _ = uc.ListModelAliases(ctx, model.ID)
		}
	} else {
		return nil, mapModelError(biz.ErrModelNotFound)
	}
	if err != nil {
		return nil, mapModelError(err)
	}
	resp := &channelv1.GetModelResponse{
		Model: toModelInfo(model),
	}
	for _, a := range aliases {
		resp.Aliases = append(resp.Aliases, toModelAliasProto(a))
	}
	for _, m := range chMappings {
		resp.ChannelMappings = append(resp.ChannelMappings, toChannelMappingProto(m))
	}
	for _, m := range subMappings {
		resp.SubscriptionMappings = append(resp.SubscriptionMappings, toSubscriptionMappingProto(m))
	}
	return resp, nil
}

func (s *ChannelService) CreateModel(ctx context.Context, req *channelv1.CreateModelRequest) (*channelv1.CreateModelResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.CreateModelResponse{Success: false, Message: "model management not configured"}, nil
	}
	model := &biz.Model{
		ModelID:       req.ModelId,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		Provider:      req.Provider,
		ModelType:     req.ModelType,
		ContextWindow: req.ContextWindow,
		PricingInput:  req.PricingInput,
		PricingOutput: req.PricingOutput,
		Status:        req.Status,
		IsPublic:      req.IsPublic,
		Capabilities:  append([]string(nil), req.Capabilities...),
		Tags:          append([]string(nil), req.Tags...),
		Category:      req.Category,
		Tier:          req.Tier,
		Metadata:      req.Metadata,
	}
	if err := uc.CreateModel(ctx, model); err != nil {
		return &channelv1.CreateModelResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.CreateModelResponse{Success: true, Message: "ok", ModelPk: model.ID}, nil
}

func (s *ChannelService) UpdateModel(ctx context.Context, req *channelv1.UpdateModelRequest) (*channelv1.UpdateModelResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.UpdateModelResponse{Success: false, Message: "model management not configured"}, nil
	}
	model := &biz.Model{
		ID:            req.ModelPk,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		Provider:      req.Provider,
		ModelType:     req.ModelType,
		ContextWindow: req.ContextWindow,
		PricingInput:  req.PricingInput,
		PricingOutput: req.PricingOutput,
		IsPublic:      req.IsPublic,
		Capabilities:  append([]string(nil), req.Capabilities...),
		Tags:          append([]string(nil), req.Tags...),
		Category:      req.Category,
		Tier:          req.Tier,
		Metadata:      req.Metadata,
	}
	if err := uc.UpdateModel(ctx, model); err != nil {
		return &channelv1.UpdateModelResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.UpdateModelResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) DeleteModel(ctx context.Context, req *channelv1.DeleteModelRequest) (*channelv1.DeleteModelResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.DeleteModelResponse{Success: false, Message: "model management not configured"}, nil
	}
	if err := uc.DeleteModel(ctx, req.ModelPk); err != nil {
		return &channelv1.DeleteModelResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.DeleteModelResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) ChangeModelStatus(ctx context.Context, req *channelv1.ChangeModelStatusRequest) (*channelv1.ChangeModelStatusResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ChangeModelStatusResponse{Success: false, Message: "model management not configured"}, nil
	}
	if err := uc.ChangeModelStatus(ctx, req.ModelPk, req.Status); err != nil {
		return &channelv1.ChangeModelStatusResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.ChangeModelStatusResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) BatchModels(ctx context.Context, req *channelv1.BatchModelsRequest) (*channelv1.BatchModelsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.BatchModelsResponse{Success: false, Message: "model management not configured"}, nil
	}
	affected, err := uc.BatchModels(ctx, req.Action, req.ModelPks)
	if err != nil {
		return &channelv1.BatchModelsResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.BatchModelsResponse{Success: true, Message: "ok", Affected: affected}, nil
}

// ── Aliases ────────────────────────────────────────────────────────────────

func (s *ChannelService) ListModelAliases(ctx context.Context, req *channelv1.ListModelAliasesRequest) (*channelv1.ListModelAliasesResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ListModelAliasesResponse{Aliases: []*channelv1.ModelAlias{}}, nil
	}
	aliases, err := uc.ListModelAliases(ctx, req.ModelPk)
	if err != nil {
		return nil, mapModelError(err)
	}
	result := make([]*channelv1.ModelAlias, 0, len(aliases))
	for _, a := range aliases {
		result = append(result, toModelAliasProto(a))
	}
	return &channelv1.ListModelAliasesResponse{Aliases: result}, nil
}

func (s *ChannelService) CreateModelAlias(ctx context.Context, req *channelv1.CreateModelAliasRequest) (*channelv1.CreateModelAliasResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.CreateModelAliasResponse{Success: false, Message: "model management not configured"}, nil
	}
	alias := &biz.ModelAlias{
		ModelPK:   req.ModelPk,
		Alias:     req.Alias,
		IsPrimary: req.IsPrimary,
	}
	if err := uc.CreateModelAlias(ctx, alias); err != nil {
		return &channelv1.CreateModelAliasResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.CreateModelAliasResponse{Success: true, Message: "ok", AliasId: alias.ID}, nil
}

func (s *ChannelService) DeleteModelAlias(ctx context.Context, req *channelv1.DeleteModelAliasRequest) (*channelv1.DeleteModelAliasResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.DeleteModelAliasResponse{Success: false, Message: "model management not configured"}, nil
	}
	if err := uc.DeleteModelAlias(ctx, req.AliasId); err != nil {
		return &channelv1.DeleteModelAliasResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.DeleteModelAliasResponse{Success: true, Message: "ok"}, nil
}

// ── Channel mappings ───────────────────────────────────────────────────────

func (s *ChannelService) ListChannelModelMappings(ctx context.Context, req *channelv1.ListChannelModelMappingsRequest) (*channelv1.ListChannelModelMappingsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ListChannelModelMappingsResponse{Mappings: []*channelv1.ModelChannelMapping{}}, nil
	}
	mappings, err := uc.ListChannelMappings(ctx, req.ChannelId)
	if err != nil {
		return nil, mapModelError(err)
	}
	result := make([]*channelv1.ModelChannelMapping, 0, len(mappings))
	for _, m := range mappings {
		result = append(result, toChannelMappingProto(m))
	}
	return &channelv1.ListChannelModelMappingsResponse{Mappings: result}, nil
}

func (s *ChannelService) UpsertChannelModelMapping(ctx context.Context, req *channelv1.UpsertChannelModelMappingRequest) (*channelv1.UpsertChannelModelMappingResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.UpsertChannelModelMappingResponse{Success: false, Message: "model management not configured"}, nil
	}
	m := &biz.ModelChannelMapping{
		ChannelID: req.ChannelId,
		ModelPK:   req.ModelPk,
		Enabled:   req.Enabled,
		Priority:  req.Priority,
		Config:    req.Config,
	}
	if err := uc.UpsertChannelMapping(ctx, m); err != nil {
		return &channelv1.UpsertChannelModelMappingResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.UpsertChannelModelMappingResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) DeleteChannelModelMapping(ctx context.Context, req *channelv1.DeleteChannelModelMappingRequest) (*channelv1.DeleteChannelModelMappingResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.DeleteChannelModelMappingResponse{Success: false, Message: "model management not configured"}, nil
	}
	if err := uc.DeleteChannelMapping(ctx, req.ChannelId, req.ModelPk); err != nil {
		return &channelv1.DeleteChannelModelMappingResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.DeleteChannelModelMappingResponse{Success: true, Message: "ok"}, nil
}

// ── Subscription mappings ──────────────────────────────────────────────────

func (s *ChannelService) ListSubscriptionModelMappings(ctx context.Context, req *channelv1.ListSubscriptionModelMappingsRequest) (*channelv1.ListSubscriptionModelMappingsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ListSubscriptionModelMappingsResponse{Mappings: []*channelv1.ModelSubscriptionMapping{}}, nil
	}
	mappings, err := uc.ListSubscriptionMappings(ctx, req.SubscriptionAccountId)
	if err != nil {
		return nil, mapModelError(err)
	}
	result := make([]*channelv1.ModelSubscriptionMapping, 0, len(mappings))
	for _, m := range mappings {
		result = append(result, toSubscriptionMappingProto(m))
	}
	return &channelv1.ListSubscriptionModelMappingsResponse{Mappings: result}, nil
}

func (s *ChannelService) UpsertSubscriptionModelMapping(ctx context.Context, req *channelv1.UpsertSubscriptionModelMappingRequest) (*channelv1.UpsertSubscriptionModelMappingResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.UpsertSubscriptionModelMappingResponse{Success: false, Message: "model management not configured"}, nil
	}
	m := &biz.ModelSubscriptionMapping{
		SubscriptionAccountID: req.SubscriptionAccountId,
		ModelPK:               req.ModelPk,
		GroupName:             req.GroupName,
		Enabled:               req.Enabled,
		Priority:              req.Priority,
	}
	if err := uc.UpsertSubscriptionMapping(ctx, m); err != nil {
		return &channelv1.UpsertSubscriptionModelMappingResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.UpsertSubscriptionModelMappingResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) DeleteSubscriptionModelMapping(ctx context.Context, req *channelv1.DeleteSubscriptionModelMappingRequest) (*channelv1.DeleteSubscriptionModelMappingResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.DeleteSubscriptionModelMappingResponse{Success: false, Message: "model management not configured"}, nil
	}
	if err := uc.DeleteSubscriptionMapping(ctx, req.SubscriptionAccountId, req.ModelPk, req.GroupName); err != nil {
		return &channelv1.DeleteSubscriptionModelMappingResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.DeleteSubscriptionModelMappingResponse{Success: true, Message: "ok"}, nil
}

// ── Sprint 4: Usage statistics ─────────────────────────────────────────────

func (s *ChannelService) RecordModelUsage(ctx context.Context, req *channelv1.RecordModelUsageRequest) (*channelv1.RecordModelUsageResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.RecordModelUsageResponse{Success: false, Message: "model management not configured"}, nil
	}
	requestCount := req.RequestCount
	if requestCount == 0 {
		requestCount = 1
	}
	if err := uc.RecordModelUsage(ctx, req.ModelId, requestCount, req.TokenCount, req.ErrorCount, req.AvgLatency, req.Date); err != nil {
		return &channelv1.RecordModelUsageResponse{Success: false, Message: err.Error()}, nil
	}
	return &channelv1.RecordModelUsageResponse{Success: true, Message: "ok"}, nil
}

func (s *ChannelService) ListModelUsageStats(ctx context.Context, req *channelv1.ListModelUsageStatsRequest) (*channelv1.ListModelUsageStatsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ListModelUsageStatsResponse{Stats: []*channelv1.ModelUsageStat{}, Total: 0}, nil
	}
	stats, total, err := uc.ListModelUsageStats(ctx, req.ModelPk, req.StartDate, req.EndDate, req.Page, req.PageSize)
	if err != nil {
		return nil, mapModelError(err)
	}
	result := make([]*channelv1.ModelUsageStat, 0, len(stats))
	for _, s := range stats {
		result = append(result, toUsageStatProto(s))
	}
	return &channelv1.ListModelUsageStatsResponse{Stats: result, Total: total}, nil
}
