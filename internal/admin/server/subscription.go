package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"micro-one-api/internal/admin/service"
	subscriptionbiz "micro-one-api/internal/subscription/biz"
)

func handleCurrentSubscriptionProgress(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID := getQueryInt64(r, "user_id", 0)
	if userID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "user_id is required", nil))
		return
	}
	progress, err := svc.GetSubscriptionProgress(r.Context(), userID)
	writeSubscriptionResponse(w, progress, err)
}

func handleUserSubscriptions(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID := getQueryInt64(r, "user_id", 0)
	if userID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "user_id is required", nil))
		return
	}
	items, err := svc.ListUserSubscriptions(r.Context(), userID)
	writeSubscriptionResponse(w, items, err)
}

func handleAssignSubscription(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req subscriptionbiz.AssignSubscriptionRequest
	if !decodeBody(w, r, &req) {
		return
	}
	sub, err := svc.AssignSubscription(r.Context(), &req)
	writeSubscriptionResponse(w, sub, err)
}

func handleSubscriptionByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/subscriptions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid subscription path", nil))
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid subscription id", nil))
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	switch parts[1] {
	case "revoke":
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeSubscriptionResponse(w, nil, svc.RevokeSubscription(r.Context(), id, req.Reason))
	case "extend":
		var req struct {
			ExpiresAt int64 `json:"expires_at"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		writeSubscriptionResponse(w, nil, svc.ExtendSubscription(r.Context(), id, req.ExpiresAt))
	case "reset-quota":
		var req struct {
			Scope string `json:"scope"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		if req.Scope == "" {
			req.Scope = "all"
		}
		writeSubscriptionResponse(w, nil, svc.ResetSubscriptionQuota(r.Context(), id, req.Scope))
	default:
		writeJSON(w, http.StatusNotFound, apiResponse(false, "not found", nil))
	}
}

func handleSubscriptionGroups(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		groups, err := svc.ListSubscriptionGroups(r.Context())
		writeSubscriptionResponse(w, groups, err)
	case http.MethodPost:
		var group subscriptionbiz.SubscriptionGroup
		if !decodeBody(w, r, &group) {
			return
		}
		err := svc.CreateSubscriptionGroup(r.Context(), &group)
		writeSubscriptionResponse(w, &group, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleSubscriptionGroupByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	id, ok := parsePathID(r.URL.Path, "/api/v1/admin/subscription-groups/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid group id", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		group, err := svc.GetSubscriptionGroup(r.Context(), id)
		writeSubscriptionResponse(w, group, err)
	case http.MethodPut:
		var group subscriptionbiz.SubscriptionGroup
		if !decodeBody(w, r, &group) {
			return
		}
		group.ID = id
		err := svc.UpdateSubscriptionGroup(r.Context(), &group)
		writeSubscriptionResponse(w, &group, err)
	case http.MethodDelete:
		writeSubscriptionResponse(w, nil, svc.DeleteSubscriptionGroup(r.Context(), id))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func writeSubscriptionResponse(w http.ResponseWriter, data interface{}, err error) {
	if err != nil {
		status := http.StatusOK
		if errors.Is(err, service.ErrSubscriptionServiceNotConfigured) {
			status = http.StatusNotImplemented
		}
		writeJSON(w, status, apiResponse(false, err.Error(), nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", normalizeSubscriptionResponse(data)))
}

func normalizeSubscriptionResponse(data interface{}) interface{} {
	switch v := data.(type) {
	case *subscriptionbiz.UserSubscription:
		return subscriptionResponse(v)
	case []*subscriptionbiz.UserSubscription:
		out := make([]subscriptionDTO, 0, len(v))
		for _, item := range v {
			out = append(out, subscriptionResponse(item))
		}
		return out
	case *subscriptionbiz.SubscriptionGroup:
		return groupResponse(v)
	case []*subscriptionbiz.SubscriptionGroup:
		out := make([]subscriptionGroupDTO, 0, len(v))
		for _, item := range v {
			out = append(out, groupResponse(item))
		}
		return out
	default:
		return data
	}
}

type subscriptionDTO struct {
	ID                 int64                              `json:"id"`
	UserID             int64                              `json:"user_id"`
	GroupID            int64                              `json:"group_id"`
	SubscriptionName   string                             `json:"subscription_name"`
	Status             subscriptionbiz.SubscriptionStatus `json:"status"`
	StartsAt           int64                              `json:"starts_at"`
	ExpiresAt          int64                              `json:"expires_at"`
	DailyUsageUSD      float64                            `json:"daily_usage_usd"`
	WeeklyUsageUSD     float64                            `json:"weekly_usage_usd"`
	MonthlyUsageUSD    float64                            `json:"monthly_usage_usd"`
	DailyWindowStart   int64                              `json:"daily_window_start"`
	WeeklyWindowStart  int64                              `json:"weekly_window_start"`
	MonthlyWindowStart int64                              `json:"monthly_window_start"`
	Metadata           string                             `json:"metadata"`
	CreatedAt          int64                              `json:"created_at"`
	UpdatedAt          int64                              `json:"updated_at"`
}

func subscriptionResponse(sub *subscriptionbiz.UserSubscription) subscriptionDTO {
	if sub == nil {
		return subscriptionDTO{}
	}
	return subscriptionDTO{
		ID:                 sub.ID,
		UserID:             sub.UserID,
		GroupID:            sub.GroupID,
		SubscriptionName:   sub.SubscriptionName,
		Status:             sub.Status,
		StartsAt:           sub.StartsAt,
		ExpiresAt:          sub.ExpiresAt,
		DailyUsageUSD:      sub.DailyUsageUSD,
		WeeklyUsageUSD:     sub.WeeklyUsageUSD,
		MonthlyUsageUSD:    sub.MonthlyUsageUSD,
		DailyWindowStart:   sub.DailyWindowStart,
		WeeklyWindowStart:  sub.WeeklyWindowStart,
		MonthlyWindowStart: sub.MonthlyWindowStart,
		Metadata:           sub.Metadata,
		CreatedAt:          sub.CreatedAt,
		UpdatedAt:          sub.UpdatedAt,
	}
}

type subscriptionGroupDTO struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	DisplayName      string   `json:"display_name"`
	Platform         string   `json:"platform"`
	SubscriptionType string   `json:"subscription_type"`
	DailyLimitUSD    *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD   *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD  *float64 `json:"monthly_limit_usd"`
	RateMultiplier   float64  `json:"rate_multiplier"`
	Status           int32    `json:"status"`
	CreatedAt        int64    `json:"created_at"`
	UpdatedAt        int64    `json:"updated_at"`
}

func groupResponse(group *subscriptionbiz.SubscriptionGroup) subscriptionGroupDTO {
	if group == nil {
		return subscriptionGroupDTO{}
	}
	return subscriptionGroupDTO{
		ID:               group.ID,
		Name:             group.Name,
		DisplayName:      group.DisplayName,
		Platform:         group.Platform,
		SubscriptionType: group.SubscriptionType,
		DailyLimitUSD:    group.DailyLimitUSD,
		WeeklyLimitUSD:   group.WeeklyLimitUSD,
		MonthlyLimitUSD:  group.MonthlyLimitUSD,
		RateMultiplier:   group.RateMultiplier,
		Status:           group.Status,
		CreatedAt:        group.CreatedAt,
		UpdatedAt:        group.UpdatedAt,
	}
}
