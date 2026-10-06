package server

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"strconv"
	"strings"
)

type iamBusinessContextKey struct{}

func iamBusinessContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, iamBusinessContextKey{}, true)
}
func isIAMBusinessContext(ctx context.Context) bool {
	value, _ := ctx.Value(iamBusinessContextKey{}).(bool)
	return value
}

// Only completed business slices can pass the console IAM guard. Each handler
// still obtains its precise resource/field decision from the owning service.
// Every accepted shape is fixed here; its owner still authorizes the operation. Unknown shapes remain denied.
func iamUserRouteReady(r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(path, "/api/v1/admin/routing-access/") {
		rest := strings.TrimPrefix(path, "/api/v1/admin/routing-access/")
		if strings.HasSuffix(rest, "/available") {
			return positivePathID(strings.TrimSuffix(rest, "/available")) && r.Method == http.MethodGet
		}
		id, err := strconv.ParseInt(rest, 10, 64)
		return err == nil && id > 0 && (r.Method == http.MethodGet || r.Method == http.MethodPatch)
	}
	if path == "/api/admin/access" {
		return r.Method == http.MethodGet
	}
	for _, base := range []string{"/v1/users", "/api/user"} {
		if path == base || path == base+"/search" {
			return r.Method == http.MethodGet || (path == base && (r.Method == http.MethodPut || r.Method == http.MethodPost))
		}
		if strings.HasPrefix(path, base+"/") {
			id, err := strconv.ParseInt(strings.TrimPrefix(path, base+"/"), 10, 64)
			if err == nil && id > 0 {
				return r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete
			}
		}
	}
	return iamOwnerRouteReady(path, r.Method)
}

func resourceHTTPErrorCode(err error) (int, bool) {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return http.StatusUnauthorized, true
	case codes.PermissionDenied:
		return http.StatusForbidden, true
	case codes.Aborted, codes.AlreadyExists:
		return http.StatusConflict, true
	case codes.InvalidArgument:
		return http.StatusBadRequest, true
	case codes.Unavailable:
		return http.StatusServiceUnavailable, true
	default:
		return 0, false
	}
}

func positivePathID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0
}
func oneOfMethod(method string, allowed ...string) bool {
	for _, value := range allowed {
		if method == value {
			return true
		}
	}
	return false
}

// These shapes correspond to owner implementations verified in B2/B4. No
// prefix grants access to arbitrary actions or to the system-only endpoints.
func iamOwnerRouteReady(path, method string) bool {
	if strings.HasPrefix(path, "/api/v1/admin/accounts/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/accounts/"), "/")
		return len(parts) == 2 && positivePathID(parts[0]) && parts[1] == "balance:reset" && method == http.MethodPost
	}
	switch path {
	case "/api/notice", "/api/about", "/api/home_page_content":
		return oneOfMethod(method, http.MethodPut, http.MethodPost)
	case "/v1/system/options", "/api/option":
		return oneOfMethod(method, http.MethodGet, http.MethodPut)
	case "/api/v1/admin/reports/cost:export", "/api/redemption/export", "/api/admin/model-health", "/api/admin/models/export", "/api/admin/models/unpriced":
		return method == http.MethodGet
	case "/api/admin/models/canonical/preflight":
		return method == http.MethodGet
	case "/api/admin/models/canonical/merge":
		return method == http.MethodPost
	case "/api/admin/models/import", "/api/admin/upstream-costs/migrate":
		return method == http.MethodPost
	case "/api/admin/upstream-costs":
		return oneOfMethod(method, http.MethodGet, http.MethodPost, http.MethodDelete)
	case "/api/admin/model-routings":
		return oneOfMethod(method, http.MethodGet, http.MethodPost)
	case "/api/v1/admin/subscriptions", "/api/v1/admin/subscriptions/operation-report":
		return method == http.MethodGet
	case "/api/v1/admin/subscriptions/assign", "/api/v1/admin/subscriptions/change":
		return method == http.MethodPost
	case "/api/v1/admin/subscription-groups", "/api/v1/admin/subscription-plans", "/v1/redeem-codes", "/api/redemption":
		return oneOfMethod(method, http.MethodGet, http.MethodPost)
	case "/api/redemption/search":
		return method == http.MethodGet
	case "/v1/redeem-codes/batch", "/v1/topup", "/api/topup":
		return method == http.MethodPost
	case "/api/user/export", "/api/log/export", "/api/log/attempts", "/api/log/routing-audit":
		return method == http.MethodGet
	case "/api/reconciliation":
		return oneOfMethod(method, http.MethodGet, http.MethodPost)
	case "/api/admin/service-health", "/api/admin/service-health/latest":
		return method == http.MethodGet
	case "/api/admin/alert-rules":
		return oneOfMethod(method, http.MethodGet, http.MethodPost)
	case "/api/admin/summary", "/api/admin/routing-ops":
		return method == http.MethodGet
	case "/api/admin/models":
		return oneOfMethod(method, http.MethodGet, http.MethodPost, http.MethodPut)
	case "/api/admin/models/batch":
		return method == http.MethodPost
	case "/v1/channels", "/api/channel":
		return oneOfMethod(method, http.MethodGet, http.MethodPost, http.MethodPut)
	case "/api/channel/batch":
		return method == http.MethodPost
	case "/api/channel/export", "/api/channel/update_balance":
		return method == http.MethodGet
	case "/api/channel/search":
		return method == http.MethodGet
	case "/v1/subscription-accounts", "/api/subscription-accounts":
		return oneOfMethod(method, http.MethodGet, http.MethodPost)
	case "/api/admin/notifications":
		return method == http.MethodGet
	case "/api/admin/notification-rules":
		return method == http.MethodGet
	case "/api/admin/request-logs":
		return oneOfMethod(method, http.MethodGet, http.MethodDelete)
	case "/api/admin/request-logs/export":
		return method == http.MethodGet
	case "/api/admin/request-logs/purge":
		return method == http.MethodPost
	case "/api/v1/admin/routing-groups":
		return oneOfMethod(method, http.MethodGet, http.MethodPost)
	case "/v1/logs", "/api/log", "/api/log/search", "/api/log/stat", "/api/log/self/stat", "/v1/account", "/api/payment/orders":
		return method == http.MethodGet
	case "/api/v1/admin/payments/refund":
		return method == http.MethodPost
	}

	for _, prefix := range []string{"/api/v1/admin/subscription-groups/", "/api/v1/admin/subscription-plans/"} {
		if strings.HasPrefix(path, prefix) {
			parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
			if len(parts) == 1 && positivePathID(parts[0]) {
				return oneOfMethod(method, http.MethodGet, http.MethodPut, http.MethodDelete)
			}
			return prefix == "/api/v1/admin/subscription-plans/" && len(parts) == 2 && positivePathID(parts[0]) && parts[1] == "for-sale" && method == http.MethodPost
		}
	}
	if strings.HasPrefix(path, "/api/v1/admin/subscriptions/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/subscriptions/"), "/")
		return len(parts) == 2 && positivePathID(parts[0]) && oneOfMethod(parts[1], "revoke", "extend", "reset-quota") && method == http.MethodPost
	}
	if strings.HasPrefix(path, "/api/admin/model-routings/") {
		return positivePathID(strings.TrimPrefix(path, "/api/admin/model-routings/")) && method == http.MethodDelete
	}
	if strings.HasPrefix(path, "/api/reconciliation/") {
		return positivePathID(strings.TrimPrefix(path, "/api/reconciliation/")) && method == http.MethodGet
	}
	for _, prefix := range []string{"/v1/redeem-codes/", "/api/redemption/"} {
		if strings.HasPrefix(path, prefix) {
			rest := strings.TrimPrefix(path, prefix)
			return rest != "" && !strings.Contains(rest, "/") && oneOfMethod(method, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
	}

	for _, prefix := range []string{"/api/user/disable/", "/api/user/enable/"} {
		if strings.HasPrefix(path, prefix) {
			return positivePathID(strings.TrimPrefix(path, prefix)) && oneOfMethod(method, http.MethodGet, http.MethodPost)
		}
	}
	if strings.HasPrefix(path, "/api/admin/alert-rules/") {
		return positivePathID(strings.TrimPrefix(path, "/api/admin/alert-rules/")) && oneOfMethod(method, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
	if strings.HasPrefix(path, "/api/admin/configs/") {
		rest := strings.TrimPrefix(path, "/api/admin/configs/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 && parts[0] != "" {
			return method == http.MethodGet
		}
		return len(parts) == 2 && parts[0] != "" && parts[1] != "" && oneOfMethod(method, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
	for _, prefix := range []string{"/v1/channels/", "/api/channel/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		if positivePathID(rest) {
			return oneOfMethod(method, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && positivePathID(parts[0]) && parts[1] == "status" && prefix == "/v1/channels/" {
			return method == http.MethodPut
		}
		if len(parts) == 2 && positivePathID(parts[1]) && prefix == "/api/channel/" && (parts[0] == "enable" || parts[0] == "disable") {
			return oneOfMethod(method, http.MethodGet, http.MethodPut)
		}
		if len(parts) == 2 && positivePathID(parts[1]) && prefix == "/api/channel/" && (parts[0] == "test" || parts[0] == "update_balance") {
			return method == http.MethodGet
		}
	}
	for _, prefix := range []string{"/v1/subscription-accounts/", "/api/subscription-accounts/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
		if len(parts) == 1 && positivePathID(parts[0]) {
			return oneOfMethod(method, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
		if len(parts) == 2 && positivePathID(parts[0]) {
			switch parts[1] {
			case "status":
				return method == http.MethodPut
			case "reset-quota", "clear-error":
				return method == http.MethodPost
			}
		}
	}
	if strings.HasPrefix(path, "/api/admin/models/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/admin/models/"), "/")
		if len(parts) == 1 && positivePathID(parts[0]) {
			return oneOfMethod(method, http.MethodGet, http.MethodDelete)
		}
		if len(parts) == 2 && positivePathID(parts[0]) {
			switch parts[1] {
			case "status":
				return method == http.MethodPatch
			case "aliases":
				return oneOfMethod(method, http.MethodGet, http.MethodPost)
			case "channels", "subscriptions", "usage-stats":
				return method == http.MethodGet
			}
		}
		if len(parts) == 3 && positivePathID(parts[0]) && parts[1] == "aliases" && positivePathID(parts[2]) {
			return method == http.MethodDelete
		}
	}
	for _, prefix := range []string{"/api/admin/channels/", "/api/admin/subscription-accounts/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
		if len(parts) == 2 && positivePathID(parts[0]) && parts[1] == "models" {
			return oneOfMethod(method, http.MethodGet, http.MethodPost)
		}
		if len(parts) == 3 && positivePathID(parts[0]) && parts[1] == "models" && positivePathID(parts[2]) {
			return method == http.MethodDelete
		}
	}
	if strings.HasPrefix(path, "/api/admin/notifications/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/admin/notifications/"), "/")
		if len(parts) == 1 && positivePathID(parts[0]) {
			return method == http.MethodGet
		}
		return len(parts) == 2 && positivePathID(parts[0]) && parts[1] == "acknowledge" && method == http.MethodPost
	}
	if strings.HasPrefix(path, "/api/admin/notification-rules/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/admin/notification-rules/"), "/")
		if len(parts) == 1 && positivePathID(parts[0]) {
			return method == http.MethodPut
		}
		return len(parts) == 2 && positivePathID(parts[0]) && parts[1] == "test" && method == http.MethodPost
	}
	if strings.HasPrefix(path, "/api/admin/request-logs/") {
		return positivePathID(strings.TrimPrefix(path, "/api/admin/request-logs/")) && method == http.MethodGet
	}
	if strings.HasPrefix(path, "/api/v1/admin/routing-groups/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/routing-groups/"), "/")
		if len(parts) == 1 && positivePathID(parts[0]) {
			return oneOfMethod(method, http.MethodGet, http.MethodPatch)
		}
		if len(parts) == 2 && positivePathID(parts[0]) {
			switch parts[1] {
			case "resource-overrides":
				return method == http.MethodPut
			case "archive":
				return method == http.MethodPost
			case "members":
				return method == http.MethodPut
			case "billing":
				return oneOfMethod(method, http.MethodGet, http.MethodPatch)
			}
		}
		return len(parts) == 3 && positivePathID(parts[0]) && parts[1] == "user-price" && positivePathID(parts[2]) && oneOfMethod(method, http.MethodPut, http.MethodDelete)
	}
	if strings.HasPrefix(path, "/api/payment/orders/") {
		return len(strings.Split(strings.TrimPrefix(path, "/api/payment/orders/"), "/")) == 1 && method == http.MethodGet
	}
	if strings.HasPrefix(path, "/api/log/") {
		return positivePathID(strings.TrimPrefix(path, "/api/log/")) && method == http.MethodGet
	}
	return false
}

func writeAdminRPCError(w http.ResponseWriter, err error) {
	writeServiceResponse(w, nil, err)
}
