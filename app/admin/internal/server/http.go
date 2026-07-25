package server

import (
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/safecast"
	"micro-one-api/platform/http"
	"micro-one-api/platform/metrics"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// adminWebAssets serves the admin SPA. The frontend is never embedded
// into the binary; it is resolved from ADMIN_WEB_ROOT (or the optional
// constructor argument) so the built web dist stays out of the git repo.
type adminWebAssets struct {
	root fs.FS
}

// adminRoleContextKey carries the resolved role of an authorised admin
// request so downstream handlers (e.g. /api/admin/access) can report it.
type adminRoleContextKey struct{}

// newAdminGuard returns a middleware that authorises admin requests using
// either the shared ADMIN_TOKEN (system-level, treated as root) or a user
// session token whose owner has role >= RoleAdmin. When the user-token path
// is taken, X-Operator-User-Id is overwritten with the authenticated user id
// so identity-service's operator-vs-target checks cannot be spoofed.
//
// ADMIN_TOKEN being unset no longer rejects everything: the per-user path
// still works, which is what lets admins use the console without the shared
// token.
func newAdminGuard(svc *service.AdminService) func(http.HandlerFunc) http.HandlerFunc {
	adminToken := os.Getenv("ADMIN_TOKEN")
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
				return
			}
			token := strings.TrimPrefix(authHeader, "Bearer ")

			if adminToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) == 1 {
				next(w, r.WithContext(context.WithValue(r.Context(), adminRoleContextKey{}, service.RoleRoot)))
				return
			}

			if svc != nil {
				userID, role, err := svc.AuthorizeAdminToken(r.Context(), token)
				if err == nil && role >= service.RoleAdmin {
					r.Header.Set("X-Operator-User-Id", strconv.FormatInt(userID, 10))
					next(w, r.WithContext(context.WithValue(r.Context(), adminRoleContextKey{}, role)))
					return
				}
			}

			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin credentials"})
		}
	}
}

// adminOperatorIDFromRequest returns the authenticated admin's user id, which
// newAdminGuard stamps onto the X-Operator-User-Id header after validating the
// bearer token. Admin-only handlers use it so audit fields (e.g. the refund
// operator id) reflect the real caller instead of a client-supplied value.
func adminOperatorIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-Operator-User-Id"))
}

// NewHTTPServer wires HTTP transport for admin-api.
//
// Optional arguments are kept for backwards-compatible tests and older wire
// call sites: first is identity HTTP endpoint, second is external web root.
func NewHTTPServer(addr string, svc *service.AdminService, options ...string) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)
	identityProxy := newServiceReverseProxy(optionString(options, 0))
	billingHTTPProxy := newBillingHTTPProxy()
	notifyWorkerProxy := newNotifyWorkerProxy()
	channelHTTPProxy := newChannelHTTPProxy()
	webAssets := newAdminWebAssets(optionString(options, 1))
	handlePage := webAssets.handlePage
	adminAuth := newAdminGuard(svc)

	// Health and metrics (unauthenticated)
	srv.HandleFunc("/", handlePage)
	srv.HandleFunc("/admin", handlePage)
	srv.HandleFunc("/admin/", handlePage)
	// SPA client-side routes — must mirror entries in web/src/router.tsx
	srv.HandleFunc("/login", handlePage)
	srv.HandleFunc("/register", handlePage)
	srv.HandleFunc("/dashboard", handlePage)
	srv.HandleFunc("/tokens", handlePage)
	srv.HandleFunc("/usage", handlePage)
	srv.HandleFunc("/api-guide", handlePage)
	srv.HandleFunc("/pricing", handlePage)
	srv.HandleFunc("/recharge", handlePage)
	srv.HandleFunc("/redeem", handlePage)
	srv.HandleFunc("/orders", handlePage)
	srv.HandleFunc("/profile", handlePage)
	srv.HandleFunc("/subscriptions", handlePage)
	srv.HandleFunc("/admin/users", handlePage)
	srv.HandleFunc("/admin/channels", handlePage)
	srv.HandleFunc("/admin/models", handlePage)
	srv.HandleFunc("/admin/channel-health", handlePage)
	srv.HandleFunc("/admin/cost-analysis", handlePage)
	srv.HandleFunc("/admin/pricing", handlePage)
	srv.HandleFunc("/admin/logs", handlePage)
	srv.HandleFunc("/admin/payment-orders", handlePage)
	srv.HandleFunc("/admin/reconciliation", handlePage)
	srv.HandleFunc("/admin/redemptions", handlePage)
	srv.HandleFunc("/admin/options", handlePage)
	srv.HandleFunc("/admin/subscription-accounts", handlePage)
	srv.HandleFunc("/admin/subscription-groups", handlePage)
	srv.HandleFunc("/admin/subscription-plans", handlePage)
	srv.HandleFunc("/admin/subscriptions", handlePage)
	// Static assets bundled by Vite
	srv.HandlePrefix("/assets/", http.HandlerFunc(handlePage))
	srv.HandleFunc("/favicon.svg", handlePage)
	srv.HandleFunc("/logo-icon.svg", handlePage)
	srv.HandleFunc("/logo-wordmark.svg", handlePage)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	srv.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && billingHTTPProxy != nil {
			proxyReq := r.Clone(r.Context())
			proxyReq.URL.Path = "/api/v1/user/payments/alipay/notify"
			proxyReq.URL.RawPath = ""
			billingHTTPProxy.ServeHTTP(w, proxyReq)
			return
		}
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		serverAddress := ""
		if svc != nil {
			if v, err := svc.GetOneAPIOption(r.Context(), "ServerAddress"); err == nil {
				serverAddress = v
			}
		}
		systemName := "micro-one-api"
		if svc != nil {
			if v, err := svc.GetOneAPIOption(r.Context(), "SystemName"); err == nil && v != "" {
				systemName = v
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "",
			"data": map[string]interface{}{
				"version":              "micro-one-api",
				"system_name":          systemName,
				"server_address":       serverAddress,
				"registration_enabled": true,
				"email_verification":   false,
				"github_oauth":         false,
				"wechat_login":         false,
				"turnstile_check":      false,
				"display_in_currency":  false,
			},
		})
	})

	srv.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		// Authenticate user
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{
					"message": "missing or invalid authorization header",
					"type":    "invalid_request_error",
				},
			})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Validate token with identity service
		validated, err := svc.ValidateToken(r.Context(), token)
		if err != nil || !validated {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{
					"message": "invalid API key",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// Get models from active channels
		channels, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
			Page:     1,
			PageSize: 1000,
			Status:   1, // active channels only
		})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{
				"error": map[string]interface{}{
					"message": "failed to fetch available models",
					"type":    "server_error",
				},
			})
			return
		}

		// Collect unique models from channels (case-insensitive dedup).
		modelSet := map[string]string{} // lowercased model -> provider
		for _, ch := range channels.GetChannels() {
			if ch.GetModels() == "" {
				continue
			}
			for _, model := range strings.Split(ch.GetModels(), ",") {
				model = strings.TrimSpace(model)
				if model != "" {
					key := strings.ToLower(model)
					if _, exists := modelSet[key]; !exists {
						modelSet[key] = providerNameFromType(ch.GetType())
					}
				}
			}
		}

		// If no models from channels, return empty list
		// (don't use fallback catalog as it may show unavailable models)

		// Build response
		type modelItem struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}
		data := make([]modelItem, 0, len(modelSet))
		for model, ownedBy := range modelSet {
			data = append(data, modelItem{
				ID:      model,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: ownedBy,
			})
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   data,
		})
	})
	srv.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		handleReadonlyPricing(w, r, svc)
	})
	srv.HandleFunc("/api/user/limits", handleUserLimits)
	srv.HandleFunc("/api/notice", handleContentRoute(adminAuth, svc, "Notice"))
	srv.HandleFunc("/api/about", handleContentRoute(adminAuth, svc, "About"))
	srv.HandleFunc("/api/home_page_content", handleContentRoute(adminAuth, svc, "HomePageContent"))
	if identityProxy != nil {
		srv.HandleFunc("/api/user/register", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/login", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/logout", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/self", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/dashboard", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/quota", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/logs", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/payment/orders", identityProxy.ServeHTTP)
		srv.HandlePrefix("/api/user/payment/orders/", identityProxy)
		srv.HandleFunc("/api/user/topup", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/amount", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/pay", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/token", identityProxy.ServeHTTP)
		srv.HandlePrefix("/api/token/", identityProxy)
		srv.HandlePrefix("/api/oauth/", identityProxy)
		srv.HandlePrefix("/v1/oauth/", identityProxy)
	}
	if billingHTTPProxy != nil {
		srv.HandleFunc("/api/v1/user/payments/alipay/notify", billingHTTPProxy.ServeHTTP)
		srv.HandleFunc("/api/user/payments/alipay/notify", billingHTTPProxy.ServeHTTP)
	}
	srv.HandlePrefix("/api/group", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGroupManagement(w, r, svc)
	}))
	srv.HandleFunc("/api/admin/access", adminAuth(handleAdminAccess))
	srv.HandleFunc("/api/admin/summary", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleAdminSummary(w, r, svc)
	}))
	srv.HandleFunc("/api/v1/subscriptions/progress", func(w http.ResponseWriter, r *http.Request) {
		handleCurrentSubscriptionProgress(w, r, svc)
	})
	// User-facing self-purchase: the buyer is resolved from the bearer token
	// inside the handlers, so these are intentionally not behind adminAuth.
	srv.HandleFunc("/api/v1/subscriptions/groups", func(w http.ResponseWriter, r *http.Request) {
		handlePurchasableSubscriptionGroups(w, r, svc)
	})
	srv.HandleFunc("/api/v1/subscriptions/plans", func(w http.ResponseWriter, r *http.Request) {
		handlePurchasableSubscriptionPlans(w, r, svc)
	})
	srv.HandleFunc("/api/v1/subscriptions/purchase", func(w http.ResponseWriter, r *http.Request) {
		handlePurchaseSubscription(w, r, svc)
	})
	srv.HandleFunc("/api/v1/subscriptions/purchase/payment", func(w http.ResponseWriter, r *http.Request) {
		handlePurchaseSubscriptionWithPayment(w, r, svc)
	})
	srv.HandleFunc("/api/v1/subscriptions/purchase/complete", func(w http.ResponseWriter, r *http.Request) {
		handleCompleteSubscriptionPurchase(w, r, svc)
	})
	srv.HandleFunc("/api/v1/admin/subscriptions", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUserSubscriptions(w, r, svc)
	}))
	srv.HandleFunc("/api/v1/admin/subscriptions/assign", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleAssignSubscription(w, r, svc)
	}))
	// Subscription change (phase 2.4): upgrade/downgrade the user's active
	// subscription. The operator id is the admin caller.
	srv.HandleFunc("/api/v1/admin/subscriptions/change", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleChangeSubscription(w, r, svc)
	}))
	// Subscription operational report (phase 2.5): plan-dimension aggregation
	// of new/renewal/refund counts, revenue, active/expired/revoked
	// subscriptions and subscription vs balance-fallback usage.
	srv.HandleFunc("/api/v1/admin/subscriptions/operation-report", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionOperationReport(w, r, svc)
	}))
	srv.HandlePrefix("/api/v1/admin/subscriptions/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionByID(w, r, svc)
	}))
	srv.HandleFunc("/api/v1/admin/subscription-groups", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionGroups(w, r, svc)
	}))
	srv.HandlePrefix("/api/v1/admin/subscription-groups/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionGroupByID(w, r, svc)
	}))
	srv.HandleFunc("/api/v1/admin/subscription-plans", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionPlans(w, r, svc)
	}))
	srv.HandlePrefix("/api/v1/admin/subscription-plans/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionPlanByID(w, r, svc)
	}))
	// Subscription-account OAuth authorization-code binding lives only on
	// channel-service HTTP; proxy the SPA's requests through so the admin UI can
	// reach auth-url/exchange. Guarded by adminAuth like the routes above.
	if channelHTTPProxy != nil {
		srv.HandlePrefix("/api/v1/admin/accounts/subscription/oauth/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
			channelHTTPProxy.ServeHTTP(w, r)
		}))
	}

	// Protected admin endpoints
	srv.HandlePrefix("/api/user/disable/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserStatusAlias(w, r, svc, 2, "/api/user/disable/")
	}))
	srv.HandlePrefix("/api/user/enable/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserStatusAlias(w, r, svc, 1, "/api/user/enable/")
	}))
	srv.HandleFunc("/api/user/export", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportUsers(w, r, svc)
	}))
	srv.HandlePrefix("/api/user/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserByID(w, r, svc)
	}))
	srv.HandlePrefix("/api/user", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUsers(w, r, svc)
	}))
	srv.HandleFunc("/v1/users", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUsers(w, r, svc)
	}))
	srv.HandlePrefix("/v1/users/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUserByID(w, r, svc)
	}))

	srv.HandleFunc("/api/channel/models", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelModels(w, r, svc)
	}))
	srv.HandleFunc("/v1/channels", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleChannels(w, r, svc)
	}))
	srv.HandlePrefix("/v1/channels/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleChannelByID(w, r, svc)
	}))
	srv.HandleFunc("/v1/subscription-accounts", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionAccounts(w, r, svc)
	}))
	srv.HandlePrefix("/v1/subscription-accounts/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionAccountByID(w, r, svc)
	}))

	// /api/subscription-accounts aliases reuse the canonical /v1/subscription-accounts
	// handlers so the web admin client (baseURL /api) can manage subscription accounts
	// through the same code path. Unlike the one-api compat shims (/api/channel), these
	// mirrors are thin: the request/response shapes already match the web client.
	srv.HandleFunc("/api/subscription-accounts", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionAccounts(w, r, svc)
	}))
	srv.HandlePrefix("/api/subscription-accounts/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleSubscriptionAccountByID(w, r, svc)
	}))

	srv.HandleFunc("/v1/system/options", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGetSystemOptions(w, r, svc)
	}))
	srv.HandlePrefix("/api/option", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOptions(w, r, svc)
	}))

	srv.HandleFunc("/v1/logs", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleListLogs(w, r, svc)
	}))
	srv.HandleFunc("/api/log/stat", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleLogStats(w, r, svc, false)
	}))
	srv.HandleFunc("/api/log/self/stat", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleLogStats(w, r, svc, true)
	}))
	srv.HandleFunc("/api/log/export", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportLogs(w, r, svc)
	}))
	srv.HandlePrefix("/api/log/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPILogByID(w, r, svc)
	}))
	srv.HandlePrefix("/api/log", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPILogs(w, r, svc)
	}))

	srv.HandleFunc("/v1/account", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGetAccount(w, r, svc)
	}))

	srv.HandleFunc("/v1/redeem-codes", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodes(w, r, svc)
	}))
	srv.HandleFunc("/v1/redeem-codes/batch", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodesBatch(w, r, svc)
	}))
	srv.HandlePrefix("/v1/redeem-codes/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodeByCode(w, r, svc)
	}))
	srv.HandleFunc("/api/redemption/export", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportRedemptions(w, r, svc)
	}))
	srv.HandlePrefix("/api/redemption/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIRedemptionByCode(w, r, svc)
	}))
	srv.HandlePrefix("/api/redemption", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIRedemptions(w, r, svc)
	}))
	srv.HandleFunc("/v1/topup", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTopUp(w, r, svc)
	}))
	srv.HandleFunc("/v1/users/reset-quota", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleResetUserQuota(w, r, svc)
	}))
	srv.HandleFunc("/api/topup", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTopUp(w, r, svc)
	}))
	srv.HandleFunc("/api/payment/orders", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handlePaymentOrders(w, r, svc)
	}))
	srv.HandlePrefix("/api/payment/orders/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handlePaymentOrderByTradeNo(w, r, svc)
	}))
	// Admin-initiated refund (phase 2.3 management path). POST only; the
	// trade_no identifies the order and the body carries reason/policy. The
	// operator id is taken from the admin token, never trusted from the body.
	srv.HandleFunc("/api/v1/admin/payments/refund", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRefundPaymentOrder(w, r, svc)
	}))
	srv.HandleFunc("/api/channel/test", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTestChannels(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/test/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTestChannel(w, r, svc)
	}))
	srv.HandleFunc("/api/channel/update_balance", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateChannelBalances(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/update_balance/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateChannelBalance(w, r, svc)
	}))
	srv.HandlePrefix("/api/reconciliation/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleReconciliationRunByID(w, r, svc)
	}))
	srv.HandleFunc("/api/reconciliation", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleReconciliationRuns(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/disable/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelStatusAlias(w, r, svc, 2, "/api/channel/disable/")
	}))
	srv.HandlePrefix("/api/channel/enable/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelStatusAlias(w, r, svc, 1, "/api/channel/enable/")
	}))
	srv.HandleFunc("/api/channel/export", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportChannels(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelByID(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannels(w, r, svc)
	}))

	// ── Model management (方案B) ──────────────────────────────────────────
	srv.HandleFunc("/api/admin/models", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleModels(w, r, svc)
	}))
	srv.HandlePrefix("/api/admin/models/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleModels(w, r, svc)
	}))
	srv.HandlePrefix("/api/admin/channels/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleAdminChannelPath(w, r, svc)
	}))
	srv.HandlePrefix("/api/admin/subscription-accounts/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleAdminSubscriptionAccountPath(w, r, svc)
	}))

	// ── Model routing (P2 #3) ─────────────────────────────────────────────
	srv.HandleFunc("/api/admin/model-routings", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleModelRoutings(w, r, svc)
	}))
	srv.HandlePrefix("/api/admin/model-routings/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleModelRoutings(w, r, svc)
	}))

	// Notification endpoints - proxy to notify-worker
	if notifyWorkerProxy != nil {
		srv.HandleFunc("/api/admin/notifications", adminAuth(func(w http.ResponseWriter, r *http.Request) {
			handleNotifyProxy(w, r, notifyWorkerProxy)
		}))
		srv.HandlePrefix("/api/admin/notifications/", adminAuth(func(w http.ResponseWriter, r *http.Request) {
			handleNotifyProxyByID(w, r, notifyWorkerProxy)
		}))
	}

	return srv
}

func optionString(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}

func newServiceReverseProxy(endpoint string) *httputil.ReverseProxy {
	target, err := parseReverseProxyTarget(endpoint)
	if err != nil {
		return nil
	}
	return httputil.NewSingleHostReverseProxy(target) // #nosec G704 -- endpoint is configured by operators and validated by parseReverseProxyTarget.
}

func parseReverseProxyTarget(endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("empty reverse proxy endpoint")
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("reverse proxy endpoint must use http or https")
	}
	if target.Host == "" {
		return nil, errors.New("reverse proxy endpoint must include a host")
	}
	return target, nil
}

func requireCSVExport(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return false
	}
	if r.URL.Query().Get("format") != "csv" {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "unsupported export format", nil))
		return false
	}
	return true
}

func writeCSV(w http.ResponseWriter, filename string, header []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write(header)
	for _, row := range rows {
		_ = writer.Write(row)
	}
	writer.Flush()
}

func handleAdminAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	role, _ := r.Context().Value(adminRoleContextKey{}).(int32)
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"admin": true, "role": role}))
}

func handleAdminSummary(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	users, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{Page: 1, PageSize: 5})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, "failed to load users", nil))
		return
	}
	activeUsers, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{Page: 1, PageSize: 1, Status: 1})
	if err != nil {
		activeUsers = &adminv1.AdminListUsersResponse{}
	}
	channels, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{Page: 1, PageSize: 5})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, "failed to load channels", nil))
		return
	}
	activeChannels, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{Page: 1, PageSize: 1, Status: 1})
	if err != nil {
		activeChannels = &adminv1.AdminListChannelsResponse{}
	}
	recentLogs, recentLogsTotal, err := svc.ListLedgerEntries(r.Context(), &adminv1.ListLogsRequest{Page: 1, PageSize: 8})
	if err != nil {
		recentLogs = []map[string]interface{}{}
		recentLogsTotal = 0
	}
	paymentOrders, err := svc.ListPaymentOrders(r.Context(), &billingv1.ListPaymentOrdersRequest{Page: 1, PageSize: 8, Status: "paid"})
	if err != nil {
		paymentOrders = &billingv1.ListPaymentOrdersResponse{}
	}
	stats, err := svc.GetLogStats(r.Context(), &adminv1.ListLogsRequest{Page: 1, PageSize: 1000})
	if err != nil {
		stats = map[string]interface{}{}
	}
	topModels, err := svc.AggregateUsageTopN(r.Context(), "model", 5)
	if err != nil {
		topModels = []service.UsageAggregateView{}
	}
	topChannels, err := svc.AggregateUsageTopN(r.Context(), "channel", 5)
	if err != nil {
		topChannels = []service.UsageAggregateView{}
	}
	topUsers, err := svc.AggregateUsageTopN(r.Context(), "user", 5)
	if err != nil {
		topUsers = []service.UsageAggregateView{}
	}
	topTokens, err := svc.AggregateUsageTopN(r.Context(), "token", 5)
	if err != nil {
		topTokens = []service.UsageAggregateView{}
	}
	topSubscriptionAccounts, err := svc.AggregateUsageTopN(r.Context(), "subscription_account", 5)
	if err != nil {
		topSubscriptionAccounts = []service.UsageAggregateView{}
	}
	topSubscriptionAccountQuotaEvents, err := svc.AggregateSubscriptionAccountQuotaEventsTopN(r.Context(), 5)
	if err != nil {
		topSubscriptionAccountQuotaEvents = []service.SubscriptionAccountQuotaEventAggregateView{}
	}
	reconciliation, err := svc.ListReconciliationRuns(r.Context(), 1, 1)
	if err != nil {
		reconciliation = &service.ListReconciliationRunsResult{}
	}
	options, err := svc.ListOneAPIOptions(r.Context())
	if err != nil {
		options = nil
	}

	subscriptionAccounts, err := svc.ListSubscriptionAccounts(r.Context(), &adminv1.AdminListSubscriptionAccountsRequest{Page: 1, PageSize: 5})
	if err != nil {
		subscriptionAccounts = &adminv1.AdminListSubscriptionAccountsResponse{Accounts: []*commonv1.SubscriptionAccountSummary{}, Total: 0}
	}
	activeSubscriptionAccounts, err := svc.ListSubscriptionAccounts(r.Context(), &adminv1.AdminListSubscriptionAccountsRequest{Page: 1, PageSize: 1, Status: 1})
	if err != nil {
		activeSubscriptionAccounts = &adminv1.AdminListSubscriptionAccountsResponse{Accounts: []*commonv1.SubscriptionAccountSummary{}, Total: 0}
	}

	requestCount := int64(0)
	quotaUsed := int64(0)
	upstreamCost := int64(0)
	grossProfit := int64(0)
	models := map[string]struct{}{}
	requestCount = interfaceToInt64(stats["total"])
	quotaUsed = interfaceToInt64(stats["total_amount"])
	upstreamCost = interfaceToInt64(stats["upstream_cost"])
	grossProfit = interfaceToInt64(stats["gross_profit"])
	totalBalance := float64(0)
	staleBalanceCount := 0
	now := time.Now().Unix()
	for _, channel := range channels.GetChannels() {
		totalBalance += channel.GetBalance()
		if channel.GetBalanceUpdatedTime() == 0 || now-channel.GetBalanceUpdatedTime() > 24*60*60 {
			staleBalanceCount++
		}
		for _, model := range strings.Split(channel.GetModels(), ",") {
			model = strings.TrimSpace(model)
			if model != "" {
				models[model] = struct{}{}
			}
		}
	}
	configuredModels := len(models)
	if configuredModels == 0 {
		configuredModels = len(oneAPIChannelModelCatalog())
	}

	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
		"totals": map[string]interface{}{
			"users":                        users.GetTotal(),
			"active_users":                 activeUsers.GetTotal(),
			"channels":                     channels.GetTotal(),
			"active_channels":              activeChannels.GetTotal(),
			"configured_models":            configuredModels,
			"request_count":                requestCount,
			"quota_used":                   quotaUsed,
			"upstream_cost":                upstreamCost,
			"gross_profit":                 grossProfit,
			"channel_balance":              totalBalance,
			"stale_balance_channels":       staleBalanceCount,
			"log_count":                    recentLogsTotal,
			"subscription_accounts":        subscriptionAccounts.GetTotal(),
			"active_subscription_accounts": activeSubscriptionAccounts.GetTotal(),
		},
		"recent_users":                          users.GetUsers(),
		"channels":                              channels.GetChannels(),
		"subscription_accounts":                 subscriptionAccounts.GetAccounts(),
		"recent_logs":                           recentLogs,
		"payment_orders":                        paymentOrders.GetOrders(),
		"usage_stats":                           stats,
		"cost_analysis":                         costAnalysisSummary(quotaUsed, upstreamCost, grossProfit),
		"top_models":                            usageAggregateViewsToMaps(topModels),
		"top_channels":                          enrichChannelUsage(topChannels, channels.GetChannels()),
		"top_users":                             usageAggregateViewsToMaps(topUsers),
		"top_tokens":                            usageAggregateViewsToMaps(topTokens),
		"top_subscription_accounts":             enrichSubscriptionAccountUsage(topSubscriptionAccounts, topSubscriptionAccountQuotaEvents, subscriptionAccounts.GetAccounts()),
		"top_subscription_account_quota_events": enrichSubscriptionAccountQuotaEventUsage(topSubscriptionAccountQuotaEvents, topSubscriptionAccounts, subscriptionAccounts.GetAccounts()),
		"alerts":                                adminSummaryAlerts(channels.GetChannels(), topChannels, reconciliation, subscriptionAccounts.GetAccounts()),
		"latest_reconciliation":                 latestReconciliationRun(reconciliation),
		"model_catalog":                         oneAPIChannelModelCatalog(),
		"pricing_options":                       optionsByKey(options, "ModelRatio", "CompletionRatio", "ModelPrice", "GroupRatio", "AmountPerUnit"),
		"payment_summary":                       paymentSummaryFromOrders(paymentOrders),
	}))
}

func costAnalysisSummary(quotaUsed, upstreamCost, grossProfit int64) map[string]interface{} {
	margin := float64(0)
	if quotaUsed > 0 {
		margin = float64(grossProfit) / float64(quotaUsed)
	}
	return map[string]interface{}{
		"revenue_quota": quotaUsed,
		"upstream_cost": upstreamCost,
		"gross_profit":  grossProfit,
		"gross_margin":  margin,
		"profitable":    grossProfit >= 0,
	}
}

func usageAggregateViewsToMaps(items []service.UsageAggregateView) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, usageAggregateViewToMap(item))
	}
	return out
}

func usageAggregateViewToMap(item service.UsageAggregateView) map[string]interface{} {
	return map[string]interface{}{
		"key":                     item.Key,
		"user_id":                 item.UserID,
		"channel_id":              item.ChannelID,
		"subscription_account_id": item.SubscriptionAccountID,
		"model":                   item.Model,
		"token_name":              item.TokenName,
		"type":                    item.Type,
		"quota":                   item.Quota,
		"upstream_cost":           item.UpstreamCost,
		"gross_profit":            item.GrossProfit,
		"prompt_tokens":           item.PromptTokens,
		"completion_tokens":       item.CompletionTokens,
		"cache_read_tokens":       item.CacheReadTokens,
		"count":                   item.Count,
		"elapsed_time":            item.ElapsedTime,
	}
}

func enrichChannelUsage(items []service.UsageAggregateView, channels []*commonv1.ChannelSummary) []map[string]interface{} {
	channelByID := map[int64]*commonv1.ChannelSummary{}
	for _, channel := range channels {
		channelByID[channel.GetId()] = channel
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		row := usageAggregateViewToMap(item)
		if channel := channelByID[item.ChannelID]; channel != nil {
			row["name"] = channel.GetName()
			row["status"] = channel.GetStatus()
			row["balance"] = channel.GetBalance()
			row["balance_updated_time"] = channel.GetBalanceUpdatedTime()
			row["used_quota"] = channel.GetUsedQuota()
		}
		out = append(out, row)
	}
	return out
}

// enrichSubscriptionAccountUsage attaches the subscription account name,
// platform, status and lifecycle metadata to each usage-aggregate row keyed by
// subscription_account_id, so the cost-analysis dashboard can render
// human-readable rows instead of bare numeric ids.
//
// Rows whose SubscriptionAccountID == 0 represent requests that were NOT served
// by a subscription account (plain API-key channels). They must be excluded
// here: otherwise the entire non-subscription usage collapses into a single
// "subscription_account_id = 0" bucket whose cost equals the total channel
// cost, which would make the "订阅账号成本" card wrongly display channel cost.
func enrichSubscriptionAccountUsage(items []service.UsageAggregateView, eventItems []service.SubscriptionAccountQuotaEventAggregateView, accounts []*commonv1.SubscriptionAccountSummary) []map[string]interface{} {
	accountByID := map[int64]*commonv1.SubscriptionAccountSummary{}
	for _, account := range accounts {
		accountByID[account.GetId()] = account
	}
	eventByAccountID := map[int64]service.SubscriptionAccountQuotaEventAggregateView{}
	for _, event := range eventItems {
		eventByAccountID[event.SubscriptionAccountID] = event
	}
	// The summary only fetches the overview account page. Keep enrichment
	// best-effort: unresolved ids still surface as rows with a fallback label.
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		// Skip the synthetic "no subscription account" bucket (id == 0) so the
		// subscription-account cost card only reflects real subscription-account
		// usage, not channel usage that happens to share subscription_account_id=0.
		if item.SubscriptionAccountID == 0 {
			continue
		}
		row := usageAggregateViewToMap(item)
		if account := accountByID[item.SubscriptionAccountID]; account != nil {
			row["name"] = account.GetName()
			row["platform"] = account.GetPlatform()
			row["status"] = account.GetStatus()
			row["account_id"] = account.GetAccountId()
			row["expires_at"] = account.GetExpiresAt()
		} else {
			row["name"] = fmt.Sprintf("订阅账号 #%d", item.SubscriptionAccountID)
		}
		if event, ok := eventByAccountID[item.SubscriptionAccountID]; ok {
			row["account_event_cost_usd"] = event.CostUSD
			row["account_event_charged_usd"] = event.ChargedUSD
			row["account_event_average_rate_multiplier"] = event.AverageRateMultiplier
			row["account_event_count"] = event.Count
			row["account_event_last_occurred_at"] = event.LastOccurredAt
		}
		out = append(out, row)
	}
	return out
}

func enrichSubscriptionAccountQuotaEventUsage(eventItems []service.SubscriptionAccountQuotaEventAggregateView, ledgerItems []service.UsageAggregateView, accounts []*commonv1.SubscriptionAccountSummary) []map[string]interface{} {
	accountByID := map[int64]*commonv1.SubscriptionAccountSummary{}
	for _, account := range accounts {
		accountByID[account.GetId()] = account
	}
	ledgerByAccountID := map[int64]service.UsageAggregateView{}
	for _, item := range ledgerItems {
		if item.SubscriptionAccountID > 0 {
			ledgerByAccountID[item.SubscriptionAccountID] = item
		}
	}
	out := make([]map[string]interface{}, 0, len(eventItems))
	for _, item := range eventItems {
		if item.SubscriptionAccountID == 0 {
			continue
		}
		row := map[string]interface{}{
			"subscription_account_id": item.SubscriptionAccountID,
			"cost_usd":                item.CostUSD,
			"charged_usd":             item.ChargedUSD,
			"average_rate_multiplier": item.AverageRateMultiplier,
			"count":                   item.Count,
			"last_occurred_at":        item.LastOccurredAt,
			"ledger_quota":            int64(0),
			"ledger_upstream_cost":    int64(0),
			"ledger_gross_profit":     int64(0),
			"ledger_count":            int64(0),
		}
		if account := accountByID[item.SubscriptionAccountID]; account != nil {
			row["name"] = account.GetName()
			row["platform"] = account.GetPlatform()
			row["status"] = account.GetStatus()
			row["account_id"] = account.GetAccountId()
			row["expires_at"] = account.GetExpiresAt()
		} else {
			row["name"] = fmt.Sprintf("订阅账号 #%d", item.SubscriptionAccountID)
		}
		if ledger, ok := ledgerByAccountID[item.SubscriptionAccountID]; ok {
			row["ledger_quota"] = ledger.Quota
			row["ledger_upstream_cost"] = ledger.UpstreamCost
			row["ledger_gross_profit"] = ledger.GrossProfit
			row["ledger_count"] = ledger.Count
		}
		out = append(out, row)
	}
	return out
}

func adminSummaryAlerts(channels []*commonv1.ChannelSummary, topChannels []service.UsageAggregateView, reconciliation *service.ListReconciliationRunsResult, subscriptionAccounts []*commonv1.SubscriptionAccountSummary) []map[string]interface{} {
	alerts := []map[string]interface{}{}
	now := time.Now().Unix()
	for _, channel := range channels {
		if channel.GetStatus() != 1 {
			continue
		}
		if channel.GetBalanceUpdatedTime() == 0 || now-channel.GetBalanceUpdatedTime() > 24*60*60 {
			alerts = append(alerts, map[string]interface{}{
				"type":       "stale_balance",
				"severity":   "warning",
				"category":   "channel",
				"channel_id": channel.GetId(),
				"message":    "渠道余额超过 24 小时未刷新",
			})
		}
		if channel.GetBalanceUpdatedTime() > 0 && channel.GetBalance() <= 1 {
			alerts = append(alerts, map[string]interface{}{
				"type":       "low_balance",
				"severity":   "critical",
				"category":   "channel",
				"channel_id": channel.GetId(),
				"message":    "渠道余额低于 1 USD",
			})
		}
	}
	for _, item := range topChannels {
		if item.GrossProfit < 0 {
			alerts = append(alerts, map[string]interface{}{
				"type":         "negative_profit",
				"severity":     "critical",
				"category":     "channel",
				"channel_id":   item.ChannelID,
				"gross_profit": item.GrossProfit,
				"message":      "渠道用量毛利为负",
			})
		}
	}
	if latest := latestReconciliationRun(reconciliation); latest != nil {
		if count := interfaceToInt64(latest["discrepancy_count"]); count > 0 {
			alerts = append(alerts, map[string]interface{}{
				"type":              "reconciliation_discrepancy",
				"severity":          "critical",
				"category":          "billing",
				"run_id":            latest["run_id"],
				"discrepancy_count": count,
				"message":           "最近一次对账存在差异",
			})
		}
	}
	// Subscription-account governance alerts. These are distinct from the
	// channel-health and billing alerts above so the admin UI can filter by
	// category. Each alert carries the recovery metadata so the operator sees
	// both the symptom and the expected recovery time.
	for _, account := range subscriptionAccounts {
		if account == nil || account.GetStatus() != 1 {
			continue
		}
		if reason := account.GetUnschedulableReason(); reason != "" {
			severity := "warning"
			if account.GetRecoveryPolicy() == "manual" {
				severity = "critical"
			}
			alerts = append(alerts, map[string]interface{}{
				"type":                 "subscription_account_unschedulable",
				"severity":             severity,
				"category":             "subscription_account",
				"account_id":           account.GetId(),
				"account_name":         account.GetName(),
				"recovery_policy":      account.GetRecoveryPolicy(),
				"expected_recovery_at": account.GetExpectedRecoveryAt(),
				"unschedulable_since":  account.GetUnschedulableSince(),
				"message":              "订阅账号不可调度: " + reason,
			})
		}
		if account.GetQuotaSnapshotPaused() {
			alerts = append(alerts, map[string]interface{}{
				"type":         "subscription_account_writeback_down",
				"severity":     "critical",
				"category":     "subscription_account",
				"account_id":   account.GetId(),
				"account_name": account.GetName(),
				"message":      "订阅账号额度快照回写已暂停",
			})
		}
	}
	return alerts
}

func latestReconciliationRun(reconciliation *service.ListReconciliationRunsResult) map[string]interface{} {
	if reconciliation == nil || len(reconciliation.Runs) == 0 || reconciliation.Runs[0] == nil {
		return nil
	}
	run := reconciliation.Runs[0]
	return map[string]interface{}{
		"run_id":             run.RunID,
		"run_at":             run.RunAt,
		"discrepancy_count":  run.DiscrepancyCount,
		"total_accounts":     run.TotalAccounts,
		"total_channels":     run.TotalChannels,
		"total_reservations": run.TotalReservations,
	}
}

func interfaceToInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func optionsByKey(options []service.OneAPIOption, keys ...string) map[string]string {
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[key] = struct{}{}
	}
	result := make(map[string]string, len(keys))
	for _, option := range options {
		if _, ok := wanted[option.Key]; ok {
			result[option.Key] = option.Value
		}
	}
	return result
}

type readonlyPricingRow struct {
	Model          string   `json:"model"`
	InputPrice     *float64 `json:"input_price,omitempty"`
	OutputPrice    *float64 `json:"output_price,omitempty"`
	CacheReadPrice *float64 `json:"cache_read_price,omitempty"`
}

type readonlyModelPrice struct {
	InputPrice     *float64 `json:"input_price"`
	OutputPrice    *float64 `json:"output_price"`
	CacheReadPrice *float64 `json:"cache_read_price"`
}

const (
	readonlyPricingMTok      = 1000000
	readonlyPricingUnitScale = 10000
)

func handleReadonlyPricing(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "",
			"data": map[string]interface{}{
				"prices":          []readonlyPricingRow{},
				"amount_per_unit": float64(readonlyPricingUnitScale),
				"unit":            "1M tokens",
			},
		})
		return
	}
	options, err := svc.ListOneAPIOptions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	optionMap := optionsByKey(options, "ModelPrice", "ModelRatio", "CompletionRatio", "AmountPerUnit")
	amountPerUnit := parseReadonlyFloatOption(optionMap["AmountPerUnit"])
	if amountPerUnit <= 0 {
		amountPerUnit = readonlyPricingUnitScale
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data": map[string]interface{}{
			"prices":          readonlyPricingRows(optionMap, amountPerUnit),
			"amount_per_unit": amountPerUnit,
			"unit":            "1M tokens",
		},
	})
}

func readonlyPricingRows(options map[string]string, quotaPerUnit float64) []readonlyPricingRow {
	modelPrices := parseReadonlyModelPrices(options["ModelPrice"])
	modelRatios := parseReadonlyRatioMap(options["ModelRatio"])
	completionRatios := parseReadonlyRatioMap(options["CompletionRatio"])
	models := map[string]struct{}{}
	for model := range modelPrices {
		models[model] = struct{}{}
	}
	for model := range modelRatios {
		models[model] = struct{}{}
	}
	for model := range completionRatios {
		models[model] = struct{}{}
	}
	names := make([]string, 0, len(models))
	for model := range models {
		names = append(names, model)
	}
	sort.Strings(names)

	rows := make([]readonlyPricingRow, 0, len(names))
	for _, model := range names {
		price := modelPrices[model]
		row := readonlyPricingRow{Model: model}
		if price.InputPrice != nil {
			row.InputPrice = floatPtr(*price.InputPrice * readonlyPricingMTok)
		} else if ratio, ok := modelRatios[model]; ok {
			row.InputPrice = floatPtr((ratio / quotaPerUnit) * readonlyPricingMTok)
		}
		if price.OutputPrice != nil {
			row.OutputPrice = floatPtr(*price.OutputPrice * readonlyPricingMTok)
		} else if ratio, ok := modelRatios[model]; ok {
			row.OutputPrice = floatPtr(((ratio * ratioOrDefault(completionRatios[model], 1)) / quotaPerUnit) * readonlyPricingMTok)
		}
		if price.CacheReadPrice != nil {
			row.CacheReadPrice = floatPtr(*price.CacheReadPrice * readonlyPricingMTok)
		}
		rows = append(rows, row)
	}
	return rows
}

func parseReadonlyModelPrices(raw string) map[string]readonlyModelPrice {
	if strings.TrimSpace(raw) == "" {
		return map[string]readonlyModelPrice{}
	}
	values := map[string]readonlyModelPrice{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return map[string]readonlyModelPrice{}
	}
	out := make(map[string]readonlyModelPrice, len(values))
	for model, price := range values {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		out[model] = price
	}
	return out
}

func parseReadonlyRatioMap(raw string) map[string]float64 {
	if strings.TrimSpace(raw) == "" {
		return map[string]float64{}
	}
	values := map[string]float64{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(values))
	for model, ratio := range values {
		model = strings.TrimSpace(model)
		if model != "" && ratio >= 0 {
			out[model] = ratio
		}
	}
	return out
}

func parseReadonlyFloatOption(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	var value float64
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		return value
	}
	value, _ = strconv.ParseFloat(raw, 64)
	return value
}

func ratioOrDefault(value, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func floatPtr(value float64) *float64 {
	return &value
}

func paymentSummaryFromOrders(resp *billingv1.ListPaymentOrdersResponse) map[string]interface{} {
	amount := int64(0)
	total := int64(0)
	if resp != nil {
		allReturnedPaid := true
		for _, order := range resp.GetOrders() {
			if order.GetStatus() != "paid" {
				allReturnedPaid = false
				continue
			}
			total++
			amount += order.GetMoneyCents()
		}
		if allReturnedPaid {
			total = resp.GetTotal()
		}
	}
	return map[string]interface{}{
		"recent_order_count":        total,
		"recent_amount_cents":       amount,
		"recent_amount_money_cents": amount,
		"recent_amount":             amount,
	}
}

func userInfoToMap(u *commonv1.UserInfo, balance, usedAmount int64) map[string]interface{} {
	return map[string]interface{}{
		"id":          u.GetId(),
		"username":    u.GetUsername(),
		"displayName": u.GetDisplayName(),
		"email":       u.GetEmail(),
		"group":       u.GetGroup(),
		"status":      u.GetStatus(),
		"role":        u.GetRole(),
		"balance":     strconv.FormatInt(balance, 10),
		"usedAmount":  strconv.FormatInt(usedAmount, 10),
		"createdAt":   strconv.FormatInt(u.GetCreatedAt(), 10),
	}
}

func providerNameFromType(channelType int32) string {
	switch channelType {
	case 1:
		return "openai"
	case 2:
		return "openai" // openai-sb
	case 3:
		return "openai" // openai-tts
	case 4:
		return "anthropic"
	case 5:
		return "google" // gemini
	case 6:
		return "azure"
	case 7:
		return "deepseek"
	case 8:
		return "tongyi" // qwen
	case 9:
		return "zhipu" // glm
	case 10:
		return "moonshot"
	case 11:
		return "mistral"
	case 12:
		return "cohere"
	case 13:
		return "perplexity"
	case 14:
		return "voyageai"
	default:
		return "organization"
	}
}

func enrichUsersWithBilling(ctx context.Context, svc *service.AdminService, users []*commonv1.UserInfo) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(users))

	// Collect user IDs for batch query
	userIDs := make([]string, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, strconv.FormatInt(u.GetId(), 10))
	}

	// Batch fetch account snapshots
	var accounts map[string]*commonv1.AccountInfo
	if svc != nil && len(userIDs) > 0 {
		var err error
		accounts, err = svc.BatchGetAccountSnapshots(ctx, userIDs)
		if err != nil {
			accounts = map[string]*commonv1.AccountInfo{}
		}
	}

	// Build result with enriched data
	for _, u := range users {
		var balance, usedAmount int64
		if accounts != nil {
			userID := strconv.FormatInt(u.GetId(), 10)
			if acc, ok := accounts[userID]; ok {
				balance = acc.GetBalance()
				usedAmount = acc.GetUsedAmount()
			}
		}
		result = append(result, userInfoToMap(u, balance, usedAmount))
	}
	return result
}

func newAdminWebAssets(webRoot string) adminWebAssets {
	webRoot = strings.TrimSpace(webRoot)
	if webRoot == "" {
		webRoot = strings.TrimSpace(os.Getenv("ADMIN_WEB_ROOT"))
	}
	if webRoot == "" {
		return adminWebAssets{}
	}
	cleanRoot, err := usableWebRoot(webRoot)
	if err != nil {
		return adminWebAssets{}
	}
	return adminWebAssets{root: os.DirFS(cleanRoot)}
}

func usableWebRoot(webRoot string) (string, error) {
	cleanRoot, err := filepath.Abs(filepath.Clean(webRoot))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(cleanRoot)
	if err != nil || !info.IsDir() {
		return "", errors.New("web root is not a directory")
	}
	indexInfo, err := os.Stat(filepath.Join(cleanRoot, "index.html"))
	if err != nil || indexInfo.IsDir() {
		return "", errors.New("web root index.html is not a file")
	}
	return cleanRoot, nil
}

func (a adminWebAssets) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if a.root == nil {
		http.Error(w, "frontend not available", http.StatusInternalServerError)
		return
	}

	// SPA fallback: any path without a file extension serves index.html so
	// client-side routes (/login, /dashboard, /tokens) load the React shell.
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || path == "index.html" || !strings.Contains(path, ".") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		http.FileServer(http.FS(a.root)).ServeHTTP(w, r2)
		return
	}

	http.FileServer(http.FS(a.root)).ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func getQueryInt32(r *http.Request, key string, defaultVal int32) int32 {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return defaultVal
	}
	return int32(v)
}

func getQueryInt64(r *http.Request, key string, defaultVal int64) int64 {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func oneAPIPage(r *http.Request) int32 {
	if raw := r.URL.Query().Get("p"); raw != "" {
		p, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || p < 0 {
			return 1
		}
		page, err := safecast.Int64ToInt32(p + 1)
		if err != nil {
			return 1
		}
		return page
	}
	return getQueryInt32(r, "page", 1)
}

func oneAPIPageSize(r *http.Request) int32 {
	if size := getQueryInt32(r, "page_size", 0); size > 0 {
		return size
	}
	if size := getQueryInt32(r, "size", 0); size > 0 {
		return size
	}
	return 20
}

func apiResponse(success bool, message string, data interface{}) map[string]interface{} {
	resp := map[string]interface{}{
		"success": success,
		"message": message,
	}
	if data != nil {
		resp["data"] = data
	}
	return resp
}

// logEntryToJSON converts a proto LogEntry to a camelCase JSON map for frontend compatibility.
func logEntryToJSON(entry *adminv1.LogEntry) map[string]interface{} {
	return map[string]interface{}{
		"id":           entry.GetId(),
		"userId":       entry.GetUserId(),
		"type":         entry.GetType(),
		"amount":       entry.GetAmount(),
		"balanceAfter": entry.GetBalanceAfter(),
		"referenceId":  entry.GetReferenceId(),
		"remark":       entry.GetRemark(),
		"createdAt":    entry.GetCreatedAt(),
	}
}

// logEntriesToJSON converts a slice of proto LogEntries to camelCase JSON maps.
func logEntriesToJSON(entries []*adminv1.LogEntry) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		result = append(result, logEntryToJSON(entry))
	}
	return result
}

func writeOneAPIServiceResponse(w http.ResponseWriter, resp interface{}, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	success := true
	message := ""
	switch v := resp.(type) {
	case *adminv1.AdminCreateUserResponse:
		success, message = v.GetSuccess(), v.GetMessage()
	case *adminv1.AdminUpdateUserResponse:
		success, message = v.GetSuccess(), v.GetMessage()
	case *adminv1.AdminDeleteUserResponse:
		success, message = v.GetSuccess(), v.GetMessage()
	case *adminv1.AdminCreateChannelResponse:
		success, message = v.GetSuccess(), v.GetMessage()
	case *adminv1.AdminUpdateChannelResponse:
		success, message = v.GetSuccess(), v.GetMessage()
	case *adminv1.AdminDeleteChannelResponse:
		success, message = v.GetSuccess(), v.GetMessage()
	case *adminv1.CreateRedeemCodeResponse:
		success, message = v.GetSuccess(), v.GetErrorMessage()
	case *adminv1.CreateRedeemCodesBatchResponse:
		success, message = v.GetSuccess(), v.GetErrorMessage()
	}
	writeJSON(w, http.StatusOK, apiResponse(success, message, resp))
}

func handleContentRoute(adminAuth func(http.HandlerFunc) http.HandlerFunc, svc *service.AdminService, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			value, err := svc.GetOneAPIOption(r.Context(), key)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiResponse(false, err.Error(), nil))
				return
			}
			writeJSON(w, http.StatusOK, apiResponse(true, "", value))
			return
		}
		adminAuth(func(w http.ResponseWriter, r *http.Request) {
			handleContentWrite(w, r, svc, key)
		})(w, r)
	}
}

func handleContentWrite(w http.ResponseWriter, r *http.Request, svc *service.AdminService, key string) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Content string `json:"content"`
		Value   string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	value := req.Content
	if value == "" {
		value = req.Value
	}
	resp, err := svc.UpdateOneAPIOption(r.Context(), key, value)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(resp.GetSuccess(), resp.GetMessage(), nil))
}

func handleGroupManagement(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		groups, err := svc.ListGroups(r.Context())
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
			return
		}
		if r.URL.Query().Get("with_ratio") == "true" {
			writeJSON(w, http.StatusOK, apiResponse(true, "", groups))
			return
		}
		names := make([]string, 0, len(groups))
		for _, group := range groups {
			names = append(names, group.Group)
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", names))
	case http.MethodPost, http.MethodPut:
		var req struct {
			Group string  `json:"group"`
			Name  string  `json:"name"`
			Ratio float64 `json:"ratio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Group == "" {
			req.Group = req.Name
		}
		result, err := svc.UpsertGroup(r.Context(), req.Group, req.Ratio)
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", result))
	case http.MethodDelete:
		group := r.URL.Query().Get("group")
		if group == "" {
			group = r.URL.Query().Get("name")
		}
		result, err := svc.DeleteGroup(r.Context(), group)
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", result))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return false
	}
	return true
}

func parsePathID(path, prefix string) (int64, bool) {
	idPart := strings.TrimPrefix(path, prefix)
	idPart = strings.Trim(idPart, "/")
	if idPart == "" || strings.Contains(idPart, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	return id, err == nil && id > 0
}

func parsePathValue(path, prefix string) (string, bool) {
	value := strings.TrimPrefix(path, prefix)
	value = strings.Trim(value, "/")
	return value, value != "" && !strings.Contains(value, "/")
}

func handleUsers(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		handleListUsers(w, r, svc)
	case http.MethodPost:
		var req adminv1.AdminCreateUserRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateUser(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.AdminUpdateUserRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.UpdateUser(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIUsers(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/user/manage" {
		handleOneAPIUserManage(w, r, svc)
		return
	}
	if trimmed != "api/user" && trimmed != "api/user/search" {
		handleOneAPIUserByID(w, r, svc)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if trimmed == "api/user/search" {
			handleOneAPISearchUsers(w, r, svc)
			return
		}
		handleOneAPIListUsers(w, r, svc)
	case http.MethodPost:
		var req adminv1.AdminCreateUserRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateUser(r.Context(), &req)
		writeOneAPIServiceResponse(w, resp, err)
	case http.MethodPut:
		var raw struct {
			ID int64 `json:"id"`
			*adminv1.AdminUpdateUserRequest
		}
		if !decodeBody(w, r, &raw) {
			return
		}
		if raw.AdminUpdateUserRequest == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req := raw.AdminUpdateUserRequest
		if req.UserId == 0 {
			req.UserId = raw.ID
		}
		resp, err := svc.UpdateUser(r.Context(), req)
		writeOneAPIServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIUserManage(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Action   string `json:"action"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Action = strings.TrimSpace(req.Action)
	if req.Username == "" || req.Action == "" {
		writeJSON(w, http.StatusOK, apiResponse(false, "username and action are required", nil))
		return
	}
	users, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{
		Page:     1,
		PageSize: 100,
		Keyword:  req.Username,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}
	var user *commonv1.UserInfo
	for _, item := range users.GetUsers() {
		if item.GetUsername() == req.Username {
			user = item
			break
		}
	}
	if user == nil {
		writeJSON(w, http.StatusOK, apiResponse(false, "user not found", nil))
		return
	}
	userID := user.GetId()

	switch req.Action {
	case "disable", "enable":
		status := int32(2)
		if req.Action == "enable" {
			status = 1
		}
		resp, err := svc.UpdateUser(r.Context(), &adminv1.AdminUpdateUserRequest{UserId: userID, Status: status})
		if err != nil || !resp.GetSuccess() {
			message := ""
			if resp != nil {
				message = resp.GetMessage()
			}
			if err != nil {
				message = err.Error()
			}
			writeJSON(w, http.StatusOK, apiResponse(false, message, nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"status": status, "role": user.GetRole()}))
	case "delete":
		resp, err := svc.DeleteUser(r.Context(), &adminv1.AdminDeleteUserRequest{UserId: userID})
		if err != nil || !resp.GetSuccess() {
			message := ""
			if resp != nil {
				message = resp.GetMessage()
			}
			if err != nil {
				message = err.Error()
			}
			writeJSON(w, http.StatusOK, apiResponse(false, message, nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"status": user.GetStatus(), "role": user.GetRole()}))
	case "promote", "demote":
		// 0=guest, 1=common, 10=admin, 100=root. Promote raises a common
		// user to admin; demote returns them to common. Root cannot be
		// changed via this endpoint — that constraint is enforced by
		// identity-service so we just surface its error.
		//
		// X-Operator-User-Id identifies the calling admin so identity-service
		// can enforce operator-vs-target rank rules. When the header is
		// missing the call is treated as a system invocation that skips
		// those checks — fine for now because admin-api is gated by the
		// shared ADMIN_TOKEN; flip to required once admin auth moves to
		// per-user sessions.
		newRole := int32(10) // admin
		if req.Action == "demote" {
			newRole = 1 // common user
		}
		operatorID, _ := strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-Operator-User-Id")), 10, 64)
		resp, err := svc.SetUserRole(r.Context(), &adminv1.AdminSetUserRoleRequest{
			UserId:         userID,
			Role:           newRole,
			OperatorUserId: operatorID,
		})
		if err != nil || !resp.GetSuccess() {
			message := ""
			if resp != nil {
				message = resp.GetMessage()
			}
			if err != nil {
				message = err.Error()
			}
			writeJSON(w, http.StatusOK, apiResponse(false, message, nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"status": user.GetStatus(), "role": resp.GetRole()}))
	default:
		writeJSON(w, http.StatusOK, apiResponse(false, "unsupported action", nil))
	}
}

func handleOneAPIUserByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/user" || trimmed == "api/user/search" || trimmed == "api/user/manage" {
		handleOneAPIUsers(w, r, svc)
		return
	}
	userID, ok := parsePathID(r.URL.Path, "/api/user/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid user id", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		user, err := svc.GetUser(r.Context(), userID)
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", user))
	case http.MethodDelete:
		resp, err := svc.DeleteUser(r.Context(), &adminv1.AdminDeleteUserRequest{UserId: userID})
		writeOneAPIServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIUserStatusAlias(w http.ResponseWriter, r *http.Request, svc *service.AdminService, status int32, prefix string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, ok := parsePathID(r.URL.Path, prefix)
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid user id", nil))
		return
	}
	resp, err := svc.UpdateUser(r.Context(), &adminv1.AdminUpdateUserRequest{
		UserId: userID,
		Status: status,
	})
	writeOneAPIServiceResponse(w, resp, err)
}

func handleOneAPIListUsers(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	resp, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{
		Page:     oneAPIPage(r),
		PageSize: oneAPIPageSize(r),
		Group:    r.URL.Query().Get("group"),
		Status:   getQueryInt32(r, "status", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", enrichUsersWithBilling(r.Context(), svc, resp.GetUsers())))
}

func handleOneAPIExportUsers(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if !requireCSVExport(w, r) {
		return
	}
	resp, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{
		Page:     oneAPIPage(r),
		PageSize: oneAPIPageSize(r),
		Keyword:  r.URL.Query().Get("keyword"),
		Group:    r.URL.Query().Get("group"),
		Status:   getQueryInt32(r, "status", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	rows := make([][]string, 0, len(resp.GetUsers()))
	for _, user := range resp.GetUsers() {
		rows = append(rows, []string{
			strconv.FormatInt(user.GetId(), 10),
			user.GetUsername(),
			user.GetDisplayName(),
			user.GetEmail(),
			user.GetGroup(),
			strconv.FormatInt(user.GetBalance(), 10),
			strconv.FormatInt(user.GetUsedAmount(), 10),
			strconv.FormatInt(int64(user.GetStatus()), 10),
		})
	}
	writeCSV(w, "admin-users.csv", []string{"id", "username", "display_name", "email", "group", "balance", "used_amount", "status"}, rows)
}

func handleOneAPISearchUsers(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	resp, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{
		Page:     1,
		PageSize: oneAPIPageSize(r),
		Keyword:  r.URL.Query().Get("keyword"),
		Group:    r.URL.Query().Get("group"),
		Status:   getQueryInt32(r, "status", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", enrichUsersWithBilling(r.Context(), svc, resp.GetUsers())))
}

func handleUserByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/users/"), "/") == "reset-quota" {
		handleResetUserQuota(w, r, svc)
		return
	}
	userID, ok := parsePathID(r.URL.Path, "/v1/users/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		resp, err := svc.DeleteUser(r.Context(), &adminv1.AdminDeleteUserRequest{UserId: userID})
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.AdminUpdateUserRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.UserId = userID
		resp, err := svc.UpdateUser(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleListUsers(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	page := getQueryInt32(r, "page", 1)
	pageSize := getQueryInt32(r, "page_size", 20)
	keyword := r.URL.Query().Get("keyword")
	group := r.URL.Query().Get("group")
	status := getQueryInt32(r, "status", 0)

	resp, err := svc.ListUsers(r.Context(), &adminv1.AdminListUsersRequest{
		Page:     page,
		PageSize: pageSize,
		Keyword:  keyword,
		Group:    group,
		Status:   status,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		handleListChannels(w, r, svc)
	case http.MethodPost:
		var req adminv1.AdminCreateChannelRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateChannel(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.AdminUpdateChannelRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.UpdateChannel(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleSubscriptionAccounts(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		resp, err := svc.ListSubscriptionAccounts(r.Context(), &adminv1.AdminListSubscriptionAccountsRequest{
			Page:     getQueryInt32(r, "page", 1),
			PageSize: getQueryInt32(r, "page_size", 20),
			Keyword:  r.URL.Query().Get("keyword"),
			Group:    r.URL.Query().Get("group"),
			Status:   getQueryInt32(r, "status", 0),
			Platform: r.URL.Query().Get("platform"),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPost:
		var req adminv1.AdminCreateSubscriptionAccountRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateSubscriptionAccount(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	switch trimmed {
	case "api/channel/disabled":
		handleOneAPIDeleteDisabledChannels(w, r, svc)
		return
	case "api/channel/batch":
		handleOneAPIBatchDeleteChannels(w, r, svc)
		return
	case "api/channel/fix":
		handleOneAPIFixChannels(w, r)
		return
	}
	if trimmed != "api/channel" && trimmed != "api/channel/search" {
		handleOneAPIChannelByID(w, r, svc)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if trimmed == "api/channel/search" {
			handleOneAPISearchChannels(w, r, svc)
			return
		}
		handleOneAPIListChannels(w, r, svc)
	case http.MethodPost:
		var req adminv1.AdminCreateChannelRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateChannel(r.Context(), &req)
		writeOneAPIServiceResponse(w, resp, err)
	case http.MethodPut:
		var raw struct {
			ID int64 `json:"id"`
			*adminv1.AdminUpdateChannelRequest
		}
		if !decodeBody(w, r, &raw) {
			return
		}
		if raw.AdminUpdateChannelRequest == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req := raw.AdminUpdateChannelRequest
		if req.ChannelId == 0 {
			req.ChannelId = raw.ID
		}
		resp, err := svc.UpdateChannel(r.Context(), req)
		writeOneAPIServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// parseSubscriptionAccountID extracts the numeric account id from either the
// canonical /v1/subscription-accounts/{id} path or the /api alias used by the
// web admin client.
func parseSubscriptionAccountID(path string) (int64, bool) {
	for _, prefix := range []string{"/v1/subscription-accounts/", "/api/subscription-accounts/"} {
		if id, ok := parsePathID(path, prefix); ok {
			return id, true
		}
	}
	return 0, false
}

func handleSubscriptionAccountByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	// Support both the canonical /v1/subscription-accounts and the /api alias
	// used by the web admin client (baseURL /api).
	path := r.URL.Path
	rest := strings.TrimPrefix(path, "/v1/subscription-accounts/")
	rest = strings.TrimPrefix(rest, "/api/subscription-accounts/")
	if strings.Trim(rest, "/") == "batch-reset-quota" {
		handleBatchResetSubscriptionAccountQuota(w, r, svc)
		return
	}
	if strings.Trim(rest, "/") == "batch-quota-template" {
		handleBatchApplySubscriptionAccountQuotaTemplate(w, r, svc)
		return
	}
	if strings.HasSuffix(rest, "/reset-quota") {
		idPart := strings.TrimSuffix(rest, "/reset-quota")
		accountID, err := strconv.ParseInt(strings.Trim(idPart, "/"), 10, 64)
		if err != nil || accountID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscription account id"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req adminv1.AdminResetSubscriptionAccountQuotaRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.AccountId = accountID
		resp, err := svc.ResetSubscriptionAccountQuota(r.Context(), &req)
		writeServiceResponse(w, resp, err)
		return
	}
	if strings.HasSuffix(rest, "/status") {
		idPart := strings.TrimSuffix(rest, "/status")
		accountID, err := strconv.ParseInt(strings.Trim(idPart, "/"), 10, 64)
		if err != nil || accountID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscription account id"})
			return
		}
		if r.Method != http.MethodPut {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req adminv1.AdminChangeSubscriptionAccountStatusRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.AccountId = accountID
		resp, err := svc.ChangeSubscriptionAccountStatus(r.Context(), &req)
		writeServiceResponse(w, resp, err)
		return
	}

	accountID, ok := parseSubscriptionAccountID(path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscription account id"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		account, err := svc.GetSubscriptionAccount(r.Context(), accountID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, account)
	case http.MethodDelete:
		resp, err := svc.DeleteSubscriptionAccount(r.Context(), &adminv1.AdminDeleteSubscriptionAccountRequest{AccountId: accountID})
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.AdminUpdateSubscriptionAccountRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.Id = accountID
		resp, err := svc.UpdateSubscriptionAccount(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

type batchSubscriptionAccountQuotaResponse struct {
	Success      bool    `json:"success"`
	Message      string  `json:"message"`
	UpdatedCount int     `json:"updated_count"`
	FailedIDs    []int64 `json:"failed_ids,omitempty"`
}

type batchResetSubscriptionAccountQuotaRequest struct {
	AccountIDs []int64 `json:"account_ids"`
	Scope      string  `json:"scope"`
}

type subscriptionAccountQuotaTemplatePatch struct {
	QuotaLimitUSD         *float64 `json:"quota_limit_usd"`
	Quota5hLimitUSD       *float64 `json:"quota_5h_limit_usd"`
	QuotaDailyLimitUSD    *float64 `json:"quota_daily_limit_usd"`
	QuotaWeeklyLimitUSD   *float64 `json:"quota_weekly_limit_usd"`
	RateMultiplier        *float64 `json:"rate_multiplier"`
	RPMLimit              *int32   `json:"rpm_limit"`
	SessionWindowLimitUSD *float64 `json:"session_window_limit_usd"`
	QuotaResetStrategy    *string  `json:"quota_reset_strategy"`
	QuotaTimezone         *string  `json:"quota_timezone"`
}

type batchApplySubscriptionAccountQuotaTemplateRequest struct {
	AccountIDs []int64                               `json:"account_ids"`
	Template   subscriptionAccountQuotaTemplatePatch `json:"template"`
}

func normalizeBatchSubscriptionAccountIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func handleBatchResetSubscriptionAccountQuota(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req batchResetSubscriptionAccountQuotaRequest
	if !decodeBody(w, r, &req) {
		return
	}
	accountIDs := normalizeBatchSubscriptionAccountIDs(req.AccountIDs)
	if len(accountIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_ids is required"})
		return
	}

	resp := batchSubscriptionAccountQuotaResponse{Success: true, Message: "ok"}
	for _, accountID := range accountIDs {
		resetResp, err := svc.ResetSubscriptionAccountQuota(r.Context(), &adminv1.AdminResetSubscriptionAccountQuotaRequest{
			AccountId: accountID,
			Scope:     req.Scope,
		})
		if err != nil || resetResp == nil || !resetResp.GetSuccess() {
			resp.FailedIDs = append(resp.FailedIDs, accountID)
			continue
		}
		resp.UpdatedCount++
	}
	if len(resp.FailedIDs) > 0 {
		resp.Success = false
		resp.Message = "some subscription account quota resets failed"
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleBatchApplySubscriptionAccountQuotaTemplate(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req batchApplySubscriptionAccountQuotaTemplateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	accountIDs := normalizeBatchSubscriptionAccountIDs(req.AccountIDs)
	if len(accountIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_ids is required"})
		return
	}
	if req.Template.isEmpty() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "template is required"})
		return
	}

	resp := batchSubscriptionAccountQuotaResponse{Success: true, Message: "ok"}
	for _, accountID := range accountIDs {
		updateReq := &adminv1.AdminUpdateSubscriptionAccountRequest{Id: accountID}
		req.Template.applyTo(updateReq)
		updateResp, err := svc.UpdateSubscriptionAccount(r.Context(), updateReq)
		if err != nil || updateResp == nil || !updateResp.GetSuccess() {
			resp.FailedIDs = append(resp.FailedIDs, accountID)
			continue
		}
		resp.UpdatedCount++
	}
	if len(resp.FailedIDs) > 0 {
		resp.Success = false
		resp.Message = "some subscription account quota template updates failed"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (p subscriptionAccountQuotaTemplatePatch) isEmpty() bool {
	return p.QuotaLimitUSD == nil &&
		p.Quota5hLimitUSD == nil &&
		p.QuotaDailyLimitUSD == nil &&
		p.QuotaWeeklyLimitUSD == nil &&
		p.RateMultiplier == nil &&
		p.RPMLimit == nil &&
		p.SessionWindowLimitUSD == nil &&
		p.QuotaResetStrategy == nil &&
		p.QuotaTimezone == nil
}

func (p subscriptionAccountQuotaTemplatePatch) applyTo(req *adminv1.AdminUpdateSubscriptionAccountRequest) {
	if p.QuotaLimitUSD != nil {
		req.QuotaLimitUsd = p.QuotaLimitUSD
	}
	if p.Quota5hLimitUSD != nil {
		req.Quota_5HLimitUsd = p.Quota5hLimitUSD
	}
	if p.QuotaDailyLimitUSD != nil {
		req.QuotaDailyLimitUsd = p.QuotaDailyLimitUSD
	}
	if p.QuotaWeeklyLimitUSD != nil {
		req.QuotaWeeklyLimitUsd = p.QuotaWeeklyLimitUSD
	}
	if p.RateMultiplier != nil {
		req.RateMultiplier = p.RateMultiplier
	}
	if p.RPMLimit != nil {
		req.RpmLimit = p.RPMLimit
	}
	if p.SessionWindowLimitUSD != nil {
		req.SessionWindowLimitUsd = p.SessionWindowLimitUSD
	}
	if p.QuotaResetStrategy != nil {
		req.QuotaResetStrategy = p.QuotaResetStrategy
	}
	if p.QuotaTimezone != nil {
		req.QuotaTimezone = p.QuotaTimezone
	}
}

func handleOneAPIChannelByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/channel" || trimmed == "api/channel/search" || trimmed == "api/channel/models" ||
		trimmed == "api/channel/disabled" || trimmed == "api/channel/batch" || trimmed == "api/channel/fix" {
		handleOneAPIChannels(w, r, svc)
		return
	}
	channelID, ok := parsePathID(r.URL.Path, "/api/channel/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid channel id", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		channel, err := svc.GetChannel(r.Context(), channelID)
		if err != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", channel))
	case http.MethodDelete:
		resp, err := svc.DeleteChannel(r.Context(), &adminv1.AdminDeleteChannelRequest{ChannelId: channelID})
		writeOneAPIServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIDeleteDisabledChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
		Page:     1,
		PageSize: 1000,
		Status:   2,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}
	deleted := 0
	for _, channel := range resp.GetChannels() {
		delResp, delErr := svc.DeleteChannel(r.Context(), &adminv1.AdminDeleteChannelRequest{ChannelId: channel.GetId()})
		if delErr != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, delErr.Error(), deleted))
			return
		}
		if delResp != nil && !delResp.GetSuccess() {
			writeJSON(w, http.StatusOK, apiResponse(false, delResp.GetMessage(), deleted))
			return
		}
		deleted++
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", deleted))
}

func handleOneAPIBatchDeleteChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusOK, apiResponse(false, "ids are required", nil))
		return
	}
	deleted := 0
	for _, id := range req.IDs {
		if id <= 0 {
			writeJSON(w, http.StatusOK, apiResponse(false, "invalid channel id", deleted))
			return
		}
		delResp, delErr := svc.DeleteChannel(r.Context(), &adminv1.AdminDeleteChannelRequest{ChannelId: id})
		if delErr != nil {
			writeJSON(w, http.StatusOK, apiResponse(false, delErr.Error(), deleted))
			return
		}
		if delResp != nil && !delResp.GetSuccess() {
			writeJSON(w, http.StatusOK, apiResponse(false, delResp.GetMessage(), deleted))
			return
		}
		deleted++
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", deleted))
}

func handleOneAPIFixChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", 0))
}

func handleOneAPIListChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
		Page:     oneAPIPage(r),
		PageSize: oneAPIPageSize(r),
		Group:    r.URL.Query().Get("group"),
		Status:   getQueryInt32(r, "status", 0),
		Type:     getQueryInt32(r, "type", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetChannels()))
}

func handleOneAPIExportChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if !requireCSVExport(w, r) {
		return
	}
	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
		Page:     oneAPIPage(r),
		PageSize: oneAPIPageSize(r),
		Keyword:  r.URL.Query().Get("keyword"),
		Group:    r.URL.Query().Get("group"),
		Status:   getQueryInt32(r, "status", 0),
		Type:     getQueryInt32(r, "type", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	rows := make([][]string, 0, len(resp.GetChannels()))
	for _, channel := range resp.GetChannels() {
		rows = append(rows, []string{
			strconv.FormatInt(channel.GetId(), 10),
			channel.GetName(),
			strconv.FormatInt(int64(channel.GetType()), 10),
			channel.GetGroup(),
			channel.GetModels(),
			strconv.FormatInt(int64(channel.GetStatus()), 10),
			strconv.FormatInt(int64(channel.GetWeight()), 10),
			strconv.FormatFloat(channel.GetBalance(), 'f', -1, 64),
			strconv.FormatInt(channel.GetUsedQuota(), 10),
		})
	}
	writeCSV(w, "admin-channels.csv", []string{"id", "name", "type", "group", "models", "status", "weight", "balance", "used_quota"}, rows)
}

func handleOneAPISearchChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
		Page:     1,
		PageSize: oneAPIPageSize(r),
		Keyword:  r.URL.Query().Get("keyword"),
		Group:    r.URL.Query().Get("group"),
		Status:   getQueryInt32(r, "status", 0),
		Type:     getQueryInt32(r, "type", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetChannels()))
}

func handleOneAPIChannelModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   oneAPIChannelModelCatalog(),
	})
}

func oneAPIChannelModelCatalog() []map[string]interface{} {
	models := []struct {
		id      string
		ownedBy string
	}{
		{id: "gpt-4o-mini", ownedBy: "openai"},
		{id: "gpt-4o", ownedBy: "openai"},
		{id: "gpt-4-turbo", ownedBy: "openai"},
		{id: "gpt-3.5-turbo", ownedBy: "openai"},
		{id: "text-embedding-3-small", ownedBy: "openai"},
		{id: "text-embedding-3-large", ownedBy: "openai"},
		{id: "claude-3-5-sonnet-20241022", ownedBy: "anthropic"},
		{id: "claude-3-5-haiku-20241022", ownedBy: "anthropic"},
		{id: "gemini-pro", ownedBy: "gemini"},
		{id: "gemini-pro-vision", ownedBy: "gemini"},
		{id: "deepseek-chat", ownedBy: "deepseek"},
		{id: "deepseek-reasoner", ownedBy: "deepseek"},
		{id: "qwen-turbo", ownedBy: "tongyi"},
		{id: "qwen-plus", ownedBy: "tongyi"},
		{id: "qwen-max", ownedBy: "tongyi"},
		{id: "glm-4", ownedBy: "zhipu"},
		{id: "moonshot-v1-8k", ownedBy: "moonshot"},
		{id: "moonshot-v1-32k", ownedBy: "moonshot"},
		{id: "mistral-large-latest", ownedBy: "mistral"},
		{id: "command-r-plus", ownedBy: "cohere"},
		{id: "llama-3.1-sonar-small-128k-online", ownedBy: "perplexity"},
		{id: "voyage-3", ownedBy: "voyageai"},
	}
	permission := []map[string]interface{}{
		{
			"id":                   "modelperm-micro-one-api",
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
	}
	result := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		result = append(result, map[string]interface{}{
			"id":         model.id,
			"object":     "model",
			"created":    1626777600,
			"owned_by":   model.ownedBy,
			"permission": permission,
			"root":       model.id,
			"parent":     nil,
		})
	}
	return result
}

func handleChannelByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/channels/")
	if strings.HasSuffix(rest, "/status") {
		idPart := strings.TrimSuffix(rest, "/status")
		channelID, err := strconv.ParseInt(strings.Trim(idPart, "/"), 10, 64)
		if err != nil || channelID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channel id"})
			return
		}
		if r.Method != http.MethodPut {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req adminv1.AdminChangeChannelStatusRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.ChannelId = channelID
		resp, err := svc.ChangeChannelStatus(r.Context(), &req)
		writeServiceResponse(w, resp, err)
		return
	}

	channelID, ok := parsePathID(r.URL.Path, "/v1/channels/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channel id"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		resp, err := svc.DeleteChannel(r.Context(), &adminv1.AdminDeleteChannelRequest{ChannelId: channelID})
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.AdminUpdateChannelRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.ChannelId = channelID
		resp, err := svc.UpdateChannel(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleTestChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	page := getQueryInt32(r, "page", 1)
	pageSize := getQueryInt32(r, "page_size", 100)
	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	results := make([]map[string]interface{}, 0, len(resp.Channels))
	for _, channel := range resp.Channels {
		results = append(results, map[string]interface{}{
			"success":    true,
			"channel_id": channel.Id,
			"name":       channel.Name,
			"type":       channel.Type,
			"status":     channel.Status,
			"group":      channel.Group,
			"models":     channel.Models,
			"message":    "channel metadata resolved",
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "", "data": results})
}

func handleTestChannel(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	channelID, ok := parsePathID(r.URL.Path, "/api/channel/test/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channel id"})
		return
	}
	result, err := svc.TestChannel(r.Context(), channelID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "", "data": result})
}

func handleUpdateChannelBalances(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	results, err := svc.RefreshAllChannelBalances(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", results))
}

func handleUpdateChannelBalance(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	channelID, ok := parsePathID(r.URL.Path, "/api/channel/update_balance/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid channel id", nil))
		return
	}
	result, err := svc.RefreshChannelBalance(r.Context(), channelID)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(result.Success, result.Message, result))
}

func handleReconciliationRuns(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	page := getQueryInt32(r, "page", 1)
	pageSize := getQueryInt32(r, "page_size", 50)
	result, err := svc.ListReconciliationRuns(r.Context(), page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", result))
}

func handleReconciliationRunByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	runID, ok := parsePathID(r.URL.Path, "/api/reconciliation/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid run id", nil))
		return
	}
	run, err := svc.GetReconciliationRun(r.Context(), runID)
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse(false, err.Error(), nil))
		return
	}
	if run == nil {
		writeJSON(w, http.StatusNotFound, apiResponse(false, "reconciliation run not found", nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", run))
}

func handleOneAPIChannelStatusAlias(w http.ResponseWriter, r *http.Request, svc *service.AdminService, status int32, prefix string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	channelID, ok := parsePathID(r.URL.Path, prefix)
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid channel id", nil))
		return
	}
	resp, err := svc.ChangeChannelStatus(r.Context(), &adminv1.AdminChangeChannelStatusRequest{
		ChannelId: channelID,
		Status:    status,
	})
	writeOneAPIServiceResponse(w, resp, err)
}

func handleListChannels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	page := getQueryInt32(r, "page", 1)
	pageSize := getQueryInt32(r, "page_size", 20)
	keyword := r.URL.Query().Get("keyword")
	group := r.URL.Query().Get("group")
	status := getQueryInt32(r, "status", 0)
	chType := getQueryInt32(r, "type", 0)

	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{
		Page:     page,
		PageSize: pageSize,
		Keyword:  keyword,
		Group:    group,
		Status:   status,
		Type:     chType,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleGetSystemOptions(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		resp, err := svc.GetSystemOptions(r.Context(), &adminv1.GetSystemOptionsRequest{})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPut:
		var req adminv1.UpdateSystemOptionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		resp, err := svc.UpdateSystemOptions(r.Context(), &req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIOptions(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if strings.Trim(r.URL.Path, "/") != "api/option" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		options, err := svc.ListOneAPIOptions(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		resp := map[string]interface{}{
			"success": true,
			"message": "",
			"data":    options,
		}
		for _, option := range options {
			switch option.Key {
			case "SystemName":
				resp["site_title"] = option.Value
			case "RegisterEnabled":
				resp["registration_enabled"] = option.Value == "true"
			}
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPut:
		var raw struct {
			Key                 string `json:"key"`
			Value               string `json:"value"`
			SiteTitle           string `json:"site_title"`
			RegistrationEnabled *bool  `json:"registration_enabled"`
			Options             *struct {
				SiteTitle           string `json:"site_title"`
				RegistrationEnabled *bool  `json:"registration_enabled"`
			} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if raw.Key != "" {
			resp, err := svc.UpdateOneAPIOption(r.Context(), raw.Key, raw.Value)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": resp.GetSuccess(),
				"message": resp.GetMessage(),
			})
			return
		}
		updates := map[string]string{}
		if raw.SiteTitle != "" {
			updates["SystemName"] = raw.SiteTitle
		}
		if raw.RegistrationEnabled != nil {
			updates["RegisterEnabled"] = strconv.FormatBool(*raw.RegistrationEnabled)
		}
		if raw.Options != nil {
			if raw.Options.SiteTitle != "" {
				updates["SystemName"] = raw.Options.SiteTitle
			}
			if raw.Options.RegistrationEnabled != nil {
				updates["RegisterEnabled"] = strconv.FormatBool(*raw.Options.RegistrationEnabled)
			}
		}
		for key, value := range updates {
			resp, err := svc.UpdateOneAPIOption(r.Context(), key, value)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			if !resp.GetSuccess() {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"success": false,
					"message": resp.GetMessage(),
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "",
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleListLogs(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	page := getQueryInt32(r, "page", 1)
	pageSize := getQueryInt32(r, "page_size", 20)
	userID := r.URL.Query().Get("user_id")
	logType := r.URL.Query().Get("type")
	startTime := getQueryInt64(r, "start_time", 0)
	endTime := getQueryInt64(r, "end_time", 0)

	entries, total, err := svc.ListLedgerEntries(r.Context(), &adminv1.ListLogsRequest{
		Page:      page,
		PageSize:  pageSize,
		UserId:    userID,
		Type:      logType,
		StartTime: startTime,
		EndTime:   endTime,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
		"logs":  entries,
		"total": total,
	}))
}

func handleLogStats(w http.ResponseWriter, r *http.Request, svc *service.AdminService, selfOnly bool) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID := r.URL.Query().Get("user_id")
	if selfOnly && userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}
	stats, err := svc.GetLogStats(r.Context(), &adminv1.ListLogsRequest{
		Page:      1,
		PageSize:  1000,
		UserId:    userID,
		Type:      r.URL.Query().Get("type"),
		StartTime: getQueryInt64(r, "start_time", 0),
		EndTime:   getQueryInt64(r, "end_time", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "", "data": stats})
}

func handleOneAPILogs(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed != "api/log" && trimmed != "api/log/search" {
		handleOneAPILogByID(w, r, svc)
		return
	}
	if trimmed == "api/log/search" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		handleOneAPISearchLogs(w, r, svc)
		return
	}
	if r.Method == http.MethodDelete {
		handleOneAPIDeleteLogs(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	handleOneAPIListLogs(w, r, svc)
}

func handleOneAPILogByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/log" || trimmed == "api/log/search" {
		handleOneAPILogs(w, r, svc)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	idText := strings.TrimPrefix(trimmed, "api/log/")
	idText = strings.Trim(idText, "/")
	if idText == "" || strings.Contains(idText, "/") {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid log id", nil))
		return
	}
	if _, err := strconv.ParseInt(idText, 10, 64); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid log id", nil))
		return
	}
	handleOneAPIGetLog(w, r, svc, idText)
}

func handleOneAPIDeleteLogs(w http.ResponseWriter, r *http.Request) {
	if getQueryInt64(r, "end_time", 0) <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "end_time is required", nil))
		return
	}
	serviceToken := os.Getenv("SERVICE_TOKEN")
	targetURL, err := logServiceURLFromEnv("/v1/logs")
	if err != nil || serviceToken == "" {
		writeJSON(w, http.StatusNotImplemented, apiResponse(false, "log delete is not configured", nil))
		return
	}
	targetURL.RawQuery = r.URL.RawQuery
	resp, err := doLogServiceRequest(r.Context(), http.MethodDelete, targetURL, serviceToken)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse(false, "log service unavailable", nil))
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse(false, "failed to read log service response", nil))
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSON(w, resp.StatusCode, apiResponse(false, string(body), nil))
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"raw": string(body)}))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", payload))
}

func handleOneAPIGetLog(w http.ResponseWriter, r *http.Request, svc *service.AdminService, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "invalid log id", nil))
		return
	}
	entry, err := svc.GetLedgerEntry(r.Context(), id)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			writeJSON(w, http.StatusNotFound, apiResponse(false, "ledger entry not found", nil))
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, "failed to get log detail", nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", entry))
}

func logServiceURLFromEnv(path string) (*url.URL, error) {
	return logServiceURL(strings.TrimSpace(os.Getenv("LOG_HTTP_ENDPOINT")), path)
}

func logServiceURL(endpoint, path string) (*url.URL, error) {
	if endpoint == "" {
		return nil, errors.New("missing log endpoint")
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "?") || strings.Contains(path, "#") {
		return nil, errors.New("log service path must be absolute and clean")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("log endpoint scheme must be http or https")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("log endpoint must be an origin URL")
	}
	if !isAllowedLogServiceOrigin(u.Scheme, u.Hostname()) {
		return nil, errors.New("log endpoint origin is not allowed")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.ForceQuery = false
	return u, nil
}

func doLogServiceRequest(ctx context.Context, method string, targetURL *url.URL, serviceToken string) (*http.Response, error) {
	if targetURL == nil || serviceToken == "" {
		return nil, errors.New("missing log service request config")
	}
	if targetURL.Scheme != "http" && targetURL.Scheme != "https" {
		return nil, errors.New("invalid log service scheme")
	}
	if targetURL.Host == "" || targetURL.User != nil || !isAllowedLogServiceOrigin(targetURL.Scheme, targetURL.Hostname()) {
		return nil, errors.New("invalid log service origin")
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL.String(), nil) // #nosec G704 -- targetURL is built from LOG_HTTP_ENDPOINT and validated by logServiceURL/doLogServiceRequest.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+serviceToken)
	return http.DefaultClient.Do(req) // #nosec G704 -- request URL is validated to an allowed log-service origin above.
}

func isAllowedLogServiceOrigin(scheme, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if scheme == "https" {
		return true
	}
	return host == "localhost" ||
		host == "127.0.0.1" ||
		host == "::1" ||
		host == "log-service" ||
		strings.HasSuffix(host, ".svc") ||
		strings.HasSuffix(host, ".svc.cluster.local")
}

func handleOneAPIListLogs(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	entries, total, err := svc.ListLedgerEntries(r.Context(), &adminv1.ListLogsRequest{
		Page:      oneAPIPage(r),
		PageSize:  oneAPIPageSize(r),
		UserId:    r.URL.Query().Get("user_id"),
		Type:      r.URL.Query().Get("type"),
		StartTime: getQueryInt64(r, "start_time", 0),
		EndTime:   getQueryInt64(r, "end_time", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
		"logs":  entries,
		"total": total,
	}))
}

func handleOneAPIExportLogs(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if !requireCSVExport(w, r) {
		return
	}
	resp, err := svc.ListLogs(r.Context(), &adminv1.ListLogsRequest{
		Page:      oneAPIPage(r),
		PageSize:  oneAPIPageSize(r),
		UserId:    r.URL.Query().Get("user_id"),
		Type:      r.URL.Query().Get("type"),
		StartTime: getQueryInt64(r, "start_time", 0),
		EndTime:   getQueryInt64(r, "end_time", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	rows := make([][]string, 0, len(resp.GetLogs()))
	for _, log := range resp.GetLogs() {
		rows = append(rows, []string{
			strconv.FormatInt(log.GetId(), 10),
			log.GetUserId(),
			log.GetType(),
			strconv.FormatInt(log.GetAmount(), 10),
			strconv.FormatInt(log.GetBalanceAfter(), 10),
			log.GetReferenceId(),
			log.GetRemark(),
			strconv.FormatInt(log.GetCreatedAt(), 10),
		})
	}
	writeCSV(w, "admin-billing-logs.csv", []string{"id", "user_id", "type", "amount", "balance_after", "reference_id", "remark", "created_at"}, rows)
}

func handleOneAPISearchLogs(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	handleOneAPIListLogs(w, r, svc)
}

func handleGetAccount(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}

	resp, err := svc.GetAccountSnapshot(r.Context(), &adminv1.GetAccountSnapshotRequest{
		UserId: userID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleRedeemCodes(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("keyword") != "" {
			resp, err := svc.SearchRedeemCodes(r.Context(), &adminv1.SearchRedeemCodesRequest{Keyword: r.URL.Query().Get("keyword")})
			writeServiceResponse(w, resp, err)
			return
		}
		handleListRedeemCodes(w, r, svc)
	case http.MethodPost:
		var req adminv1.CreateRedeemCodeRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateRedeemCode(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.UpdateRedeemCodeRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.UpdateRedeemCode(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleRedeemCodesBatch(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req adminv1.CreateRedeemCodesBatchRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.CreateRedeemCodesBatch(r.Context(), &req)
	writeServiceResponse(w, resp, err)
}

func handleRedeemCodeByCode(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	code, ok := parsePathValue(r.URL.Path, "/v1/redeem-codes/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid redeem code"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := svc.GetRedeemCode(r.Context(), &adminv1.GetRedeemCodeRequest{Code: code})
		writeServiceResponse(w, resp, err)
	case http.MethodDelete:
		resp, err := svc.DeleteRedeemCode(r.Context(), &adminv1.DeleteRedeemCodeRequest{Code: code})
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.UpdateRedeemCodeRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.Code = code
		resp, err := svc.UpdateRedeemCode(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIRedemptionByCode(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	trimmed := strings.Trim(r.URL.Path, "/")
	if trimmed == "api/redemption" || trimmed == "api/redemption/search" {
		handleOneAPIRedemptions(w, r, svc)
		return
	}
	code, ok := parsePathValue(r.URL.Path, "/api/redemption/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid redemption code"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		resp, err := svc.GetRedeemCode(r.Context(), &adminv1.GetRedeemCodeRequest{Code: code})
		writeServiceResponse(w, resp, err)
	case http.MethodDelete:
		resp, err := svc.DeleteRedeemCode(r.Context(), &adminv1.DeleteRedeemCodeRequest{Code: code})
		writeServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.UpdateRedeemCodeRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.Code = code
		resp, err := svc.UpdateRedeemCode(r.Context(), &req)
		writeServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIRedemptions(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		if keyword := r.URL.Query().Get("keyword"); keyword != "" {
			resp, err := svc.SearchRedeemCodes(r.Context(), &adminv1.SearchRedeemCodesRequest{Keyword: keyword})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetCodes()))
			return
		}
		resp, err := svc.ListRedeemCodes(r.Context(), &adminv1.ListRedeemCodesRequest{
			Page:     oneAPIPage(r),
			PageSize: oneAPIPageSize(r),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetCodes()))
	case http.MethodPost:
		var req adminv1.CreateRedeemCodesBatchRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if req.BatchSize == 0 {
			req.BatchSize = req.Count
		}
		resp, err := svc.CreateRedeemCodesBatch(r.Context(), &req)
		writeOneAPIServiceResponse(w, resp, err)
	case http.MethodPut:
		var req adminv1.UpdateRedeemCodeRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.UpdateRedeemCode(r.Context(), &req)
		writeOneAPIServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleOneAPIExportRedemptions(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if !requireCSVExport(w, r) {
		return
	}
	var codes []*adminv1.RedeemCodeInfo
	if keyword := r.URL.Query().Get("keyword"); keyword != "" {
		resp, err := svc.SearchRedeemCodes(r.Context(), &adminv1.SearchRedeemCodesRequest{Keyword: keyword})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		codes = resp.GetCodes()
	} else {
		resp, err := svc.ListRedeemCodes(r.Context(), &adminv1.ListRedeemCodesRequest{
			Page:     oneAPIPage(r),
			PageSize: oneAPIPageSize(r),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		codes = resp.GetCodes()
	}
	rows := make([][]string, 0, len(codes))
	for _, code := range codes {
		if status := getQueryInt32(r, "status", 0); status != 0 && code.GetStatus() != status {
			continue
		}
		rows = append(rows, []string{
			code.GetCode(),
			code.GetName(),
			strconv.FormatInt(code.GetAmount(), 10),
			strconv.FormatInt(int64(code.GetCount()), 10),
			strconv.FormatInt(int64(code.GetStatus()), 10),
			code.GetCreatedBy(),
			strconv.FormatInt(code.GetCreatedAt(), 10),
		})
	}
	writeCSV(w, "admin-redemptions.csv", []string{"code", "name", "amount", "count", "status", "created_by", "created_at"}, rows)
}

func handleListRedeemCodes(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	page := getQueryInt32(r, "page", 1)
	pageSize := getQueryInt32(r, "page_size", 20)

	resp, err := svc.ListRedeemCodes(r.Context(), &adminv1.ListRedeemCodesRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleTopUp(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req adminv1.TopUpQuotaRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.TopUpQuota(r.Context(), &req)
	writeServiceResponse(w, resp, err)
}

func handleResetUserQuota(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req adminv1.ResetUserQuotaRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := svc.ResetUserQuota(r.Context(), &req)
	writeServiceResponse(w, resp, err)
}

func handlePaymentOrders(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp, err := svc.ListPaymentOrders(r.Context(), &billingv1.ListPaymentOrdersRequest{
		Page:      getQueryInt32(r, "page", 1),
		PageSize:  getQueryInt32(r, "page_size", 20),
		UserId:    r.URL.Query().Get("user_id"),
		Status:    r.URL.Query().Get("status"),
		Channel:   r.URL.Query().Get("channel"),
		TradeNo:   r.URL.Query().Get("trade_no"),
		StartTime: getQueryInt64(r, "start_time", 0),
		EndTime:   getQueryInt64(r, "end_time", 0),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, err.Error(), nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{
		"orders": resp.GetOrders(),
		"total":  resp.GetTotal(),
	}))
}

func handlePaymentOrderByTradeNo(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	tradeNo, ok := parsePathValue(r.URL.Path, "/api/payment/orders/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "trade_no is required", nil))
		return
	}
	resp, err := svc.GetPaymentOrderByTradeNo(r.Context(), &billingv1.GetPaymentOrderByTradeNoRequest{TradeNo: tradeNo})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, err.Error(), nil))
		return
	}
	if !resp.GetSuccess() {
		message := resp.GetErrorMessage()
		if message == "" {
			message = "payment order not found"
		}
		writeJSON(w, http.StatusOK, apiResponse(false, message, nil))
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"order": resp.GetOrder()}))
}

func writeServiceResponse(w http.ResponseWriter, resp interface{}, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// newNotifyWorkerProxy creates a reverse proxy to the notify-worker HTTP service.
// It reads the NOTIFY_HTTP_ENDPOINT environment variable, defaulting to http://notify-worker:8008.
func newNotifyWorkerProxy() *httputil.ReverseProxy {
	endpoint := os.Getenv("NOTIFY_HTTP_ENDPOINT")
	if endpoint == "" {
		// Default to notify-worker's HTTP port
		endpoint = "http://notify-worker:8008"
	}
	target, err := parseReverseProxyTarget(endpoint)
	if err != nil {
		return nil
	}
	return httputil.NewSingleHostReverseProxy(target) // #nosec G704 -- endpoint is configured by operators and validated by parseReverseProxyTarget.
}

// newChannelHTTPProxy builds a reverse proxy to channel-service's HTTP port.
// The subscription-account OAuth authorization-code endpoints live only on
// channel-service HTTP (not the gRPC surface admin-api normally speaks), so the
// SPA cannot reach them through the /api gateway without this proxy.
func newChannelHTTPProxy() *httputil.ReverseProxy {
	endpoint := os.Getenv("CHANNEL_HTTP_ENDPOINT")
	if endpoint == "" {
		// Default to channel-service's HTTP port (see app/channel/configs/config.yaml).
		endpoint = "http://channel-service:8002"
	}
	target, err := parseReverseProxyTarget(endpoint)
	if err != nil {
		return nil
	}
	return httputil.NewSingleHostReverseProxy(target) // #nosec G704 -- endpoint is configured by operators and validated by parseReverseProxyTarget.
}

// newBillingHTTPProxy builds a reverse proxy to billing-service's HTTP port.
// It is used only for unauthenticated provider callbacks such as Alipay notify.
func newBillingHTTPProxy() *httputil.ReverseProxy {
	endpoint := os.Getenv("BILLING_HTTP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://billing-service:8004"
	}
	target, err := parseReverseProxyTarget(endpoint)
	if err != nil {
		return nil
	}
	return httputil.NewSingleHostReverseProxy(target) // #nosec G704 -- endpoint is configured by operators and validated by parseReverseProxyTarget.
}

// handleNotifyProxy proxies requests to the notify-worker's /v1/notifications endpoint.
// It adds the admin authentication wrapper and forwards the request with the original query parameters.
func handleNotifyProxy(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Update the request path to point to notify-worker's endpoint
	// /api/admin/notifications -> /v1/notifications
	r.URL.Path = "/v1/notifications"

	// Proxy the request
	proxy.ServeHTTP(w, r)
}

// handleNotifyProxyByID proxies requests to the notify-worker's /v1/notifications/{id} endpoint.
// It handles GET requests for fetching a single notification and PUT requests for updating status.
func handleNotifyProxyByID(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy) {
	// Extract the ID from the path
	// Path format: /api/admin/notifications/{id} or /api/admin/notifications/{id}/status
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid notification path"})
		return
	}

	// Extract notification ID and optional action (like "status")
	notificationID := pathParts[4]
	action := ""
	if len(pathParts) > 5 {
		action = pathParts[5]
	}

	// For status updates, we need to handle it specially since notify-worker's HTTP server
	// only supports GET on /v1/notifications/{id}, does not support PUT
	if action == "status" && r.Method == http.MethodPut {
		// Parse the request body to get the new status
		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Proxy to notify-worker's gRPC endpoint via HTTP (not available in current HTTP server)
		// For now, return an error indicating this feature is not yet available
		writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
			"success": false,
			"message": "Notification status update via HTTP is not yet implemented. Please use the gRPC endpoint.",
		})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Update the request path to point to notify-worker's endpoint
	r.URL.Path = "/v1/notifications/" + notificationID

	// Proxy the request
	proxy.ServeHTTP(w, r)
}
