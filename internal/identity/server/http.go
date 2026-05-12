package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/internal/identity/biz"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/pkg/oauth"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// NewHTTPServer wires HTTP transport for identity-service.
func NewHTTPServer(addr string, uc *biz.IdentityUsecase, oauthRegistry *oauth.ProviderRegistry, billingClients ...billingv1.BillingServiceClient) *khttp.Server {
	srv := khttp.NewServer(
		khttp.Address(addr),
	)
	var billingClient billingv1.BillingServiceClient
	if len(billingClients) > 0 {
		billingClient = billingClients[0]
	}

	// OAuth endpoints
	if oauthRegistry != nil {
		srv.HandleFunc("/api/oauth/github", func(w http.ResponseWriter, r *http.Request) {
			handleLegacyOAuth(w, r, oauthRegistry, "github")
		})
		srv.HandleFunc("/api/oauth/google", func(w http.ResponseWriter, r *http.Request) {
			handleLegacyOAuth(w, r, oauthRegistry, "google")
		})
		srv.HandleFunc("/v1/oauth/providers", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"providers": oauthRegistry.Names(),
			})
		})
		srv.HandleFunc("/v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
			handleOAuth(w, r, oauthRegistry, uc)
		})
	}
	srv.HandleFunc("/api/oauth/state", handleOAuthState)
	for _, providerName := range []string{"oidc", "lark", "wechat"} {
		name := providerName
		srv.HandleFunc("/api/oauth/"+name, func(w http.ResponseWriter, r *http.Request) {
			handleOneAPIOAuthAlias(w, r, oauthRegistry, name)
		})
	}
	srv.HandleFunc("/api/oauth/wechat/bind", func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOAuthAlias(w, r, oauthRegistry, "wechat")
	})

	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	srv.HandleFunc("/api/user/register", func(w http.ResponseWriter, r *http.Request) {
		handleRegister(w, r, uc)
	})
	srv.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		handleLogin(w, r, uc)
	})
	srv.HandleFunc("/api/user/logout", func(w http.ResponseWriter, r *http.Request) {
		handleLogout(w, r)
	})
	srv.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		handleSelf(w, r, uc)
	})
	srv.HandleFunc("/api/user/available_models", func(w http.ResponseWriter, r *http.Request) {
		handleAvailableModels(w, r, uc)
	})
	srv.HandleFunc("/api/user/aff", func(w http.ResponseWriter, r *http.Request) {
		handleAffCode(w, r, uc)
	})
	srv.HandleFunc("/api/oauth/email/bind", func(w http.ResponseWriter, r *http.Request) {
		handleEmailBind(w, r, uc)
	})
	srv.HandleFunc("/api/user/dashboard", func(w http.ResponseWriter, r *http.Request) {
		handleUserDashboard(w, r, uc, billingClient)
	})
	srv.HandleFunc("/dashboard/billing/usage", func(w http.ResponseWriter, r *http.Request) {
		handleDashboardBillingUsage(w, r, uc, billingClient)
	})
	srv.HandleFunc("/v1/dashboard/billing/usage", func(w http.ResponseWriter, r *http.Request) {
		handleDashboardBillingUsage(w, r, uc, billingClient)
	})
	srv.HandleFunc("/dashboard/billing/subscription", func(w http.ResponseWriter, r *http.Request) {
		handleDashboardBillingSubscription(w, r, uc, billingClient)
	})
	srv.HandleFunc("/v1/dashboard/billing/subscription", func(w http.ResponseWriter, r *http.Request) {
		handleDashboardBillingSubscription(w, r, uc, billingClient)
	})
	srv.HandleFunc("/api/user/topup", func(w http.ResponseWriter, r *http.Request) {
		handleUserTopUp(w, r, uc, billingClient)
	})
	srv.HandleFunc("/api/verification", func(w http.ResponseWriter, r *http.Request) {
		handleEmailVerification(w, r)
	})
	srv.HandleFunc("/api/reset_password", func(w http.ResponseWriter, r *http.Request) {
		handleResetPasswordRequest(w, r)
	})
	srv.HandleFunc("/api/user/reset", func(w http.ResponseWriter, r *http.Request) {
		handleResetPassword(w, r, uc)
	})
	srv.HandleFunc("/api/user/token", func(w http.ResponseWriter, r *http.Request) {
		handleCreateUserToken(w, r, uc)
	})
	srv.HandlePrefix("/api/token/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleTokens(w, r, uc)
	}))
	srv.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		handleTokens(w, r, uc)
	})
	return srv
}

type apiResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type verificationRecord struct {
	Code string
	At   time.Time
}

var verificationStore = struct {
	sync.Mutex
	items map[string]verificationRecord
}{
	items: make(map[string]verificationRecord),
}

func handleRegister(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Group    string `json:"group"`
		AffCode  string `json:"aff_code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Group == "" {
		req.Group = "default"
	}
	user, err := uc.RegisterWithAffCode(r.Context(), req.Username, req.Password, req.Email, req.Group, req.AffCode)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"user_id": user.ID}})
}

func handleAffCode(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	code, err := uc.GetOrCreateAffCode(r.Context(), snapshot.UserID)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: code})
}

func handleEmailBind(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	email := r.URL.Query().Get("email")
	code := r.URL.Query().Get("code")
	if email == "" || code == "" {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "invalid parameter"})
		return
	}
	verificationStore.Lock()
	record, ok := verificationStore.items["v:"+email]
	verificationStore.Unlock()
	if !ok || record.Code != code {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "验证码错误或已过期"})
		return
	}
	if err := uc.UpdateSelfEmail(r.Context(), snapshot.UserID, email); err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: ""})
}

func handleUserDashboard(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	if billingClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Success: false, Message: "billing service unavailable"})
		return
	}
	resp, err := billingClient.GetAccountSnapshot(r.Context(), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	account := resp.GetSnapshot()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{
		"quota":         account.GetQuota(),
		"used_quota":    account.GetUsedQuota(),
		"request_count": account.GetRequestCount(),
		"group":         account.GetGroup(),
		"group_ratio":   account.GetGroupRatio(),
		"frozen_quota":  account.GetFrozenQuota(),
	}})
}

func handleDashboardBillingUsage(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": map[string]interface{}{"message": "unauthorized", "type": "one_api_error"}})
		return
	}
	if billingClient == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": map[string]interface{}{"message": "billing service unavailable", "type": "one_api_error"}})
		return
	}
	resp, err := billingClient.GetAccountSnapshot(r.Context(), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": map[string]interface{}{"message": err.Error(), "type": "one_api_error"}})
		return
	}
	usedQuota := int64(0)
	if resp.GetSnapshot() != nil {
		usedQuota = resp.GetSnapshot().GetUsedQuota()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":      "list",
		"total_usage": usedQuota * 100,
	})
}

func handleDashboardBillingSubscription(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": map[string]interface{}{"message": "unauthorized", "type": "one_api_error"}})
		return
	}
	if billingClient == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": map[string]interface{}{"message": "billing service unavailable", "type": "one_api_error"}})
		return
	}
	resp, err := billingClient.GetAccountSnapshot(r.Context(), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": map[string]interface{}{"message": err.Error(), "type": "one_api_error"}})
		return
	}
	limit := int64(0)
	if resp.GetSnapshot() != nil {
		limit = resp.GetSnapshot().GetQuota()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":                "billing_subscription",
		"has_payment_method":    false,
		"canceled":              false,
		"canceled_at":           nil,
		"delinquent":            nil,
		"access_until":          0,
		"soft_limit_usd":        limit,
		"hard_limit_usd":        limit,
		"system_hard_limit_usd": limit,
		"soft_limit":            limit,
		"hard_limit":            limit,
		"system_hard_limit":     limit,
	})
}

func handleUserTopUp(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	if billingClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Success: false, Message: "billing service unavailable"})
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "key is required"})
		return
	}
	resp, err := billingClient.RedeemCode(r.Context(), &billingv1.RedeemCodeRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
		Code:   req.Key,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	if !resp.GetSuccess() {
		message := resp.GetErrorMessage()
		if message == "" {
			message = "redeem failed"
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: message})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: resp.GetAmount()})
}

func handleLogin(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	user, token, err := uc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "invalid credentials"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Message: "",
		Data: map[string]interface{}{
			"token":   token,
			"user_id": user.ID,
			"user":    userToMap(user),
		},
	})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: ""})
}

func handleSelf(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		user, err := uc.GetUser(r.Context(), snapshot.UserID)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: userToMap(user)})
	case http.MethodPut:
		var req struct {
			Username    string `json:"username"`
			DisplayName string `json:"display_name"`
			Password    string `json:"password"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := uc.UpdateSelf(r.Context(), snapshot.UserID, req.Username, req.DisplayName, req.Password); err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: ""})
	case http.MethodDelete:
		if err := uc.DeleteUser(r.Context(), snapshot.UserID); err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: ""})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
	}
}

func handleAvailableModels(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: snapshot.AllowedModels})
}

func handleEmailVerification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	email := r.URL.Query().Get("email")
	if email == "" {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "email is required"})
		return
	}
	code := generateState()[:6]
	verificationStore.Lock()
	verificationStore.items["v:"+email] = verificationRecord{Code: code, At: time.Now()}
	verificationStore.Unlock()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"email": email, "verification_code": code}})
}

func handleResetPasswordRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	email := r.URL.Query().Get("email")
	if email == "" {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "email is required"})
		return
	}
	code := generateState()[:6]
	verificationStore.Lock()
	verificationStore.items["r:"+email] = verificationRecord{Code: code, At: time.Now()}
	verificationStore.Unlock()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"email": email, "token": code}})
}

func handleResetPassword(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	var req struct {
		Email    string `json:"email"`
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Email == "" || req.Token == "" {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "invalid parameter"})
		return
	}
	verificationStore.Lock()
	record, ok := verificationStore.items["r:"+req.Email]
	verificationStore.Unlock()
	if !ok || record.Code != req.Token {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "重置链接非法或已过期"})
		return
	}
	password := req.Password
	if password == "" {
		password = generateState()[:12]
	}
	if err := uc.ResetPasswordByEmail(r.Context(), req.Email, password); err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"password": password}})
}

func handleLegacyOAuth(w http.ResponseWriter, r *http.Request, registry *oauth.ProviderRegistry, providerName string) {
	provider, ok := registry.Get(providerName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider: " + providerName})
		return
	}
	handleOAuthAuthorize(w, r, provider)
}

func handleOAuthState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	state := generateState()
	setOAuthStateCookie(w, state)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"state": state}})
}

func handleOneAPIOAuthAlias(w http.ResponseWriter, r *http.Request, registry *oauth.ProviderRegistry, providerName string) {
	if registry == nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: providerName + " oauth disabled"})
		return
	}
	provider, ok := registry.Get(providerName)
	if !ok {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: providerName + " oauth disabled"})
		return
	}
	handleOAuthAuthorize(w, r, provider)
}

func handleCreateUserToken(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	token, err := uc.CreateAccessToken(r.Context(), snapshot.UserID, "default", nil, 0)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, true)})
}

func handleTokens(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	tokenID, hasTokenID := parseTokenID(r.URL.Path)
	switch r.Method {
	case http.MethodGet:
		if hasTokenID {
			token, err := uc.GetAccessToken(r.Context(), snapshot.UserID, tokenID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, apiResponse{Success: false, Message: "token not found"})
				return
			}
			writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, true)})
			return
		}
		page := queryInt32(r, "page", 1)
		pageSize := queryInt32(r, "page_size", 20)
		keyword := r.URL.Query().Get("keyword")
		tokens, total, err := uc.ListAccessTokens(r.Context(), snapshot.UserID, page, pageSize, keyword)
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		items := make([]map[string]interface{}, 0, len(tokens))
		for _, token := range tokens {
			items = append(items, tokenToMap(token, false))
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"items": items, "total": total}})
	case http.MethodPost:
		var req struct {
			Name           string   `json:"name"`
			Models         []string `json:"models"`
			ExpiredAt      int64    `json:"expired_time"`
			ExpireAt       int64    `json:"expire_at"`
			RemainQuota    int64    `json:"remain_quota"`
			UnlimitedQuota bool     `json:"unlimited_quota"`
			Subnet         string   `json:"subnet"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		expireAt := req.ExpiredAt
		if expireAt == 0 {
			expireAt = req.ExpireAt
		}
		token, err := uc.CreateAccessToken(r.Context(), snapshot.UserID, req.Name, req.Models, expireAt, biz.CreateAccessTokenOptions{
			RemainQuota:    req.RemainQuota,
			UnlimitedQuota: req.UnlimitedQuota,
			Subnet:         req.Subnet,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, true)})
	case http.MethodPut:
		var req struct {
			ID             int64    `json:"id"`
			Name           string   `json:"name"`
			Models         []string `json:"models"`
			ExpiredAt      int64    `json:"expired_time"`
			Status         int32    `json:"status"`
			RemainQuota    int64    `json:"remain_quota"`
			UnlimitedQuota bool     `json:"unlimited_quota"`
			Subnet         string   `json:"subnet"`
		}
		req.RemainQuota = -1
		if !decodeJSON(w, r, &req) {
			return
		}
		if !hasTokenID {
			tokenID = req.ID
			hasTokenID = tokenID > 0
		}
		if !hasTokenID {
			writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "token id is required"})
			return
		}
		token, err := uc.UpdateAccessTokenWithOptions(r.Context(), snapshot.UserID, tokenID, biz.UpdateAccessTokenOptions{
			Name:           req.Name,
			Models:         req.Models,
			ExpireAt:       req.ExpiredAt,
			Status:         req.Status,
			RemainQuota:    req.RemainQuota,
			UnlimitedQuota: req.UnlimitedQuota,
			Subnet:         req.Subnet,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, true)})
	case http.MethodDelete:
		if !hasTokenID {
			writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "token id is required"})
			return
		}
		if err := uc.DeleteAccessToken(r.Context(), snapshot.UserID, tokenID); err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: ""})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "invalid request body"})
		return false
	}
	return true
}

func authSnapshotFromRequest(r *http.Request, uc *biz.IdentityUsecase) (*biz.AuthSnapshot, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, biz.ErrInvalidToken
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return nil, biz.ErrInvalidToken
	}
	return uc.GetAuthSnapshot(r.Context(), token)
}

func parseTokenID(path string) (int64, bool) {
	rest := strings.TrimPrefix(path, "/api/token")
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func queryInt32(r *http.Request, key string, defaultVal int32) int32 {
	value := r.URL.Query().Get(key)
	if value == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return defaultVal
	}
	return int32(n)
}

func userToMap(user *biz.User) map[string]interface{} {
	return map[string]interface{}{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"email":        user.Email,
		"group":        user.Group,
		"status":       user.Status,
	}
}

func tokenToMap(token *biz.Token, includeKey bool) map[string]interface{} {
	data := map[string]interface{}{
		"id":              token.ID,
		"name":            token.Name,
		"status":          token.Status,
		"expired_time":    token.ExpiredAt,
		"remain_quota":    token.RemainQuota,
		"unlimited_quota": token.UnlimitedQuota,
		"used_quota":      token.UsedQuota,
		"models":          token.Models,
		"created_at":      token.CreatedAt,
		"created_time":    token.CreatedAt,
		"accessed_time":   token.AccessedAt,
		"subnet":          token.Subnet,
	}
	if includeKey {
		data["key"] = token.Key
	}
	return data
}

// handleOAuth routes /v1/oauth/{provider}/{action} requests.
func handleOAuth(w http.ResponseWriter, r *http.Request, registry *oauth.ProviderRegistry, uc *biz.IdentityUsecase) {
	// Parse: /v1/oauth/{provider}/{action}
	path := r.URL.Path[len("/v1/oauth/"):]
	// Find first slash
	idx := -1
	for i, c := range path {
		if c == '/' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		http.Error(w, `{"error":"invalid oauth path"}`, http.StatusBadRequest)
		return
	}
	providerName := path[:idx]
	action := path[idx+1:]

	provider, ok := registry.Get(providerName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider: " + providerName})
		return
	}

	switch action {
	case "authorize":
		handleOAuthAuthorize(w, r, provider)
	case "callback":
		handleOAuthCallback(w, r, provider, uc)
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
	}
}

func handleOAuthAuthorize(w http.ResponseWriter, r *http.Request, provider oauth.Provider) {
	state := generateState()
	setOAuthStateCookie(w, state)
	http.Redirect(w, r, provider.AuthURL(state), http.StatusFound)
}

func setOAuthStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   300,
		SameSite: http.SameSiteLaxMode,
	})
}

func handleOAuthCallback(w http.ResponseWriter, r *http.Request, provider oauth.Provider, uc *biz.IdentityUsecase) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing code"})
		return
	}

	// Validate state (CSRF protection — mandatory)
	state := r.URL.Query().Get("state")
	cookie, _ := r.Cookie("oauth_state")
	if cookie == nil || cookie.Value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing oauth state cookie"})
		return
	}
	if cookie.Value != state {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid state"})
		return
	}

	// Delete state cookie after validation to prevent replay
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   -1,
		SameSite: http.SameSiteStrictMode,
	})

	userInfo, err := provider.Exchange(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "oauth provider error"})
		return
	}

	user, token, err := uc.OAuthLogin(r.Context(), userInfo.Provider, userInfo.ProviderID, userInfo.Username, userInfo.Email, userInfo.DisplayName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"token":    token,
		"user_id":  user.ID,
		"username": user.Username,
	})
}

func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
