package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
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

const paymentQuotaPerCNY = int64(5000000)

type TurnstileVerifier interface {
	VerifyTurnstile(ctx context.Context, secret, token, remoteIP string) error
}

type RegistrationPolicy struct {
	Enabled                       bool
	EmailDomainRestrictionEnabled bool
	EmailDomainWhitelist          []string
	TurnstileCheckEnabled         bool
	TurnstileSecret               string
	TurnstileVerifyHandler        TurnstileVerifier
}

type defaultTurnstileVerifier struct {
	client *http.Client
}

// NewHTTPServer wires HTTP transport for identity-service.
func NewHTTPServer(addr string, uc *biz.IdentityUsecase, oauthRegistry *oauth.ProviderRegistry, billingClients ...billingv1.BillingServiceClient) *khttp.Server {
	return NewHTTPServerWithRegistrationPolicy(addr, uc, oauthRegistry, RegistrationPolicy{Enabled: true}, billingClients...)
}

func NewHTTPServerWithRegistrationPolicy(addr string, uc *biz.IdentityUsecase, oauthRegistry *oauth.ProviderRegistry, registrationPolicy RegistrationPolicy, billingClients ...billingv1.BillingServiceClient) *khttp.Server {
	if registrationPolicy.TurnstileCheckEnabled && registrationPolicy.TurnstileVerifyHandler == nil {
		registrationPolicy.TurnstileVerifyHandler = &defaultTurnstileVerifier{client: &http.Client{Timeout: 10 * time.Second}}
	}
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
		srv.HandleFunc("/api/oauth/github/bind", func(w http.ResponseWriter, r *http.Request) {
			handleOneAPIOAuthBind(w, r, oauthRegistry, uc, "github")
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
	srv.HandleFunc("/api/oauth/oidc/bind", func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOAuthBind(w, r, oauthRegistry, uc, "oidc")
	})
	srv.HandleFunc("/api/oauth/wechat/bind", func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOAuthBind(w, r, oauthRegistry, uc, "wechat")
	})
	srv.HandleFunc("/api/oauth/lark/bind", func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOAuthBind(w, r, oauthRegistry, uc, "lark")
	})
	srv.HandleFunc("/api/oauth/telegram/login", func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOAuthAlias(w, r, oauthRegistry, "telegram")
	})
	srv.HandleFunc("/api/oauth/telegram/bind", func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOAuthAlias(w, r, oauthRegistry, "telegram")
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
		handleRegister(w, r, uc, registrationPolicy, billingClient)
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
	srv.HandleFunc("/api/user/models", func(w http.ResponseWriter, r *http.Request) {
		handleAvailableModels(w, r, uc)
	})
	srv.HandleFunc("/api/user/aff", func(w http.ResponseWriter, r *http.Request) {
		handleAffCode(w, r, uc)
	})
	srv.HandleFunc("/api/user/invitation", func(w http.ResponseWriter, r *http.Request) {
		handleAffCode(w, r, uc)
	})
	srv.HandleFunc("/api/user/aff_transfer", func(w http.ResponseWriter, r *http.Request) {
		handleAffTransferDisabled(w, r, uc)
	})
	srv.HandleFunc("/api/oauth/email/bind", func(w http.ResponseWriter, r *http.Request) {
		handleEmailBind(w, r, uc)
	})
	srv.HandleFunc("/api/user/dashboard", func(w http.ResponseWriter, r *http.Request) {
		handleUserDashboard(w, r, uc, billingClient)
	})
	srv.HandleFunc("/api/user/quota", func(w http.ResponseWriter, r *http.Request) {
		handleUserDashboard(w, r, uc, billingClient)
	})
	srv.HandleFunc("/api/user/logs", func(w http.ResponseWriter, r *http.Request) {
		handleUserLogs(w, r, uc, billingClient)
	})
	srv.HandleFunc("/api/user/payment/orders", func(w http.ResponseWriter, r *http.Request) {
		handleUserPaymentOrders(w, r, uc, billingClient)
	})
	srv.HandlePrefix("/api/user/payment/orders/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleUserPaymentOrderByTradeNo(w, r, uc, billingClient)
	}))
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
	srv.HandleFunc("/api/user/amount", func(w http.ResponseWriter, r *http.Request) {
		handleCreatePaymentOrder(w, r, uc, billingClient)
	})
	srv.HandleFunc("/api/user/pay", func(w http.ResponseWriter, r *http.Request) {
		handleCreatePaymentOrder(w, r, uc, billingClient)
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

func handleRegister(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, policy RegistrationPolicy, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	var req struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		Email          string `json:"email"`
		Group          string `json:"group"`
		AffCode        string `json:"aff_code"`
		TurnstileToken string `json:"turnstile_token"`
		CFToken        string `json:"cf_turnstile_response"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !policy.Enabled {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "registration disabled"})
		return
	}
	if policy.EmailDomainRestrictionEnabled && !emailDomainAllowed(req.Email, policy.EmailDomainWhitelist) {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "email domain is not allowed"})
		return
	}
	if policy.TurnstileCheckEnabled {
		token := req.TurnstileToken
		if token == "" {
			token = req.CFToken
		}
		if policy.TurnstileSecret == "" || policy.TurnstileVerifyHandler == nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "turnstile verification is not configured"})
			return
		}
		if err := policy.TurnstileVerifyHandler.VerifyTurnstile(r.Context(), policy.TurnstileSecret, token, requestRemoteIP(r)); err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "turnstile verification failed"})
			return
		}
	}
	if req.Group == "" {
		req.Group = "default"
	}
	user, err := uc.RegisterWithAffCode(r.Context(), req.Username, req.Password, req.Email, req.Group, req.AffCode)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	creditInvitationBonus(r.Context(), user, billingClient)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"user_id": user.ID}})
}

// creditInvitationBonus best-effort credits the invitee + inviter via the billing service so a ledger row is written.
// Failure is swallowed because the user record is already committed; admin /api/topup can grant the bonus manually if needed.
func creditInvitationBonus(ctx context.Context, user *biz.User, billingClient billingv1.BillingServiceClient) {
	if user == nil || user.InviterID == 0 || billingClient == nil {
		return
	}
	if bonus := positiveEnvInt64("INVITEE_BONUS_QUOTA"); bonus > 0 {
		_, _ = billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     strconv.FormatInt(user.ID, 10),
			Amount:     bonus,
			OperatorId: "system_invitation",
			Remark:     "invitation invitee bonus",
		})
	}
	if bonus := positiveEnvInt64("INVITER_BONUS_QUOTA"); bonus > 0 {
		_, _ = billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     strconv.FormatInt(user.InviterID, 10),
			Amount:     bonus,
			OperatorId: "system_invitation",
			Remark:     fmt.Sprintf("invitation inviter bonus, invitee=%d", user.ID),
		})
	}
}

func positiveEnvInt64(key string) int64 {
	value, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func emailDomainAllowed(email string, whitelist []string) bool {
	_, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || domain == "" {
		return false
	}
	domain = strings.ToLower(domain)
	for _, allowed := range whitelist {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed != "" && domain == allowed {
			return true
		}
	}
	return false
}

func requestRemoteIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		return strings.TrimSpace(first)
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, ok := strings.Cut(r.RemoteAddr, ":")
	if ok {
		return host
	}
	return r.RemoteAddr
}

func (v *defaultTurnstileVerifier) VerifyTurnstile(ctx context.Context, secret, token, remoteIP string) error {
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := v.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var payload struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if !payload.Success {
		return fmt.Errorf("turnstile verification failed")
	}
	return nil
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

func handleAffTransferDisabled(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	if _, err := authSnapshotFromRequest(r, uc); err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "aff transfer is not supported"})
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

func handleUserLogs(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
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

	page := queryInt32(r, "page", 1)
	pageSize := queryInt32(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	resp, err := billingClient.ListLedger(r.Context(), &billingv1.ListLedgerRequest{
		UserId:   strconv.FormatInt(snapshot.UserID, 10),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}

	logType := strings.TrimSpace(r.URL.Query().Get("type"))
	items := make([]map[string]interface{}, 0, len(resp.GetEntries()))
	for _, entry := range resp.GetEntries() {
		if logType != "" && entry.GetType() != logType {
			continue
		}
		createdAt := int64(0)
		if entry.GetCreatedAt() != nil {
			createdAt = entry.GetCreatedAt().AsTime().Unix()
		}
		items = append(items, map[string]interface{}{
			"id":                entry.GetId(),
			"user_id":           entry.GetUserId(),
			"type":              entry.GetType(),
			"amount":            entry.GetAmount(),
			"balance_after":     entry.GetBalanceAfter(),
			"reference_id":      entry.GetReferenceId(),
			"remark":            entry.GetRemark(),
			"created_at":        createdAt,
			"token_name":        entry.GetTokenName(),
			"model_name":        entry.GetModelName(),
			"quota":             entry.GetQuota(),
			"prompt_tokens":     entry.GetPromptTokens(),
			"completion_tokens": entry.GetCompletionTokens(),
			"channel_id":        entry.GetChannelId(),
			"elapsed_time":      entry.GetElapsedTime(),
			"is_stream":         entry.GetIsStream(),
			"endpoint":          entry.GetEndpoint(),
		})
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{
		"items": items,
		"total": resp.GetTotal(),
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

func handleCreatePaymentOrder(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
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
		Amount        float64 `json:"amount"`
		PaymentMethod string  `json:"payment_method"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Amount <= 0 {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "amount must be positive"})
		return
	}
	if req.PaymentMethod == "" {
		req.PaymentMethod = "alipay"
	}
	switch req.PaymentMethod {
	case "alipay", "wechat", "mock":
	default:
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "invalid payment_method"})
		return
	}
	assetAmount := int64(req.Amount * float64(paymentQuotaPerCNY))
	if assetAmount <= 0 {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "amount too small"})
		return
	}
	moneyCents := int64(req.Amount * 100)
	if moneyCents <= 0 {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "amount too small"})
		return
	}
	resp, err := billingClient.CreatePaymentOrder(r.Context(), &billingv1.CreatePaymentOrderRequest{
		UserId:      strconv.FormatInt(snapshot.UserID, 10),
		Channel:     req.PaymentMethod,
		AssetType:   "quota",
		AssetAmount: assetAmount,
		MoneyCents:  moneyCents,
		Currency:    "CNY",
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	if !resp.GetSuccess() {
		message := resp.GetErrorMessage()
		if message == "" {
			message = "payment order creation failed"
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: message})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Message: "",
		Data: map[string]interface{}{
			"trade_no":   resp.GetOrder().GetTradeNo(),
			"pay_url":    resp.GetOrder().GetPayUrl(),
			"order":      resp.GetOrder(),
			"asset_type": resp.GetOrder().GetAssetType(),
		},
	})
}

func handleUserPaymentOrders(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
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
	userID := strconv.FormatInt(snapshot.UserID, 10)
	user, err := uc.GetUser(r.Context(), snapshot.UserID)
	if err == nil && user.IsAdmin() {
		userID = ""
	}
	query := r.URL.Query()
	pageSize := queryInt32(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	resp, err := billingClient.ListPaymentOrders(r.Context(), &billingv1.ListPaymentOrdersRequest{
		Page:     queryInt32(r, "page", 1),
		PageSize: pageSize,
		UserId:   userID,
		Status:   query.Get("status"),
		Channel:  query.Get("channel"),
		TradeNo:  query.Get("trade_no"),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: resp})
}

func handleUserPaymentOrderByTradeNo(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
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
	tradeNo := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/user/payment/orders/"), "/")
	if tradeNo == "" || strings.Contains(tradeNo, "/") {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "trade_no is required"})
		return
	}
	resp, err := billingClient.GetPaymentOrderByTradeNo(r.Context(), &billingv1.GetPaymentOrderByTradeNoRequest{TradeNo: tradeNo})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	if !resp.GetSuccess() {
		message := resp.GetErrorMessage()
		if message == "" {
			message = "payment order not found"
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: message})
		return
	}
	order := resp.GetOrder()
	user, userErr := uc.GetUser(r.Context(), snapshot.UserID)
	isAdminUser := userErr == nil && user.IsAdmin()
	if !isAdminUser && order.GetUserId() != strconv.FormatInt(snapshot.UserID, 10) {
		writeJSON(w, http.StatusForbidden, apiResponse{Success: false, Message: "forbidden"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{"order": order}})
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
	models := snapshot.AllowedModels
	if len(models) == 0 {
		models = defaultAvailableModels()
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: models})
}

func defaultAvailableModels() []string {
	return []string{
		"gpt-4o-mini",
		"gpt-4o",
		"gpt-4-turbo",
		"gpt-3.5-turbo",
		"text-embedding-3-small",
		"text-embedding-3-large",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"gemini-pro",
		"gemini-pro-vision",
		"deepseek-chat",
		"deepseek-reasoner",
		"qwen-turbo",
		"qwen-plus",
		"qwen-max",
		"glm-4",
		"moonshot-v1-8k",
		"moonshot-v1-32k",
		"mistral-large-latest",
		"command-r-plus",
		"voyage-3",
	}
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

func handleOneAPIOAuthBind(w http.ResponseWriter, r *http.Request, registry *oauth.ProviderRegistry, uc *biz.IdentityUsecase, providerName string) {
	if registry == nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: providerName + " oauth disabled"})
		return
	}
	provider, ok := registry.Get(providerName)
	if !ok {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: providerName + " oauth disabled"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid token"})
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		handleOAuthAuthorize(w, r, provider)
		return
	}
	userInfo, err := provider.Exchange(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "oauth provider error"})
		return
	}
	user, err := uc.BindOAuthIdentity(r.Context(), snapshot.UserID, userInfo.Provider, userInfo.ProviderID)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]interface{}{
		"user_id":        user.ID,
		"oauth_provider": user.OAuthProvider,
	}})
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
		if err := uc.DeleteAccessTokenForAuth(r.Context(), snapshot, tokenID); err != nil {
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
		"role":         user.Role,
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
	} else {
		data["masked_key"] = maskTokenKey(token.Key)
	}
	return data
}

func maskTokenKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
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
