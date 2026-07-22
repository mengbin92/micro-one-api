package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
		writeJSON(w, http.StatusOK, map[string]interface{}{"mappings": detail.GetChannelMappings()})
	case action == "subscriptions" && r.Method == http.MethodGet:
		detail, err := svc.GetModel(r.Context(), &channelv1.GetModelRequest{ModelPk: modelPK})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"mappings": detail.GetSubscriptionMappings()})
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
var _ = json.Marshal

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
