package server

import (
	"context"

	"micro-one-api/pkg/jsonx"

	"go.uber.org/zap"

	"net/http"
	"strconv"
	"strings"

	applogger "micro-one-api/platform/logging"

	"google.golang.org/protobuf/types/known/emptypb"

	adminv1 "micro-one-api/api/admin/v1"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/service"
)

// handleModels is the /api/admin/models collection handler.
// GET    → list models
// POST   → create model
// PUT    → update model (body carries model_pk)
// PATCH  → batch operation (/api/admin/models/batch)
func handleModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/admin/models/batch" {
		handleModelsBatch(w, r, svc)
		return
	}
	if trimmed != "api/admin/models" {
		handleModelByID(w, r, svc)
		return
	}
	switch r.Method {
	case http.MethodGet:
		handleListModels(w, r, svc)
	case http.MethodPost:
		handleCreateModel(w, r, svc)
	case http.MethodPut:
		handleUpdateModel(w, r, svc)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleListModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	resp, err := svc.ListModels(r.Context(), &channelv1.ListModelsRequest{
		Page:       getQueryInt32(r, "page", 1),
		PageSize:   getQueryInt32(r, "page_size", 20),
		Keyword:    r.URL.Query().Get("keyword"),
		Provider:   r.URL.Query().Get("provider"),
		ModelType:  r.URL.Query().Get("model_type"),
		Status:     getQueryInt32(r, "status", 0),
		Category:   r.URL.Query().Get("category"),
		Tier:       r.URL.Query().Get("tier"),
		PublicOnly: r.URL.Query().Get("public_only") == "true",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleCreateModel(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	var req channelv1.CreateModelRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.CreateModel(r.Context(), &req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleUpdateModel(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	var req channelv1.UpdateModelRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.UpdateModel(r.Context(), &req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleModelsBatch(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req channelv1.BatchModelsRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.BatchModels(r.Context(), &req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleModelByID handles /api/admin/models/{model_pk}[/{action}].
// GET    → get model detail
// DELETE → delete model
// PUT    /status → change status
// POST   /aliases → create alias
// DELETE /aliases/{alias_id} → delete alias
func handleModelByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/models/")
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 3)
	modelPK, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || modelPK <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model id"})
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		resp, err := svc.GetModel(r.Context(), &channelv1.GetModelRequest{ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case action == "" && r.Method == http.MethodDelete:
		resp, err := svc.DeleteModel(r.Context(), &channelv1.DeleteModelRequest{ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case action == "status" && r.Method == http.MethodPatch:
		var body struct {
			Status int32 `json:"status"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		resp, err := svc.ChangeModelStatus(r.Context(), &channelv1.ChangeModelStatusRequest{ModelPk: modelPK, Status: body.Status})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case action == "usage-stats" && r.Method == http.MethodGet:
		handleModelUsageStats(w, r, svc)
		return
	case action == "aliases" && r.Method == http.MethodGet:
		resp, err := svc.ListModelAliases(r.Context(), &channelv1.ListModelAliasesRequest{ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case action == "aliases" && r.Method == http.MethodPost:
		var req channelv1.CreateModelAliasRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.ModelPk = modelPK
		resp, err := svc.CreateModelAlias(r.Context(), &req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case action == "aliases" && r.Method == http.MethodDelete:
		// /api/admin/models/{model_pk}/aliases/{alias_id}
		if len(parts) < 3 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alias id required"})
			return
		}
		aliasID, perr := strconv.ParseInt(parts[2], 10, 64)
		if perr != nil || aliasID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid alias id"})
			return
		}
		resp, err := svc.DeleteModelAlias(r.Context(), &channelv1.DeleteModelAliasRequest{AliasId: aliasID})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case action == "channels" && r.Method == http.MethodGet:
		// Use the model-scoped query via GetModel which returns channel mappings.
		detail, err := svc.GetModel(r.Context(), &channelv1.GetModelRequest{ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mappings": detail.GetChannelMappings()})
	case action == "subscriptions" && r.Method == http.MethodGet:
		detail, err := svc.GetModel(r.Context(), &channelv1.GetModelRequest{ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mappings": detail.GetSubscriptionMappings()})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleChannelModelMappings handles /api/admin/channels/{channel_id}/models.
// GET   → list mappings for the channel
// POST  → upsert a mapping
func handleChannelModelMappings(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	channelID, ok := parseChannelMappingPathID(r.URL.Path, "/api/admin/channels/", "/models")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channel id"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := svc.ListChannelModelMappings(r.Context(), &channelv1.ListChannelModelMappingsRequest{ChannelId: channelID})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPost:
		var req channelv1.UpsertChannelModelMappingRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.ChannelId = channelID
		resp, err := svc.UpsertChannelModelMapping(r.Context(), &req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodDelete:
		// /api/admin/channels/{channel_id}/models/{model_pk}
		rest := strings.TrimPrefix(r.URL.Path, "/api/admin/channels/"+strconv.FormatInt(channelID, 10)+"/models/")
		rest = strings.Trim(rest, "/")
		modelPK, perr := strconv.ParseInt(rest, 10, 64)
		if perr != nil || modelPK <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model pk"})
			return
		}
		resp, err := svc.DeleteChannelModelMapping(r.Context(), &channelv1.DeleteChannelModelMappingRequest{ChannelId: channelID, ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleSubscriptionModelMappings handles /api/admin/subscription-accounts/{account_id}/models.
func handleSubscriptionModelMappings(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	accountID, ok := parseChannelMappingPathID(r.URL.Path, "/api/admin/subscription-accounts/", "/models")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscription account id"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := svc.ListSubscriptionModelMappings(r.Context(), &channelv1.ListSubscriptionModelMappingsRequest{SubscriptionAccountId: accountID})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPost:
		var req channelv1.UpsertSubscriptionModelMappingRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.SubscriptionAccountId = accountID
		resp, err := svc.UpsertSubscriptionModelMapping(r.Context(), &req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodDelete:
		// /api/admin/subscription-accounts/{account_id}/models/{model_pk}[?group_name=...]
		rest := strings.TrimPrefix(r.URL.Path, "/api/admin/subscription-accounts/"+strconv.FormatInt(accountID, 10)+"/models/")
		rest = strings.Trim(rest, "/")
		modelPK, perr := strconv.ParseInt(rest, 10, 64)
		if perr != nil || modelPK <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model pk"})
			return
		}
		groupName := r.URL.Query().Get("group_name")
		resp, err := svc.DeleteSubscriptionModelMapping(r.Context(), &channelv1.DeleteSubscriptionModelMappingRequest{
			SubscriptionAccountId: accountID,
			ModelPk:               modelPK,
			GroupName:             groupName,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// ── Sprint 4: Usage statistics ─────────────────────────────────────────────

// handleModelUsageStats handles /api/admin/models/{model_pk}/usage-stats.
// GET → list usage stats for a model
func handleModelUsageStats(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/models/")
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[1] != "usage-stats" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}
	modelPK, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || modelPK <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model id"})
		return
	}
	resp, err := svc.ListModelUsageStats(r.Context(), &channelv1.ListModelUsageStatsRequest{
		ModelPk:   modelPK,
		StartDate: r.URL.Query().Get("start_date"),
		EndDate:   r.URL.Query().Get("end_date"),
		Page:      getQueryInt32(r, "page", 1),
		PageSize:  getQueryInt32(r, "page_size", 20),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseChannelMappingPathID extracts the {id} segment from a path shaped
// /prefix/{id}/suffix, returning the numeric id.
func parseChannelMappingPathID(path, prefix, suffix string) (int64, bool) {
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimSuffix(rest, suffix)
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil && id > 0
}

// ensure unused import stays valid if json is referenced indirectly.
var _ = jsonx.Marshal

// handleAdminChannelPath dispatches /api/admin/channels/{id}/... sub-paths.
// Currently only the /models suffix is handled; other paths fall through to 404.
func handleAdminChannelPath(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if strings.Contains(r.URL.Path, "/models") {
		handleChannelModelMappings(w, r, svc)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// handleAdminSubscriptionAccountPath dispatches /api/admin/subscription-accounts/{id}/...
func handleAdminSubscriptionAccountPath(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if strings.Contains(r.URL.Path, "/models") {
		handleSubscriptionModelMappings(w, r, svc)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// ── Model routing (P2 #3) ─────────────────────────────────────────────────

// handleModelRoutings handles /api/admin/model-routings and
// /api/admin/model-routings/{id}.
// GET    /api/admin/model-routings?group_name=&model=&platform=  → list
// POST   /api/admin/model-routings                               → upsert
// DELETE /api/admin/model-routings/{id}                          → delete
func handleModelRoutings(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/admin/model-routings" {
		switch r.Method {
		case http.MethodGet:
			resp, err := svc.ListModelRoutings(r.Context(), &adminv1.ListModelRoutingsRequest{
				GroupName: r.URL.Query().Get("group_name"),
				Model:     r.URL.Query().Get("model"),
				Platform:  r.URL.Query().Get("platform"),
			})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, resp)
		case http.MethodPost:
			var req adminv1.UpsertModelRoutingRequest
			if !decodeBody(w, r, &req) {
				return
			}
			resp, err := svc.UpsertModelRouting(r.Context(), &req)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, resp)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
		return
	}

	// /api/admin/model-routings/{id}
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/model-routings/")
	rest = strings.Trim(rest, "/")
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid routing id"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		resp, err := svc.DeleteModelRouting(r.Context(), &adminv1.DeleteModelRoutingRequest{Id: id})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// ── Canonical model ID governance (v0.11.0 Phase 2 §2.1) ──────────────────
//
// GET  /api/admin/models/canonical/preflight  → read-only duplicate report
// POST /api/admin/models/canonical/merge      → merge one duplicate group
//
// Mounted under /api/admin/models/canonical/* (see http.go). Kept separate
// from the CRUD path so the operator-facing preflight/merge surface does not
// collide with model-id path params.

func handleCanonicalPreflight(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp, err := svc.CanonicalModelPreflight(r.Context(), &emptypb.Empty{})
	if err != nil {
		// A canonical conflict surfaces as MODEL_CANONICAL_CONFLICT inside the
		// structured error; map it to 409 so the client can render it.
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "canonical") || strings.Contains(err.Error(), "conflict") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleCanonicalMerge(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req channelv1.MergeCanonicalModelsRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.MergeCanonicalModels(r.Context(), &req)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "canonical") || strings.Contains(err.Error(), "conflict") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUnpricedRoutedModels serves GET /api/admin/models/unpriced — the
// v0.11.0 Phase 2 §2.2 "routed but unpriced" audit. The priced set is sourced
// from the ModelPrice system option here (admin-api owns system_options) so
// the operator gets a single-call status without manually threading config.
func handleUnpricedRoutedModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp, err := svc.ListUnpricedRoutedModelsWithPricing(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// v0.11.0 review M5: update the Prometheus gauge via the shared helper so
	// the metric is also refreshed by the background worker.
	service.RecordUnpricedRoutedMetric(resp)
	// Write a structured audit event. The roadmap requires an audit trail for
	// the unpriced state; when the admin auditor is not wired we fall back to
	// the application logger so the event is never lost.
	logUnpricedRoutedAudit(r, resp)
	writeJSON(w, http.StatusOK, resp)
}

// logUnpricedRoutedAudit emits a structured audit log entry for the unpriced
// audit. The actor is the authenticated admin (from the X-Operator-User-Id
// header stamped by newAdminGuard); the model ids are included in the audit
// detail so ops can act on them without a second API call.
func logUnpricedRoutedAudit(r *http.Request, resp *channelv1.ListUnpricedRoutedModelsResponse) {
	if resp == nil {
		return
	}
	operator := adminOperatorIDFromRequest(r)
	modelIDs := make([]string, 0, len(resp.GetModels()))
	for _, m := range resp.GetModels() {
		modelIDs = append(modelIDs, m.GetModelId())
	}
	applogger.Log.Info("unpriced routed models audit",
		zap.String("actor", operator),
		zap.Int("count", int(resp.GetTotal())),
		zap.Strings("models", modelIDs),
		zap.String("request_id", r.Header.Get("X-Request-Id")),
	)
}

// ── v0.11.0 Phase 2 §2.2: independent upstream-cost management ─────────────
//
// GET    /api/admin/upstream-costs                → list the structured per-source view
// POST   /api/admin/upstream-costs                → upsert one entry (canonical key)
// DELETE /api/admin/upstream-costs?key=...        → delete one entry
// POST   /api/admin/upstream-costs/migrate        → migrate legacy keys (body: {dry_run:bool})

func handleListUpstreamCosts(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	view, err := svc.ListUpstreamCosts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func handleSetUpstreamCost(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var entry service.UpstreamCostEntry
	if !decodeBody(w, r, &entry) {
		return
	}
	if err := svc.SetUpstreamCost(r.Context(), entry); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func handleDeleteUpstreamCost(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	key := r.URL.Query().Get("key")
	if err := svc.DeleteUpstreamCost(r.Context(), key); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func handleMigrateUpstreamCostKeys(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// Default to dry_run=true so a careless POST does not rewrite config. The
	// operator must explicitly send {"dry_run": false} to apply.
	dryRun := true
	var body struct {
		DryRun *bool `json:"dry_run"`
	}
	_ = jsonx.NewDecoder(r.Body).Decode(&body)
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}
	plan, err := svc.MigrateUpstreamCostKeys(r.Context(), dryRun)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// unpricedRoutedCount returns the number of public, enabled, routed models
// that have no ModelPrice entry, computed against the freshly-saved pricing
// config. Used by the price-save flow (v0.11.0 Phase 2 §2.2) to surface the
// gap in the save response. Returns -1 when channel-service is unavailable
// so the caller can omit the field instead of reporting a misleading 0.
func unpricedRoutedCount(ctx context.Context, svc *service.AdminService) int {
	if svc == nil {
		return -1
	}
	resp, err := svc.ListUnpricedRoutedModelsWithPricing(ctx)
	if err != nil || resp == nil {
		return -1
	}
	return int(resp.GetTotal())
}
