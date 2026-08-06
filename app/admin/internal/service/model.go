package service

import (
	"context"
	"strings"

	"micro-one-api/pkg/jsonx"

	"google.golang.org/protobuf/types/known/emptypb"

	adminv1 "micro-one-api/api/admin/v1"
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

// ── Model routing (P2 #3, passthrough channel-service) ────────────────────
//
// admin-api owns its own request/response types (adminv1) but forwards to
// channel-service (channelv1), converting at the boundary so the admin gRPC
// surface stays decoupled from the channel proto package.

// ListModelRoutings lists model→account routing overrides.
func (s *AdminService) ListModelRoutings(ctx context.Context, req *adminv1.ListModelRoutingsRequest) (*adminv1.ListModelRoutingsResponse, error) {
	resp, err := s.channelClient.ListModelRoutings(ctx, &channelv1.ListModelRoutingsRequest{
		GroupName: req.GroupName,
		Model:     req.Model,
		Platform:  req.Platform,
	})
	if err != nil {
		return nil, err
	}
	out := &adminv1.ListModelRoutingsResponse{}
	if resp != nil {
		out.Routings = make([]*adminv1.ModelRouting, 0, len(resp.GetRoutings()))
		for _, r := range resp.GetRoutings() {
			out.Routings = append(out.Routings, channelToAdminModelRouting(r))
		}
	}
	return out, nil
}

// UpsertModelRouting creates or updates a routing override.
func (s *AdminService) UpsertModelRouting(ctx context.Context, req *adminv1.UpsertModelRoutingRequest) (*adminv1.UpsertModelRoutingResponse, error) {
	resp, err := s.channelClient.UpsertModelRouting(ctx, &channelv1.UpsertModelRoutingRequest{
		GroupName:             req.GroupName,
		Model:                 req.Model,
		Platform:              req.Platform,
		SubscriptionAccountId: req.SubscriptionAccountId,
		Enabled:               req.Enabled, // admin.proto UpsertModelRoutingRequest.enabled is optional bool; pointer threaded through.
		Priority:              req.Priority,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &adminv1.UpsertModelRoutingResponse{}, nil
	}
	return &adminv1.UpsertModelRoutingResponse{
		Success: resp.GetSuccess(),
		Message: resp.GetMessage(),
		Id:      resp.GetId(),
	}, nil
}

// DeleteModelRouting removes a routing override.
func (s *AdminService) DeleteModelRouting(ctx context.Context, req *adminv1.DeleteModelRoutingRequest) (*adminv1.DeleteModelRoutingResponse, error) {
	resp, err := s.channelClient.DeleteModelRouting(ctx, &channelv1.DeleteModelRoutingRequest{Id: req.Id})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &adminv1.DeleteModelRoutingResponse{}, nil
	}
	return &adminv1.DeleteModelRoutingResponse{
		Success: resp.GetSuccess(),
		Message: resp.GetMessage(),
	}, nil
}

// channelToAdminModelRouting maps a channel-service ModelRouting DTO to the
// admin-service DTO. The two protos share the same shape by design; the
// boundary conversion keeps the admin API decoupled from the channel proto.
func channelToAdminModelRouting(r *channelv1.ModelRouting) *adminv1.ModelRouting {
	if r == nil {
		return nil
	}
	return &adminv1.ModelRouting{
		Id:                    r.GetId(),
		GroupName:             r.GetGroupName(),
		Model:                 r.GetModel(),
		Platform:              r.GetPlatform(),
		SubscriptionAccountId: r.GetSubscriptionAccountId(),
		Enabled:               r.GetEnabled(),
		Priority:              r.GetPriority(),
		CreatedAt:             r.GetCreatedAt(),
		UpdatedAt:             r.GetUpdatedAt(),
	}
}

// ── Canonical model ID governance (v0.11.0 Phase 2 §2.1) ──────────────────

// CanonicalModelPreflight returns the read-only duplicate report, enriched
// with the pricing-config keys (ModelPrice / UpstreamModelPrice) that reference
// each duplicate member. channel-service owns the model registry and computes
// the duplicate groups; admin-api owns system_options and attaches the price
// references afterwards so channel biz stays decoupled from pricing storage
// (v0.11.0 Phase 2 §2.1).
func (s *AdminService) CanonicalModelPreflight(ctx context.Context, in *emptypb.Empty) (*channelv1.CanonicalModelPreflightResponse, error) {
	resp, err := s.channelClient.CanonicalModelPreflight(ctx, in)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return resp, nil
	}
	priceKeys := s.loadPricingConfigKeys(ctx)
	if len(priceKeys) == 0 {
		return resp, nil
	}
	for _, g := range resp.GetGroups() {
		if g == nil {
			continue
		}
		for _, m := range g.GetMembers() {
			if m == nil {
				continue
			}
			m.PriceReferences = matchingPriceKeys(m.GetModelId(), priceKeys)
		}
	}
	return resp, nil
}

// loadPricingConfigKeys loads the union of ModelPrice and UpstreamModelPrice
// keys from system_options. Returns nil when storage is not configured or the
// options are absent/unparseable.
func (s *AdminService) loadPricingConfigKeys(ctx context.Context) map[string]struct{} {
	out := map[string]struct{}{}
	for _, key := range []string{"ModelPrice", "UpstreamModelPrice"} {
		raw, err := s.GetSystemOption(ctx, key)
		if err != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		var parsed map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal([]byte(raw), &parsed); err != nil {
			continue
		}
		for k := range parsed {
			if k = strings.TrimSpace(k); k != "" {
				out[k] = struct{}{}
			}
		}
	}
	return out
}

// matchingPriceKeys returns the pricing-config keys that reference the given
// model id (matching both the stored spelling and its canonical lowercase form).
func matchingPriceKeys(modelID string, priceKeys map[string]struct{}) []string {
	if len(priceKeys) == 0 {
		return nil
	}
	out := []string{}
	seen := map[string]struct{}{}
	add := func(k string) {
		if k == "" {
			return
		}
		if _, ok := seen[k]; ok {
			return
		}
		if _, ok := priceKeys[k]; ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	add(modelID)
	add(strings.ToLower(strings.TrimSpace(modelID)))
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeCanonicalModels merges a duplicate group onto a survivor.
func (s *AdminService) MergeCanonicalModels(ctx context.Context, req *channelv1.MergeCanonicalModelsRequest) (*channelv1.MergeCanonicalModelsResponse, error) {
	return s.channelClient.MergeCanonicalModels(ctx, req)
}

// ListUnpricedRoutedModels returns the routed-but-unpriced audit. The admin
// caller is responsible for loading the ModelPrice config, canonicalising its
// keys, and passing them as priced_model_ids; channel-service owns the model
// registry and computes the diff.
func (s *AdminService) ListUnpricedRoutedModels(ctx context.Context, req *channelv1.ListUnpricedRoutedModelsRequest) (*channelv1.ListUnpricedRoutedModelsResponse, error) {
	return s.channelClient.ListUnpricedRoutedModels(ctx, req)
}

// GetSystemOption reads a single system_options value by key. Returns "" when
// the option is absent or system-options storage is not configured.
func (s *AdminService) GetSystemOption(ctx context.Context, key string) (string, error) {
	if s == nil || s.systemOptsUc == nil {
		return "", nil
	}
	return s.systemOptsUc.Get(ctx, key)
}

// ── Model import/export passthrough (v0.11.0 Phase 4) ──────────────────────
// Admin-api proxies the exchange RPCs to channel-service, mirroring the
// existing model-management passthrough. The admin HTTP layer enforces the
// admin/root role check for export_prices/import_prices; channel-service
// performs the actual read/write. Prices are only forwarded when the caller
// has the required role, so a misconfigured client cannot leak pricing.

// ExportModels exports the model registry as a versioned document.
func (s *AdminService) ExportModels(ctx context.Context, req *channelv1.ExportModelsRequest) (*channelv1.ExportModelsResponse, error) {
	return s.channelClient.ExportModels(ctx, req)
}

// ImportModels applies an import document in one transaction.
func (s *AdminService) ImportModels(ctx context.Context, req *channelv1.ImportModelsRequest) (*channelv1.ImportModelsResponse, error) {
	return s.channelClient.ImportModels(ctx, req)
}

// DryRunImportModels previews an import without writing.
func (s *AdminService) DryRunImportModels(ctx context.Context, req *channelv1.ImportModelsRequest) (*channelv1.ImportModelsDryRunResponse, error) {
	return s.channelClient.DryRunImportModels(ctx, req)
}
