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

// handlePurchasableSubscriptionGroups lists the subscription groups a user may
// buy for themselves. Any authenticated user may read the catalogue.
func handlePurchasableSubscriptionGroups(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if _, ok := authenticatedUserID(w, r, svc); !ok {
		return
	}
	groups, err := svc.ListPurchasableSubscriptionGroups(r.Context())
	writeSubscriptionResponse(w, groups, err)
}

func handlePurchasableSubscriptionPlans(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if _, ok := authenticatedUserID(w, r, svc); !ok {
		return
	}
	plans, err := svc.ListPurchasableSubscriptionPlans(r.Context())
	writeSubscriptionResponse(w, plans, err)
}

// handlePurchaseSubscription lets the authenticated user buy a subscription with
// their wallet balance. The buyer is taken from the bearer token, never from the
// request body, so a user cannot purchase on someone else's balance.
func handlePurchaseSubscription(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, ok := authenticatedUserID(w, r, svc)
	if !ok {
		return
	}
	var req struct {
		GroupID int64 `json:"group_id"`
		PlanID  int64 `json:"plan_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.PlanID <= 0 && req.GroupID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "plan_id or group_id is required", nil))
		return
	}
	var sub interface{}
	var err error
	if req.PlanID > 0 {
		sub, err = svc.PurchaseSubscriptionPlan(r.Context(), userID, req.PlanID)
	} else {
		sub, err = svc.PurchaseSubscription(r.Context(), userID, req.GroupID)
	}
	writeSubscriptionResponse(w, sub, err)
}

// authenticatedUserID resolves the bearer token to a user id, writing a 401 and
// returning ok=false when the token is missing or invalid.
func authenticatedUserID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) (int64, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
		return 0, false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	userID, err := svc.ResolveUserIDFromToken(r.Context(), token)
	if err != nil || userID <= 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return 0, false
	}
	return userID, true
}

func handleUserSubscriptions(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// user_id is an optional filter: with it we scope to one user, without it we
	// list every subscription so admins can browse without knowing a user id.
	userID := getQueryInt64(r, "user_id", 0)
	if userID > 0 {
		items, err := svc.ListUserSubscriptions(r.Context(), userID)
		writeSubscriptionResponse(w, items, err)
		return
	}
	items, err := svc.ListAllSubscriptions(r.Context())
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

func handleSubscriptionPlans(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		plans, err := svc.ListSubscriptionPlans(r.Context())
		writeSubscriptionResponse(w, plans, err)
	case http.MethodPost:
		var plan subscriptionbiz.SubscriptionPlan
		if !decodeBody(w, r, &plan) {
			return
		}
		err := svc.CreateSubscriptionPlan(r.Context(), &plan)
		writeSubscriptionResponse(w, &plan, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleSubscriptionPlanByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	id, ok := parsePathID(r.URL.Path, "/api/v1/admin/subscription-plans/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid plan id", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		plan, err := svc.GetSubscriptionPlan(r.Context(), id)
		writeSubscriptionResponse(w, plan, err)
	case http.MethodPut:
		var plan subscriptionbiz.SubscriptionPlan
		if !decodeBody(w, r, &plan) {
			return
		}
		plan.ID = id
		err := svc.UpdateSubscriptionPlan(r.Context(), &plan)
		writeSubscriptionResponse(w, &plan, err)
	case http.MethodDelete:
		writeSubscriptionResponse(w, nil, svc.DeleteSubscriptionPlan(r.Context(), id))
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

// handlePurchaseSubscriptionWithPayment lets an authenticated user create a
// payment order for a subscription purchase.
func handlePurchaseSubscriptionWithPayment(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, ok := authenticatedUserID(w, r, svc)
	if !ok {
		return
	}
	var req struct {
		GroupID    int64  `json:"group_id"`
		PlanID     int64  `json:"plan_id"`
		Channel    string `json:"channel"`     // Optional: payment channel (default: alipay)
		MoneyCents int64  `json:"money_cents"` // Optional: amount in cents (default: price * 100)
		Currency   string `json:"currency"`    // Optional: currency code (default: CNY)
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.PlanID <= 0 && req.GroupID <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "plan_id or group_id is required", nil))
		return
	}

	sub, paymentOrder, err := svc.CreateSubscriptionPaymentOrder(r.Context(), userID, req.GroupID, req.PlanID, req.Channel, req.MoneyCents, req.Currency)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}

	// If subscription was created directly (balance sufficient)
	if sub != nil {
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
			"subscription": subscriptionResponse(sub),
			"payment":      nil,
		}))
		return
	}

	// If payment order was created (balance insufficient)
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
		"subscription": nil,
		"payment":      paymentOrder,
	}))
}

// handleCompleteSubscriptionPurchase completes a subscription purchase after payment.
// It is called after the user completes the payment to assign the subscription.
func handleCompleteSubscriptionPurchase(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, ok := authenticatedUserID(w, r, svc)
	if !ok {
		return
	}
	var req struct {
		TradeNo string `json:"trade_no"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.TradeNo == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "trade_no is required", nil))
		return
	}

	sub, err := svc.CompleteSubscriptionPurchase(r.Context(), userID, req.TradeNo)
	writeSubscriptionResponse(w, sub, err)
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
	case *subscriptionbiz.SubscriptionPlan:
		return planResponse(v)
	case []*subscriptionbiz.SubscriptionPlan:
		out := make([]subscriptionPlanDTO, 0, len(v))
		for _, item := range v {
			out = append(out, planResponse(item))
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
	PriceQuota       int64    `json:"price_quota"`
	DurationDays     int32    `json:"duration_days"`
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
		PriceQuota:       group.PriceQuota,
		DurationDays:     group.DurationDays,
		CreatedAt:        group.CreatedAt,
		UpdatedAt:        group.UpdatedAt,
	}
}

type subscriptionPlanDTO struct {
	ID            int64                 `json:"id"`
	GroupID       int64                 `json:"group_id"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	PriceQuota    int64                 `json:"price_quota"`
	OriginalPrice *int64                `json:"original_price,omitempty"`
	ValidityDays  int32                 `json:"validity_days"`
	ValidityUnit  string                `json:"validity_unit"`
	Features      string                `json:"features"`
	ProductName   string                `json:"product_name"`
	ForSale       bool                  `json:"for_sale"`
	SortOrder     int32                 `json:"sort_order"`
	Group         *subscriptionGroupDTO `json:"group,omitempty"`
	CreatedAt     int64                 `json:"created_at"`
	UpdatedAt     int64                 `json:"updated_at"`
}

func planResponse(plan *subscriptionbiz.SubscriptionPlan) subscriptionPlanDTO {
	if plan == nil {
		return subscriptionPlanDTO{}
	}
	var group *subscriptionGroupDTO
	if plan.Group != nil {
		dto := groupResponse(plan.Group)
		group = &dto
	}
	return subscriptionPlanDTO{
		ID:            plan.ID,
		GroupID:       plan.GroupID,
		Name:          plan.Name,
		Description:   plan.Description,
		PriceQuota:    plan.PriceQuota,
		OriginalPrice: plan.OriginalPrice,
		ValidityDays:  plan.ValidityDays,
		ValidityUnit:  plan.ValidityUnit,
		Features:      plan.Features,
		ProductName:   plan.ProductName,
		ForSale:       plan.ForSale,
		SortOrder:     plan.SortOrder,
		Group:         group,
		CreatedAt:     plan.CreatedAt,
		UpdatedAt:     plan.UpdatedAt,
	}
}
