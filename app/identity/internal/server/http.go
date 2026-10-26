package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"google.golang.org/grpc/metadata"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"micro-one-api/domain/authorization"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/iamdto"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/service"
	xhttp "micro-one-api/platform/http"
	"micro-one-api/platform/metrics"
	appmiddleware "micro-one-api/platform/middleware"
	oauth "micro-one-api/platform/security"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type TurnstileVerifier interface {
	VerifyTurnstile(ctx context.Context, secret, token, remoteIP string) error
}

type RegistrationPolicy struct {
	Enabled                       bool
	EmailVerificationEnabled      bool
	EmailDomainRestrictionEnabled bool
	EmailDomainWhitelist          []string
	TurnstileCheckEnabled         bool
	TurnstileSecret               string
	TurnstileSiteKey              string
	TurnstileVerifyHandler        TurnstileVerifier
	Challenges                    *biz.EmailChallengeUsecase
	// Deprecated: inject Challenges with its shared repo and mail deliverer.
	CodeDeliverer CodeDeliverer
}

type CodeDeliverer = biz.CodeDeliverer

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
	// khttp.Filter applies an http.Handler middleware to every route on the
	// server's mux. The RequestID filter stamps a sanitized request ID into
	// context (X-Request-ID header or a generated UUID) so audit records and
	// structured logs carry a traceable identifier — the same pattern
	// admin-api uses in newAdminGuard.
	srv := xhttp.NewServer(
		khttp.RequestDecoder(iamdto.DecodeRequest), khttp.ResponseEncoder(iamdto.EncodeResponse),
		khttp.Address(addr),
		khttp.Filter(appmiddleware.RequestID),
		khttp.Filter(publicIdentityRateLimit(uc)),
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
			writeJSON(w, http.StatusOK, map[string]any{
				"providers": oauthRegistry.Names(),
			})
		})
		srv.HandlePrefix("/v1/oauth/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleOAuth(w, r, oauthRegistry, uc, billingClient)
		}))
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
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	identityv1.RegisterIAMServiceHTTPServer(srv, service.NewIAMService(nil, uc))
	srv.HandleFunc("/api/user/register", func(w http.ResponseWriter, r *http.Request) {
		handleRegister(w, r, uc, registrationPolicy, billingClient)
	})
	srv.HandleFunc("/api/registration/policy", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrationPolicy(w, r, registrationPolicy)
	})
	srv.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		handleLogin(w, r, uc)
	})
	srv.HandleFunc("/api/user/logout", func(w http.ResponseWriter, r *http.Request) {
		handleLogout(w, r, uc)
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
		handleEmailBind(w, r, uc, registrationPolicy.Challenges)
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
		handleEmailVerification(w, r, registrationPolicy)
	})
	srv.HandleFunc("/api/reset_password", func(w http.ResponseWriter, r *http.Request) {
		handleResetPasswordRequest(w, r, registrationPolicy.Challenges, uc)
	})
	srv.HandleFunc("/api/user/reset", func(w http.ResponseWriter, r *http.Request) {
		handleResetPassword(w, r, uc, registrationPolicy.Challenges)
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
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type oauthBindRecord struct {
	Token    string
	At       time.Time
	Provider string
}

var oauthBindStore = struct {
	sync.Mutex
	items map[string]oauthBindRecord
}{
	items: make(map[string]oauthBindRecord),
}

func handleRegister(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, policy RegistrationPolicy, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	var req struct {
		Username         string `json:"username"`
		Password         string `json:"password"`
		Email            string `json:"email"`
		Group            string `json:"group"`
		AffCode          string `json:"aff_code"`
		VerificationCode string `json:"verification_code"`
		TurnstileToken   string `json:"turnstile_token"`
		CFToken          string `json:"cf_turnstile_response"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !policy.Enabled {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "registration disabled"})
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if strings.TrimSpace(req.Username) == "" || len(req.Username) > 128 || len(req.Password) < 8 || len(req.Password) > 72 {
		writeJSON(w, http.StatusOK, apiResponse{Message: "invalid parameter"})
		return
	}
	if (req.Email != "" || policy.EmailVerificationEnabled || policy.EmailDomainRestrictionEnabled) && !validEmail(req.Email) {
		writeJSON(w, http.StatusOK, apiResponse{Message: "invalid email"})
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
	verifiedEmail := false
	if policy.EmailVerificationEnabled && req.VerificationCode == "" {
		writeJSON(w, http.StatusOK, apiResponse{Message: "email verification code is required"})
		return
	}
	if req.VerificationCode != "" {
		if _, err := policy.Challenges.Consume(r.Context(), biz.ChallengeVerification, req.Email, req.VerificationCode); err != nil {
			writeChallengeError(w, err)
			return
		}
		verifiedEmail = true
	}
	// M5: ignore any client-supplied group on public registration. The
	// group drives channel access and billing ratios, so self-selection
	// would let a registrant join a privileged or discounted group. Force
	// the default group; admins assign other groups explicitly.
	user, err := uc.RegisterWithAffCode(r.Context(), req.Username, req.Password, req.Email, "default", req.AffCode)
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	creditRegistrationRewards(r.Context(), user, uc, billingClient, verifiedEmail)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{"user_id": user.ID}})
}

// creditRegistrationRewards best-effort credits the registration rewards
// (new-user amount plus invitee/inviter invitation bonuses) via the billing
// service so a ledger row is written for each grant. Amounts come from the
// AmountForNewUser / AmountForInviter / AmountForInvitee system options
// configured in the admin UI (quota units); when an invitation bonus option
// is not configured, the legacy INVITE*_BONUS_* env vars are used instead.
// Failures are swallowed because the user record is already committed;
// admin /api/topup can grant the reward manually if needed.
func creditRegistrationRewards(ctx context.Context, user *biz.User, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient, verifiedEmail bool) {
	if user == nil || billingClient == nil {
		return
	}
	rewards := uc.GetRegistrationRewards(ctx)
	if rewards.NewUser > 0 {
		_, _ = billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     strconv.FormatInt(user.ID, 10),
			Amount:     rewards.NewUser,
			OperatorId: "system_register",
			Remark:     "new user registration reward",
			RequestId:  "registration:new-user:" + strconv.FormatInt(user.ID, 10),
		})
	}
	// Invitation payouts require ownership proof instead of a freely supplied address.
	if user.InviterID == 0 || !verifiedEmail {
		return
	}
	inviteeAmount := rewards.Invitee
	if !rewards.InviteeSet {
		inviteeAmount = positiveEnvInt64("INVITEE_BONUS_AMOUNT", "INVITEE_BONUS_QUOTA")
	}
	inviterAmount := rewards.Inviter
	if !rewards.InviterSet {
		inviterAmount = positiveEnvInt64("INVITER_BONUS_AMOUNT", "INVITER_BONUS_QUOTA")
	}
	if inviteeAmount > 0 {
		_, _ = billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     strconv.FormatInt(user.ID, 10),
			Amount:     inviteeAmount,
			OperatorId: "system_invitation",
			Remark:     "invitation invitee bonus",
			RequestId:  "registration:invitee:" + strconv.FormatInt(user.ID, 10),
		})
	}
	if inviterAmount > 0 {
		_, _ = billingClient.TopUpQuota(ctx, &billingv1.TopUpQuotaRequest{
			UserId:     strconv.FormatInt(user.InviterID, 10),
			Amount:     inviterAmount,
			OperatorId: "system_invitation",
			Remark:     fmt.Sprintf("invitation inviter bonus, invitee=%d", user.ID),
			RequestId:  "registration:inviter:" + strconv.FormatInt(user.ID, 10),
		})
	}
}

func positiveEnvInt64(keys ...string) int64 {
	for _, key := range keys {
		raw, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0
		}
		return value
	}
	return 0
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
	return xhttp.ClientIP(r, xhttp.TrustedProxyCIDRsFromEnv("IDENTITY_TRUSTED_PROXY_CIDRS"))
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
	if err := jsonx.NewDecoder(resp.Body).Decode(&payload); err != nil {
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
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
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

func handleEmailBind(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, challenges *biz.EmailChallengeUsecase) {
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
	if _, err := challenges.Consume(r.Context(), biz.ChallengeVerification, email, code); err != nil {
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
	userID := strconv.FormatInt(snapshot.UserID, 10)
	resp, err := billingClient.GetAccountSnapshot(billingSelfContext(r.Context()), &billingv1.GetAccountSnapshotRequest{
		UserId: userID,
	})
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	account := resp.GetSnapshot()

	// Fetch aggregated ledger data for usage trend (last 7 days, server-side aggregation)
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sevenDaysAgo := startOfDay.AddDate(0, 0, -6)

	aggResp, aggErr := billingClient.AggregateLedgerByDate(billingSelfContext(r.Context()), &billingv1.AggregateLedgerByDateRequest{
		UserId:    userID,
		StartTime: timestamppb.New(sevenDaysAgo),
		EndTime:   timestamppb.New(now),
		Type:      "consume",
	})
	if aggErr != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "failed to aggregate usage: " + aggErr.Error()})
		return
	}

	// Build lookup from aggregated daily data
	type dailyUsage struct {
		Quota               int64
		PromptTokens        int64
		CompletionTokens    int64
		CacheReadTokens     int64
		CacheCreationTokens int64
		UncachedInputTokens int64
		BillableTotalTokens int64
		Count               int64
		ElapsedTime         int64
	}
	dayMap := map[string]*dailyUsage{}
	for d := sevenDaysAgo; !d.After(startOfDay); d = d.AddDate(0, 0, 1) {
		dayMap[d.Format("2006-01-02")] = &dailyUsage{}
	}

	var todayAmount, todayPromptTokens, todayCompletionTokens, todayCacheReadTokens int64
	var totalElapsedTime, consumeCount int64

	if aggResp != nil {
		for _, d := range aggResp.GetDaily() {
			if day, ok := dayMap[d.GetDate()]; ok {
				day.Quota = d.GetQuota()
				day.PromptTokens = d.GetPromptTokens()
				day.CompletionTokens = d.GetCompletionTokens()
				day.CacheReadTokens = d.GetCacheReadTokens()
				day.CacheCreationTokens = d.GetCacheCreation_5MTokens() + d.GetCacheCreation_1HTokens()
				day.UncachedInputTokens = d.GetUncachedInputTokens()
				day.BillableTotalTokens = d.GetBillableTotalTokens()
				day.Count = d.GetCount()
				day.ElapsedTime = d.GetElapsedTime()
			}
			if d.GetDate() == startOfDay.Format("2006-01-02") {
				todayAmount = d.GetQuota()
				todayPromptTokens = d.GetPromptTokens()
				todayCompletionTokens = d.GetCompletionTokens()
				todayCacheReadTokens = d.GetCacheReadTokens()
			}
			totalElapsedTime += d.GetElapsedTime()
			consumeCount += d.GetCount()
		}
	}

	// Build sorted usage array
	var usageArr []map[string]any
	for d := sevenDaysAgo; !d.After(startOfDay); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		day := dayMap[key]
		usageArr = append(usageArr, map[string]any{
			"date":                  key,
			"count":                 day.Count,
			"amount":                day.Quota,
			"prompt_tokens":         day.PromptTokens,
			"completion_tokens":     day.CompletionTokens,
			"cache_read_tokens":     day.CacheReadTokens,
			"cache_creation_tokens": day.CacheCreationTokens,
			"uncached_input_tokens": day.UncachedInputTokens,
			"billable_total_tokens": day.BillableTotalTokens,
		})
	}

	// Average latency (ms)
	var avgLatency int64
	if consumeCount > 0 {
		avgLatency = totalElapsedTime / consumeCount
	}

	// Model distribution (top 10, from server-side aggregation)
	var modelDistribution []map[string]any
	if aggResp != nil {
		for _, m := range aggResp.GetModels() {
			if m.GetModel() == "" {
				continue
			}
			if len(modelDistribution) >= 10 {
				break
			}
			modelDistribution = append(modelDistribution, map[string]any{
				"model":  m.GetModel(),
				"tokens": m.GetTokens(),
			})
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{
		"balance":                 account.GetBalance(),
		"used_amount":             account.GetUsedAmount(),
		"request_count":           account.GetRequestCount(),
		"group":                   account.GetGroup(),
		"group_ratio":             account.GetGroupRatio(),
		"frozen_amount":           account.GetFrozenAmount(),
		"usage":                   usageArr,
		"today_amount":            todayAmount,
		"today_prompt_tokens":     todayPromptTokens,
		"today_completion_tokens": todayCompletionTokens,
		"today_cache_read_tokens": todayCacheReadTokens,
		"avg_latency":             avgLatency,
		"model_distribution":      modelDistribution,
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
	pageSize := min(queryInt32(r, "page_size", 20), 100)
	logType := strings.TrimSpace(r.URL.Query().Get("type"))
	userID := strconv.FormatInt(snapshot.UserID, 10)

	resp, err := billingClient.ListLedger(billingSelfContext(r.Context()), &billingv1.ListLedgerRequest{
		UserId:   userID,
		Page:     page,
		PageSize: pageSize,
		Type:     logType,
	})
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}

	items := make([]map[string]any, 0)
	for _, entry := range resp.GetEntries() {
		items = append(items, ledgerEntryToMap(entry))
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{
		"items": items,
		"total": resp.GetTotal(),
	}})
}

func ledgerEntryToMap(entry *commonv1.LedgerEntry) map[string]any {
	createdAt := int64(0)
	if entry.GetCreatedAt() != nil {
		createdAt = entry.GetCreatedAt().AsTime().Unix()
	}
	return map[string]any{
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
		"cache_read_tokens": entry.GetCacheReadTokens(),
		"channel_id":        entry.GetChannelId(),
		"elapsed_time":      entry.GetElapsedTime(),
		"is_stream":         entry.GetIsStream(),
		"endpoint":          entry.GetEndpoint(),
		"cost_source":       entry.GetCostSource(),
		"subscription_cost": entry.GetSubscriptionCost(),
		"balance_cost":      entry.GetBalanceCost(),
		"ledger_dedupe_key": entry.GetLedgerDedupeKey(),
		// §9.1 display contract: reported vs billable vs canonical buckets.
		"cache_creation_5m_tokens": entry.GetCacheCreation_5MTokens(),
		"cache_creation_1h_tokens": entry.GetCacheCreation_1HTokens(),
		"uncached_input_tokens":    entry.GetUncachedInputTokens(),
		"reported_prompt_tokens":   entry.GetReportedPromptTokens(),
		"reported_total_tokens":    entry.GetReportedTotalTokens(),
		"billable_total_tokens":    entry.GetBillableTotalTokens(),
		"usage_semantics":          entry.GetUsageSemantics(),
		"usage_parse_status":       entry.GetUsageParseStatus(),
		"usage_decision_reason":    entry.GetUsageDecisionReason(),
	}
}

func handleDashboardBillingUsage(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	snapshot, err := authSnapshotFromRequest(r, uc)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "unauthorized", "type": "one_api_error"}})
		return
	}
	if billingClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": map[string]any{"message": "billing service unavailable", "type": "one_api_error"}})
		return
	}
	resp, err := billingClient.GetAccountSnapshot(billingSelfContext(r.Context()), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": map[string]any{"message": err.Error(), "type": "one_api_error"}})
		return
	}
	usedQuota := int64(0)
	if resp.GetSnapshot() != nil {
		usedQuota = resp.GetSnapshot().GetUsedAmount()
	}
	writeJSON(w, http.StatusOK, map[string]any{
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
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "unauthorized", "type": "one_api_error"}})
		return
	}
	if billingClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": map[string]any{"message": "billing service unavailable", "type": "one_api_error"}})
		return
	}
	resp, err := billingClient.GetAccountSnapshot(billingSelfContext(r.Context()), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": map[string]any{"message": err.Error(), "type": "one_api_error"}})
		return
	}
	limit := int64(0)
	if resp.GetSnapshot() != nil {
		limit = resp.GetSnapshot().GetBalance()
	}
	writeJSON(w, http.StatusOK, map[string]any{
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
	resp, err := billingClient.RedeemCode(billingSelfContext(r.Context()), &billingv1.RedeemCodeRequest{
		UserId: strconv.FormatInt(snapshot.UserID, 10),
		Code:   req.Key,
	})
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
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
	if math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) || req.Amount <= 0 || req.Amount > 1e6 {
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
	moneyCents := int64(math.Round(req.Amount * 100))
	if moneyCents <= 0 {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "amount too small"})
		return
	}
	assetAmount, err := subscriptionbiz.WalletAmountForMoneyCents(moneyCents, subscriptionbiz.RechargeAmountMultiplier())
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "amount exceeds supported range"})
		return
	}
	resp, err := billingClient.CreatePaymentOrder(billingSelfContext(r.Context()), &billingv1.CreatePaymentOrderRequest{
		UserId:      strconv.FormatInt(snapshot.UserID, 10),
		Channel:     req.PaymentMethod,
		AssetType:   "balance",
		AssetAmount: assetAmount,
		MoneyCents:  moneyCents,
		Currency:    "CNY",
	})
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
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
		Data: map[string]any{
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
	query := r.URL.Query()
	pageSize := min(queryInt32(r, "page_size", 20), 100)
	resp, err := billingClient.ListPaymentOrders(billingSelfContext(r.Context()), &billingv1.ListPaymentOrdersRequest{
		Page:     queryInt32(r, "page", 1),
		PageSize: pageSize,
		UserId:   userID,
		Status:   query.Get("status"),
		Channel:  query.Get("channel"),
		TradeNo:  query.Get("trade_no"),
	})
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
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
	resp, err := billingClient.GetPaymentOrderByTradeNo(billingSelfContext(r.Context()), &billingv1.GetPaymentOrderByTradeNoRequest{TradeNo: tradeNo})
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
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
	mode, modeErr := uc.AuthorizationMode(r.Context())
	if modeErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Success: false, Message: "authorization unavailable"})
		return
	}
	isAdminUser := mode == "legacy" && userErr == nil && user.IsAdmin()
	if !isAdminUser && order.GetUserId() != strconv.FormatInt(snapshot.UserID, 10) {
		writeJSON(w, http.StatusForbidden, apiResponse{Success: false, Message: "forbidden"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{"order": order}})
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
	user, token, err := uc.Login(r.Context(), req.Username, req.Password, requestRemoteIP(r))
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "invalid credentials"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Message: "",
		Data: map[string]any{
			"token":   token,
			"user_id": user.ID,
			"user":    userToMap(user),
		},
	})
}

func handleLogout(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	// M6: logout is no longer a no-op. Advance the user's password epoch so
	// the currently-presented session JWT (and any others sharing it) are
	// rejected on subsequent validation. This relies on the request carrying
	// a valid session; logout without one is still a 200 (no-op reveal).
	if snapshot, err := authSnapshotFromRequest(r, uc); err == nil && snapshot != nil && snapshot.UserID > 0 {
		if err := uc.InvalidateAllSessions(r.Context(), snapshot.UserID); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, apiResponse{Success: false, Message: "session revocation failed"})
			return
		}
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
			Username        string  `json:"username"`
			DisplayName     *string `json:"display_name"`
			Password        string  `json:"password"`
			CurrentPassword string  `json:"current_password"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		displayName := ""
		if req.DisplayName != nil {
			displayName = *req.DisplayName
		}
		if err := uc.UpdateSelf(r.Context(), snapshot.UserID, req.Username, displayName, req.Password, req.CurrentPassword, req.DisplayName != nil); err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: ""})
	case http.MethodDelete:
		if err := uc.DeleteSelf(r.Context(), snapshot.UserID); err != nil {
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

// Public policy is owned here so the BFF and direct Identity callers use the
// same registration and mail requirements. Secrets never cross this boundary.
func handleRegistrationPolicy(w http.ResponseWriter, r *http.Request, policy RegistrationPolicy) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"registration_enabled":             policy.Enabled,
		"email_verification":               policy.EmailVerificationEnabled,
		"email_domain_restriction_enabled": policy.EmailDomainRestrictionEnabled,
		"email_domain_whitelist":           policy.EmailDomainWhitelist,
		"turnstile_check":                  policy.TurnstileCheckEnabled,
		"turnstile_site_key":               policy.TurnstileSiteKey,
		"email_delivery_configured":        policy.Challenges.Available(),
	}})
}

// Accept only a mailbox, without display names or header/control characters.
// Preserve case for compatibility with existing case-sensitive stored bindings.
func validEmail(email string) bool {
	if len(email) > 254 || strings.ContainsAny(email, "\r\n\t ") {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && strings.Contains(email, "@")
}

func writeChallengeError(w http.ResponseWriter, err error) {
	status, message := http.StatusServiceUnavailable, "email service unavailable"
	if errors.Is(err, biz.ErrChallengeRateLimited) {
		status, message = http.StatusTooManyRequests, "too many requests"
		w.Header().Set("Retry-After", "60")
	} else if errors.Is(err, biz.ErrChallengeInvalid) {
		status, message = http.StatusOK, "invalid or expired verification code"
	}
	writeJSON(w, status, apiResponse{Message: message})
}

func handleEmailVerification(w http.ResponseWriter, r *http.Request, policy RegistrationPolicy) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if !validEmail(email) {
		writeJSON(w, http.StatusOK, apiResponse{Message: "invalid email"})
		return
	}
	if policy.EmailDomainRestrictionEnabled && !emailDomainAllowed(email, policy.EmailDomainWhitelist) {
		writeJSON(w, http.StatusOK, apiResponse{Message: "email domain is not allowed"})
		return
	}
	if err := policy.Challenges.IssueVerification(r.Context(), email); err != nil {
		writeChallengeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "verification code sent"})
}

const recoveryAcceptedMessage = "If the account is eligible, password reset instructions will be sent. Please try again later if no email arrives."

func handleResetPasswordRequest(w http.ResponseWriter, r *http.Request, challenges *biz.EmailChallengeUsecase, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if !validEmail(email) {
		writeJSON(w, http.StatusOK, apiResponse{Message: "invalid email"})
		return
	}
	// Check process-wide configuration before account lookup; a missing service
	// yields the same result for every address. Per-account errors below always
	// use a neutral acknowledgement, including delivery failures and cooldowns.
	if !challenges.Available() {
		writeChallengeError(w, biz.ErrMailUnavailable)
		return
	}
	if err := challenges.RequestRecovery(r.Context(), email, uc.PrepareEmailRecovery); err != nil {
		writeChallengeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: recoveryAcceptedMessage})
}

func handleResetPassword(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase, challenges *biz.EmailChallengeUsecase) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
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
	req.Email = strings.TrimSpace(req.Email)
	if !validEmail(req.Email) || req.Token == "" || len(req.Password) < 8 || len(req.Password) > 72 {
		writeJSON(w, http.StatusOK, apiResponse{Message: "invalid parameter"})
		return
	}
	record, err := challenges.Consume(r.Context(), biz.ChallengePasswordReset, req.Email, req.Token)
	if err != nil {
		writeChallengeError(w, err)
		return
	}
	if err := uc.ResetPasswordByEmail(biz.WithVerifiedEmailRecovery(r.Context(), req.Email, time.Now(), record.UserID, record.PasswordEpoch), req.Email, req.Password); err != nil {
		// Do not leak account state or database details through a recovery link.
		writeJSON(w, http.StatusOK, apiResponse{Message: "invalid or expired verification code"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "password reset"})
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
	state, err := generateState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Success: false, Message: "failed to generate oauth state"})
		return
	}
	setOAuthStateCookie(w, state)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{"state": state}})
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
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Message: "method not allowed"})
		return
	}
	if registry == nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: providerName + " oauth disabled"})
		return
	}
	provider, ok := registry.Get(providerName)
	if !ok {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: providerName + " oauth disabled"})
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		token, ok := bearerTokenFromRequest(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid token"})
			return
		}
		if _, err := uc.GetSessionSnapshot(r.Context(), token); err != nil {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid token"})
			return
		}
		state, err := generateState()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{Success: false, Message: "failed to generate oauth state"})
			return
		}
		oauthBindStore.Lock()
		for key, record := range oauthBindStore.items {
			if time.Since(record.At) > 5*time.Minute {
				delete(oauthBindStore.items, key)
			}
		}
		if len(oauthBindStore.items) >= 4096 {
			oauthBindStore.Unlock()
			writeJSON(w, http.StatusServiceUnavailable, apiResponse{Message: "oauth binding temporarily unavailable"})
			return
		}
		oauthBindStore.items[state] = oauthBindRecord{Token: token, At: time.Now(), Provider: providerName}
		oauthBindStore.Unlock()
		setOAuthStateCookie(w, state)
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{"auth_url": provider.AuthURL(state)}})
		return
	}
	state := r.URL.Query().Get("state")
	snapshot, redirectOnSuccess, err := oauthBindSnapshotFromRequest(r, uc, providerName, state)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid token"})
		return
	}
	userInfo, err := provider.Exchange(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: "oauth provider error"})
		return
	}
	user, err := uc.BindOAuthIdentity(r.Context(), snapshot.UserID, userInfo.Provider, userInfo.ProviderID)
	if err != nil {
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
		return
	}
	if redirectOnSuccess {
		http.Redirect(w, r, "/profile?oauth_bind=success&provider="+url.QueryEscape(providerName), http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{
		"user_id":        user.ID,
		"oauth_provider": user.OAuthProvider,
	}})
}

func oauthBindSnapshotFromRequest(r *http.Request, uc *biz.IdentityUsecase, providerName, state string) (*biz.AuthSnapshot, bool, error) {
	token, ok := bearerTokenFromRequest(r)
	if ok {
		snapshot, err := uc.GetSessionSnapshot(r.Context(), token)
		if err == nil {
			*r = *r.WithContext(authorization.WithCredential(r.Context(), token))
		}
		return snapshot, false, err
	}
	if state == "" {
		return nil, false, biz.ErrInvalidToken
	}
	cookie, _ := r.Cookie("oauth_state")
	if cookie == nil || cookie.Value != state {
		return nil, false, biz.ErrInvalidToken
	}
	record, found, err := takeOAuthBindRecord(state, providerName)
	if !found || err != nil {
		return nil, false, biz.ErrInvalidToken
	}
	snapshot, err := uc.GetSessionSnapshot(r.Context(), record.Token)
	if err == nil {
		*r = *r.WithContext(authorization.WithCredential(r.Context(), record.Token))
	}
	return snapshot, true, err
}

func takeOAuthBindRecord(state, providerName string) (oauthBindRecord, bool, error) {
	oauthBindStore.Lock()
	record, ok := oauthBindStore.items[state]
	if ok {
		delete(oauthBindStore.items, state)
	}
	oauthBindStore.Unlock()
	if !ok {
		return oauthBindRecord{}, false, nil
	}
	if record.Provider != providerName || time.Since(record.At) > 5*time.Minute {
		return oauthBindRecord{}, true, biz.ErrInvalidToken
	}
	return record, true, nil
}

func handleCreateUserToken(w http.ResponseWriter, r *http.Request, uc *biz.IdentityUsecase) {
	if r.Method != http.MethodPost {
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
		if authz.IsAuthorizationError(err) {
			authz.WriteHTTPError(w, err)
			return
		}
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
			writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, false)})
			return
		}
		page := queryInt32(r, "page", 1)
		pageSize := min(queryInt32(r, "page_size", 20), 100)
		keyword := r.URL.Query().Get("keyword")
		tokens, total, err := uc.ListAccessTokens(r.Context(), snapshot.UserID, page, pageSize, keyword)
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		items := make([]map[string]any, 0, len(tokens))
		for _, token := range tokens {
			items = append(items, tokenToMap(token, false))
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: map[string]any{"items": items, "total": total}})
	case http.MethodPost:
		var req struct {
			RoutingMode    *string  `json:"routing_mode"`
			RoutingGroupID *int64   `json:"routing_group_id"`
			Name           string   `json:"name"`
			Models         []string `json:"models"`
			ExpiredAt      int64    `json:"expired_time"`
			ExpireAt       int64    `json:"expire_at"`
			RemainQuota    int64    `json:"remain_quota"`
			UnlimitedQuota *bool    `json:"unlimited_quota"`
			Subnet         string   `json:"subnet"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.RoutingMode != nil || req.RoutingGroupID != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "use the routing-token API for routing settings"})
			return
		}
		expireAt := req.ExpiredAt
		if expireAt == 0 {
			expireAt = req.ExpireAt
		}
		// When the client omits unlimited_quota (nil pointer), fall back to
		// the biz default (unlimited). A plain bool cannot distinguish
		// "omitted" from an explicit false, so every frontend that only
		// posts {"name"} would otherwise create a zero-quota token that is
		// immediately rejected as TOKEN_EXHAUSTED.
		if req.UnlimitedQuota == nil {
			t := true
			req.UnlimitedQuota = &t
		}
		token, err := uc.CreateAccessToken(r.Context(), snapshot.UserID, req.Name, req.Models, expireAt, biz.CreateAccessTokenOptions{
			RemainQuota:    req.RemainQuota,
			UnlimitedQuota: *req.UnlimitedQuota,
			Subnet:         req.Subnet,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, true)})
	case http.MethodPut:
		var req struct {
			RoutingMode    *string  `json:"routing_mode"`
			RoutingGroupID *int64   `json:"routing_group_id"`
			ID             int64    `json:"id"`
			Name           string   `json:"name"`
			Models         []string `json:"models"`
			ExpiredAt      int64    `json:"expired_time"`
			Status         int32    `json:"status"`
			RemainQuota    int64    `json:"remain_quota"`
			UnlimitedQuota *bool    `json:"unlimited_quota"`
			Subnet         *string  `json:"subnet"`
		}
		req.RemainQuota = -1
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.RoutingMode != nil || req.RoutingGroupID != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "use the routing-token API for routing settings"})
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
		writeJSON(w, http.StatusOK, apiResponse{Success: true, Message: "", Data: tokenToMap(token, false)})
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

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := jsonx.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "invalid request body"})
		return false
	}
	return true
}

func authSnapshotFromRequest(r *http.Request, uc *biz.IdentityUsecase) (*biz.AuthSnapshot, error) {
	token, ok := bearerTokenFromRequest(r)
	if !ok {
		return nil, biz.ErrInvalidToken
	}
	snapshot, err := uc.GetSessionSnapshot(r.Context(), token)
	if err == nil {
		*r = *r.WithContext(authorization.WithCredential(r.Context(), token))
	}
	return snapshot, err
}

func bearerTokenFromRequest(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return "", false
	}
	return token, true
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

func userToMap(user *biz.User) map[string]any {
	return map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"email":        user.Email,
		"group":        user.Group,
		"status":       user.Status,
		"role":         user.Role,
	}
}

func tokenToMap(token *biz.Token, includeKey bool) map[string]any {
	data := map[string]any{
		"routing_mode": token.RoutingMode, "routing_group_id": token.RoutingGroupID, "routing_revision": token.RoutingRevision,
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
func handleOAuth(w http.ResponseWriter, r *http.Request, registry *oauth.ProviderRegistry, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
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
		handleOAuthCallback(w, r, provider, uc, billingClient)
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
	}
}

func handleOAuthAuthorize(w http.ResponseWriter, r *http.Request, provider oauth.Provider) {
	state, err := generateState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Success: false, Message: "failed to generate oauth state"})
		return
	}
	authURL := provider.AuthURL(state)
	safeURL, ok := safeOAuthAuthorizeURL(authURL)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Success: false, Message: "invalid oauth authorize URL"})
		return
	}
	setOAuthStateCookie(w, state)
	http.Redirect(w, r, safeURL.String(), http.StatusFound) // #nosec G710 -- OAuth provider URL is parsed and constrained by safeOAuthAuthorizeURL before redirect.
}

func safeOAuthAuthorizeURL(rawURL string) (*url.URL, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, false
	}
	if u.Host == "" || u.User != nil {
		return nil, false
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLocalOAuthHost(u.Hostname())) {
		return nil, false
	}
	return u, true
}

func isLocalOAuthHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func isSafeOAuthAuthorizeURL(rawURL string) bool {
	_, ok := safeOAuthAuthorizeURL(rawURL)
	return ok
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

func handleOAuthCallback(w http.ResponseWriter, r *http.Request, provider oauth.Provider, uc *biz.IdentityUsecase, billingClient billingv1.BillingServiceClient) {
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

	var bindSnapshot *biz.AuthSnapshot
	bindRecord, bindCallback, bindErr := takeOAuthBindRecord(state, provider.Name())
	if bindCallback {
		if bindErr != nil {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid token"})
			return
		}
		bindSnapshot, bindErr = uc.GetSessionSnapshot(r.Context(), bindRecord.Token)
		if bindErr == nil {
			*r = *r.WithContext(authorization.WithCredential(r.Context(), bindRecord.Token))
		}
		if bindErr != nil {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid token"})
			return
		}
	}

	userInfo, err := provider.Exchange(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "oauth provider error"})
		return
	}
	if bindSnapshot != nil {
		if _, err := uc.BindOAuthIdentity(r.Context(), bindSnapshot.UserID, userInfo.Provider, userInfo.ProviderID); err != nil {
			writeJSON(w, http.StatusOK, apiResponse{Success: false, Message: err.Error()})
			return
		}
		http.Redirect(w, r, "/profile?oauth_bind=success&provider="+url.QueryEscape(provider.Name()), http.StatusFound)
		return
	}

	if !userInfo.EmailVerified {
		userInfo.Email = ""
	}
	user, token, created, err := uc.OAuthLogin(r.Context(), userInfo.Provider, userInfo.ProviderID, userInfo.Username, userInfo.Email, userInfo.DisplayName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if created {
		creditRegistrationRewards(r.Context(), user, uc, billingClient, userInfo.EmailVerified)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"token":    token,
		"user_id":  user.ID,
		"username": user.Username,
	})
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = jsonx.NewEncoder(w).Encode(v)
}

// Propagate only the credential already verified by identity's self guard.
// Billing independently authenticates it; a URL user_id never becomes actor.
func billingSelfContext(ctx context.Context) context.Context {
	raw := authorization.Credential(ctx)
	if raw == "" {
		return ctx
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-operator-authorization", "Bearer "+raw)
	return metadata.NewOutgoingContext(ctx, md)
}
