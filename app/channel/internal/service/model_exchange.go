package service

import (
	"context"
	"errors"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── Model import/export service layer (v0.11.0 Phase 4) ───────────────────
//
// These handlers are DTO↔DO pass-throughs on ChannelService (mirroring the
// existing model-management pattern). Validation of limits and canonical IDs
// happens in biz; the service only converts types and normalises the conflict
// strategy. No business rules, no storage access, no PO.
//
// Sensitive-field safety: ExportModels never reads channel keys or OAuth
// tokens — the biz/data path only selects model registry columns. The DO →
// DTO converter below explicitly omits any credential-shaped field even if a
// future column were added.

// ExportModels exports the model registry as a versioned document.
func (s *ChannelService) ExportModels(ctx context.Context, req *channelv1.ExportModelsRequest) (*channelv1.ExportModelsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ExportModelsResponse{
			SchemaVersion: biz.ModelExchangeSchemaVersion,
			Models:        []*channelv1.ModelExportModel{},
		}, nil
	}
	result, err := uc.ExportModels(ctx, biz.ListModelsFilter{
		Keyword:   req.GetKeyword(),
		Provider:  req.GetProvider(),
		ModelType: req.GetModelType(),
		Status:    req.GetStatus(),
		Category:  req.GetCategory(),
		Tier:      req.GetTier(),
	}, req.GetExportPrices())
	if err != nil {
		return nil, mapModelError(err)
	}
	models := make([]*channelv1.ModelExportModel, 0, len(result.Models))
	for _, m := range result.Models {
		models = append(models, exportModelToProto(m))
	}
	return &channelv1.ExportModelsResponse{
		SchemaVersion: result.SchemaVersion,
		ExportedAt:    result.ExportedAt,
		Models:        models,
		ContentHash:   result.ContentHash,
	}, nil
}

// ImportModels applies an import document in one transaction. A conflict under
// the reject strategy aborts the whole batch (no partial writes).
func (s *ChannelService) ImportModels(ctx context.Context, req *channelv1.ImportModelsRequest) (*channelv1.ImportModelsResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ImportModelsResponse{Success: false, Message: "model registry not enabled"}, nil
	}
	if req.GetSchemaVersion() != biz.ModelExchangeSchemaVersion {
		return nil, status.Error(codes.InvalidArgument, "schema version mismatch: expected "+biz.ModelExchangeSchemaVersion)
	}
	models := protoToExportModels(req.GetModels(), req.GetImportPrices())
	summary, err := uc.ImportModels(ctx, models, biz.ImportOptions{
		ConflictStrategy: parseConflictStrategy(req.GetConflictStrategy()),
		ImportPrices:     req.GetImportPrices(),
	})
	if err != nil {
		return nil, mapModelExchangeError(err)
	}
	return importSummaryToResponse(summary), nil
}

// DryRunImportModels previews an import without writing.
func (s *ChannelService) DryRunImportModels(ctx context.Context, req *channelv1.ImportModelsRequest) (*channelv1.ImportModelsDryRunResponse, error) {
	uc := s.modelUc()
	if uc == nil {
		return &channelv1.ImportModelsDryRunResponse{WouldSucceed: false, Message: "model registry not enabled"}, nil
	}
	if req.GetSchemaVersion() != biz.ModelExchangeSchemaVersion {
		return &channelv1.ImportModelsDryRunResponse{
			WouldSucceed: false,
			Message:      "schema version mismatch: expected " + biz.ModelExchangeSchemaVersion,
		}, nil
	}
	models := protoToExportModels(req.GetModels(), req.GetImportPrices())
	summary, err := uc.DryRunImportModels(ctx, models, biz.ImportOptions{
		ConflictStrategy: parseConflictStrategy(req.GetConflictStrategy()),
		ImportPrices:     req.GetImportPrices(),
	})
	if err != nil {
		return nil, mapModelExchangeError(err)
	}
	return &channelv1.ImportModelsDryRunResponse{
		WouldSucceed: !summary.HasErrors(),
		Message:      summaryMessage(summary),
		WouldCreate:  summary.Created,
		WouldUpdate:  summary.Updated,
		WouldSkip:    summary.Skipped,
		Conflicts:    summary.Conflicts,
		Errors:       summary.Errors,
		Results:      importResultsToProto(summary.Results),
	}, nil
}

func mapModelExchangeError(err error) error {
	switch {
	case errors.Is(err, biz.ErrImportConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, biz.ErrImportSchemaVersion),
		errors.Is(err, biz.ErrImportRecordLimit),
		errors.Is(err, biz.ErrImportInvalidRecord),
		errors.Is(err, biz.ErrInvalidConflictStrategy):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return mapModelError(err)
	}
}

// ── DTO ↔ DO conversion ───────────────────────────────────────────────────

func exportModelToProto(m *biz.ModelExportModel) *channelv1.ModelExportModel {
	if m == nil {
		return nil
	}
	out := &channelv1.ModelExportModel{
		ModelId:       m.ModelID,
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
	for _, a := range m.Aliases {
		if a != nil {
			out.Aliases = append(out.Aliases, &channelv1.ModelAlias{
				Alias:     a.Alias,
				IsPrimary: a.IsPrimary,
			})
		}
	}
	for _, c := range m.ChannelMappings {
		if c != nil {
			out.ChannelMappings = append(out.ChannelMappings, &channelv1.ModelExportChannelMapping{
				ChannelId:       c.ChannelID,
				Enabled:         c.Enabled,
				Priority:        c.Priority,
				Config:          c.Config,
				UpstreamModelId: c.UpstreamModelID,
			})
		}
	}
	for _, sm := range m.SubscriptionMappings {
		if sm != nil {
			out.SubscriptionMappings = append(out.SubscriptionMappings, &channelv1.ModelExportSubscriptionMapping{
				SubscriptionAccountId: sm.SubscriptionAccountID,
				GroupName:             sm.GroupName,
				Enabled:               sm.Enabled,
				Priority:              sm.Priority,
				UpstreamModelId:       sm.UpstreamModelID,
			})
		}
	}
	return out
}

func protoToExportModels(in []*channelv1.ModelExportModel, importPrices bool) []*biz.ModelExportModel {
	out := make([]*biz.ModelExportModel, 0, len(in))
	for _, m := range in {
		if m == nil {
			continue
		}
		do := &biz.ModelExportModel{
			ModelID:       m.GetModelId(),
			DisplayName:   m.GetDisplayName(),
			Description:   m.GetDescription(),
			Provider:      m.GetProvider(),
			ModelType:     m.GetModelType(),
			ContextWindow: m.GetContextWindow(),
			Status:        m.GetStatus(),
			IsPublic:      m.GetIsPublic(),
			Capabilities:  append([]string(nil), m.GetCapabilities()...),
			Tags:          append([]string(nil), m.GetTags()...),
			Category:      m.GetCategory(),
			Tier:          m.GetTier(),
			Metadata:      m.GetMetadata(),
		}
		if importPrices {
			do.PricingInput = m.GetPricingInput()
			do.PricingOutput = m.GetPricingOutput()
		}
		for _, a := range m.GetAliases() {
			if a != nil {
				do.Aliases = append(do.Aliases, &biz.ModelAlias{
					Alias:     a.GetAlias(),
					IsPrimary: a.GetIsPrimary(),
				})
			}
		}
		for _, c := range m.GetChannelMappings() {
			if c != nil {
				do.ChannelMappings = append(do.ChannelMappings, &biz.ModelChannelMapping{
					ChannelID:       c.GetChannelId(),
					Enabled:         c.GetEnabled(),
					Priority:        c.GetPriority(),
					Config:          c.GetConfig(),
					UpstreamModelID: c.GetUpstreamModelId(),
				})
			}
		}
		for _, sm := range m.GetSubscriptionMappings() {
			if sm != nil {
				do.SubscriptionMappings = append(do.SubscriptionMappings, &biz.ModelSubscriptionMapping{
					SubscriptionAccountID: sm.GetSubscriptionAccountId(),
					GroupName:             sm.GetGroupName(),
					Enabled:               sm.GetEnabled(),
					Priority:              sm.GetPriority(),
					UpstreamModelID:       sm.GetUpstreamModelId(),
				})
			}
		}
		out = append(out, do)
	}
	return out
}

func importSummaryToResponse(s *biz.ImportSummary) *channelv1.ImportModelsResponse {
	if s == nil {
		return &channelv1.ImportModelsResponse{}
	}
	return &channelv1.ImportModelsResponse{
		Success:   !s.HasErrors(),
		Message:   summaryMessage(s),
		Created:   s.Created,
		Updated:   s.Updated,
		Skipped:   s.Skipped,
		Conflicts: s.Conflicts,
		Errors:    s.Errors,
		Results:   importResultsToProto(s.Results),
	}
}

func importResultsToProto(in []biz.ImportRecordOutcome) []*channelv1.ImportRecordResult {
	out := make([]*channelv1.ImportRecordResult, 0, len(in))
	for _, r := range in {
		out = append(out, &channelv1.ImportRecordResult{
			ModelId: r.ModelID,
			Action:  r.Action,
			Message: r.Message,
		})
	}
	return out
}

func parseConflictStrategy(s string) biz.ImportConflictStrategy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "update":
		return biz.ConflictStrategyUpdate
	default:
		return biz.ConflictStrategyReject
	}
}

func summaryMessage(s *biz.ImportSummary) string {
	if s == nil {
		return ""
	}
	if s.HasErrors() {
		return "import completed with conflicts or errors"
	}
	return "import completed"
}
