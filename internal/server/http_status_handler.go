package server

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

func (s *HTTPServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	modelsReply, err := s.listAvailableModels(r.Context(), authSnapshot.Group)
	if err != nil {
		s.handleChannelError(w, err)
		return
	}

	models := s.applyModelWhitelist(modelsReply.Models, authSnapshot.AllowedModels)

	response := struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}{
		Object: "list",
	}

	for _, model := range models {
		response.Data = append(response.Data, struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}{
			ID:      model,
			Object:  "model",
			Created: 0,
			OwnedBy: "organization",
		})
	}

	s.writeJSON(w, http.StatusOK, response)
}

func (s *HTTPServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}
	resp, err := s.billingClient.GetAccountSnapshot(r.Context(), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(authSnapshot.UserId, 10),
	})
	if err != nil {
		s.writeError(w, http.StatusBadGateway, "billing service error")
		return
	}
	account := resp.GetSnapshot()
	if account == nil {
		s.writeError(w, http.StatusBadGateway, "billing account not found")
		return
	}

	remaining := account.GetBalance()
	used := account.GetUsedAmount()
	frozen := account.GetFrozenAmount()
	remainingUSD := amountUnitsToUSD(remaining)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"mode":      "unrestricted",
		"isValid":   true,
		"is_active": true,
		"status":    "active",
		"user_id":   account.GetUserId(),
		"planName":  "钱包余额",
		"remaining": remainingUSD,
		"balance":   remainingUSD,
		"unit":      "USD",
		"quota": map[string]interface{}{
			"remaining": remaining,
			"used":      used,
			"frozen":    frozen,
			"unit":      "quota",
			"per_usd":   amountUnitsPerUSD,
		},
		"usage": map[string]interface{}{
			"total": map[string]interface{}{
				"cost":     used,
				"requests": account.GetRequestCount(),
			},
		},
	})
}

func (s *HTTPServer) handleSubscriptionUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if !authSnapshot.GetUserEnabled() || !authSnapshot.GetTokenEnabled() {
		s.writeError(w, http.StatusForbidden, "user or token disabled")
		return
	}

	if s.subscriptionUsecase == nil {
		// Subscriptions are not enabled on this deployment. Report a structured
		// success:false so tooling can surface "no subscription" rather than a
		// hard 5xx.
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":   false,
			"isValid":   false,
			"is_active": false,
			"mode":      "subscription",
			"message":   "subscription service not configured",
		})
		return
	}

	progress, err := s.subscriptionUsecase.GetProgress(r.Context(), authSnapshot.UserId)
	if err != nil && !stderrors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
		s.writeError(w, http.StatusBadGateway, "subscription service error")
		return
	}
	if progress == nil {
		// No active subscription is a normal state for a wallet-only user; return
		// success:false instead of an error status so cc-switch-style tools render
		// "no subscription" rather than a failure banner.
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":   false,
			"isValid":   false,
			"is_active": false,
			"mode":      "subscription",
			"message":   "no active subscription",
			"user_id":   strconv.FormatInt(authSnapshot.UserId, 10),
		})
		return
	}

	planName := progress.SubscriptionName
	if planName == "" {
		planName = fmt.Sprintf("subscription #%d", progress.ID)
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"isValid":   progress.Status == subscriptionbiz.SubscriptionStatusActive,
		"is_active": progress.Status == subscriptionbiz.SubscriptionStatusActive,
		"status":    string(progress.Status),
		"mode":      "subscription",
		"planName":  planName,
		"unit":      "USD",
		"user_id":   strconv.FormatInt(authSnapshot.UserId, 10),
		"data":      progress,
	})
}

func (s *HTTPServer) handleRetrieveModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	const prefix = "/v1/models/"
	modelID := strings.TrimPrefix(r.URL.Path, prefix)
	if modelID == "" || strings.Contains(modelID, "/") {
		s.writeError(w, http.StatusNotFound, "model not found")
		return
	}

	s.writeJSON(w, http.StatusOK, openAIModelResponse(modelID))
}

func (s *HTTPServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data": map[string]interface{}{
			"version":              "micro-one-api",
			"system_name":          "micro-one-api",
			"email_verification":   false,
			"github_oauth":         false,
			"wechat_login":         false,
			"turnstile_check":      false,
			"display_in_currency":  false,
			"registration_enabled": true,
		},
	})
}

func (s *HTTPServer) handleDashboardModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"message":  "",
		"data":     oneAPIChannelModelsByType(),
		"metadata": oneAPIProviderCatalogMetadata(),
	})
}

func (s *HTTPServer) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		group = "default"
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data":    []string{group},
	})
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// While draining WebSocket connections for shutdown (Phase 3.3), report
	// 503 so external load balancers stop sending traffic. The draining flag
	// is only set after a SIGTERM; in steady state healthz stays 200 ok.
	if s != nil && s.IsWSDraining() {
		w.Header().Set("Content-Type", "application/json")
		// Retry-After gives LBs a hint sized to the configured drain window.
		drainSec := int64(s.drainTimeout().Seconds())
		if drainSec <= 0 {
			drainSec = 30
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", drainSec))
		w.WriteHeader(http.StatusServiceUnavailable)
		encodeJSON(w, map[string]string{"status": "draining", "drain": "true"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func openAIModelResponse(modelID string) map[string]interface{} {
	permissionID := "modelperm-micro-one-api"
	return map[string]interface{}{
		"id":       modelID,
		"object":   "model",
		"created":  1626777600,
		"owned_by": "organization",
		"permission": []map[string]interface{}{
			{
				"id":                   permissionID,
				"object":               "model_permission",
				"created":              1626777600,
				"allow_create_engine":  true,
				"allow_sampling":       true,
				"allow_logprobs":       true,
				"allow_search_indices": false,
				"allow_view":           true,
				"allow_fine_tuning":    false,
				"organization":         "*",
				"group":                nil,
				"is_blocking":          false,
			},
		},
		"root":   modelID,
		"parent": nil,
	}
}
