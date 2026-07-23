package service

import (
	"context"

	channelv1 "micro-one-api/api/channel/v1"
)

// ── Model management passthrough (方案B) ────────────────────────────────────
// Admin-api proxies model management RPCs to channel-service, mirroring the
// existing channel/subscription-account passthrough pattern. The admin service
// is a thin DTO forwarder — no business rules, no storage access.

// ListModels lists models from the registry.
func (s *AdminService) ListModels(ctx context.Context, req *channelv1.ListModelsRequest) (*channelv1.ListModelsResponse, error) {
	return s.channelClient.ListModels(ctx, req)
}

// GetModel retrieves a model by pk or model_id.
func (s *AdminService) GetModel(ctx context.Context, req *channelv1.GetModelRequest) (*channelv1.GetModelResponse, error) {
	return s.channelClient.GetModel(ctx, req)
}

// CreateModel creates a new model.
func (s *AdminService) CreateModel(ctx context.Context, req *channelv1.CreateModelRequest) (*channelv1.CreateModelResponse, error) {
	return s.channelClient.CreateModel(ctx, req)
}

// UpdateModel updates an existing model.
func (s *AdminService) UpdateModel(ctx context.Context, req *channelv1.UpdateModelRequest) (*channelv1.UpdateModelResponse, error) {
	return s.channelClient.UpdateModel(ctx, req)
}

// DeleteModel deletes a model.
func (s *AdminService) DeleteModel(ctx context.Context, req *channelv1.DeleteModelRequest) (*channelv1.DeleteModelResponse, error) {
	return s.channelClient.DeleteModel(ctx, req)
}

// ChangeModelStatus changes a model's status.
func (s *AdminService) ChangeModelStatus(ctx context.Context, req *channelv1.ChangeModelStatusRequest) (*channelv1.ChangeModelStatusResponse, error) {
	return s.channelClient.ChangeModelStatus(ctx, req)
}

// BatchModels performs a batch action on models.
func (s *AdminService) BatchModels(ctx context.Context, req *channelv1.BatchModelsRequest) (*channelv1.BatchModelsResponse, error) {
	return s.channelClient.BatchModels(ctx, req)
}

// ListModelAliases lists aliases for a model.
func (s *AdminService) ListModelAliases(ctx context.Context, req *channelv1.ListModelAliasesRequest) (*channelv1.ListModelAliasesResponse, error) {
	return s.channelClient.ListModelAliases(ctx, req)
}

// CreateModelAlias adds an alias.
func (s *AdminService) CreateModelAlias(ctx context.Context, req *channelv1.CreateModelAliasRequest) (*channelv1.CreateModelAliasResponse, error) {
	return s.channelClient.CreateModelAlias(ctx, req)
}

// DeleteModelAlias removes an alias.
func (s *AdminService) DeleteModelAlias(ctx context.Context, req *channelv1.DeleteModelAliasRequest) (*channelv1.DeleteModelAliasResponse, error) {
	return s.channelClient.DeleteModelAlias(ctx, req)
}

// ListChannelModelMappings lists channel-model mappings.
func (s *AdminService) ListChannelModelMappings(ctx context.Context, req *channelv1.ListChannelModelMappingsRequest) (*channelv1.ListChannelModelMappingsResponse, error) {
	return s.channelClient.ListChannelModelMappings(ctx, req)
}

// UpsertChannelModelMapping creates or updates a channel-model mapping.
func (s *AdminService) UpsertChannelModelMapping(ctx context.Context, req *channelv1.UpsertChannelModelMappingRequest) (*channelv1.UpsertChannelModelMappingResponse, error) {
	return s.channelClient.UpsertChannelModelMapping(ctx, req)
}

// DeleteChannelModelMapping removes a channel-model mapping.
func (s *AdminService) DeleteChannelModelMapping(ctx context.Context, req *channelv1.DeleteChannelModelMappingRequest) (*channelv1.DeleteChannelModelMappingResponse, error) {
	return s.channelClient.DeleteChannelModelMapping(ctx, req)
}

// ListSubscriptionModelMappings lists subscription-model mappings.
func (s *AdminService) ListSubscriptionModelMappings(ctx context.Context, req *channelv1.ListSubscriptionModelMappingsRequest) (*channelv1.ListSubscriptionModelMappingsResponse, error) {
	return s.channelClient.ListSubscriptionModelMappings(ctx, req)
}

// UpsertSubscriptionModelMapping creates or updates a subscription-model mapping.
func (s *AdminService) UpsertSubscriptionModelMapping(ctx context.Context, req *channelv1.UpsertSubscriptionModelMappingRequest) (*channelv1.UpsertSubscriptionModelMappingResponse, error) {
	return s.channelClient.UpsertSubscriptionModelMapping(ctx, req)
}

// DeleteSubscriptionModelMapping removes a subscription-model mapping.
func (s *AdminService) DeleteSubscriptionModelMapping(ctx context.Context, req *channelv1.DeleteSubscriptionModelMappingRequest) (*channelv1.DeleteSubscriptionModelMappingResponse, error) {
	return s.channelClient.DeleteSubscriptionModelMapping(ctx, req)
}

// ── Sprint 4: Usage statistics ─────────────────────────────────────────────

// RecordModelUsage records a usage event for a model.
func (s *AdminService) RecordModelUsage(ctx context.Context, req *channelv1.RecordModelUsageRequest) (*channelv1.RecordModelUsageResponse, error) {
	return s.channelClient.RecordModelUsage(ctx, req)
}

// ListModelUsageStats lists usage statistics for models.
func (s *AdminService) ListModelUsageStats(ctx context.Context, req *channelv1.ListModelUsageStatsRequest) (*channelv1.ListModelUsageStatsResponse, error) {
	return s.channelClient.ListModelUsageStats(ctx, req)
}
