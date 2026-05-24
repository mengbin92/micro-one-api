package server

import (
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"

	adminv1 "micro-one-api/api/admin/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/internal/admin/service"
	"micro-one-api/internal/pkg/metrics"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

//go:embed all:static/web
var webFS embed.FS

// AdminAuth creates a middleware that validates Bearer token against ADMIN_TOKEN env var.
// If ADMIN_TOKEN is not set, the middleware rejects all requests to protected endpoints.
func AdminAuth(next http.HandlerFunc) http.HandlerFunc {
	adminToken := os.Getenv("ADMIN_TOKEN")
	return func(w http.ResponseWriter, r *http.Request) {
		if adminToken == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin token not configured"})
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin token"})
			return
		}
		next(w, r)
	}
}

// NewHTTPServer wires HTTP transport for admin-api.
func NewHTTPServer(addr string, svc *service.AdminService, identityHTTPEndpoint ...string) *khttp.Server {
	srv := khttp.NewServer(
		khttp.Address(addr),
	)
	identityProxy := newServiceReverseProxy(firstString(identityHTTPEndpoint))

	// Health and metrics (unauthenticated)
	srv.HandleFunc("/", handleAdminPage)
	srv.HandleFunc("/admin", handleAdminPage)
	// SPA client-side routes — must mirror entries in web/src/router.tsx
	srv.HandleFunc("/login", handleAdminPage)
	srv.HandleFunc("/dashboard", handleAdminPage)
	srv.HandleFunc("/tokens", handleAdminPage)
	srv.HandleFunc("/admin/users", handleAdminPage)
	srv.HandleFunc("/admin/channels", handleAdminPage)
	srv.HandleFunc("/admin/logs", handleAdminPage)
	srv.HandleFunc("/admin/redemptions", handleAdminPage)
	srv.HandleFunc("/admin/options", handleAdminPage)
	// Static assets bundled by Vite
	srv.HandlePrefix("/assets/", http.HandlerFunc(handleAdminPage))
	srv.HandleFunc("/favicon.svg", handleAdminPage)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	srv.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "",
			"data": map[string]interface{}{
				"version":              "micro-one-api",
				"system_name":          "micro-one-api",
				"registration_enabled": true,
				"email_verification":   false,
				"github_oauth":         false,
				"wechat_login":         false,
				"turnstile_check":      false,
				"display_in_currency":  false,
			},
		})
	})
	srv.HandleFunc("/api/notice", handleContentRoute(svc, "Notice"))
	srv.HandleFunc("/api/about", handleContentRoute(svc, "About"))
	srv.HandleFunc("/api/home_page_content", handleContentRoute(svc, "HomePageContent"))
	if identityProxy != nil {
		srv.HandleFunc("/api/user/register", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/login", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/logout", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/self", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/dashboard", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/quota", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/amount", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/user/pay", identityProxy.ServeHTTP)
		srv.HandleFunc("/api/token", identityProxy.ServeHTTP)
		srv.HandlePrefix("/api/token/", identityProxy)
	}
	srv.HandlePrefix("/api/group", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGroupManagement(w, r, svc)
	}))
	srv.HandleFunc("/api/admin/access", AdminAuth(handleAdminAccess))

	// Protected admin endpoints
	srv.HandlePrefix("/api/user/disable/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserStatusAlias(w, r, svc, 2, "/api/user/disable/")
	}))
	srv.HandlePrefix("/api/user/enable/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserStatusAlias(w, r, svc, 1, "/api/user/enable/")
	}))
	srv.HandleFunc("/api/user/export", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportUsers(w, r, svc)
	}))
	srv.HandlePrefix("/api/user/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserByID(w, r, svc)
	}))
	srv.HandlePrefix("/api/user", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUsers(w, r, svc)
	}))
	srv.HandleFunc("/v1/users", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUsers(w, r, svc)
	}))
	srv.HandlePrefix("/v1/users/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUserByID(w, r, svc)
	}))

	srv.HandleFunc("/api/channel/models", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelModels(w, r, svc)
	}))
	srv.HandleFunc("/v1/channels", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleChannels(w, r, svc)
	}))
	srv.HandlePrefix("/v1/channels/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleChannelByID(w, r, svc)
	}))

	srv.HandleFunc("/v1/system/options", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGetSystemOptions(w, r, svc)
	}))
	srv.HandlePrefix("/api/option", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIOptions(w, r, svc)
	}))

	srv.HandleFunc("/v1/logs", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleListLogs(w, r, svc)
	}))
	srv.HandleFunc("/api/log/stat", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleLogStats(w, r, svc, false)
	}))
	srv.HandleFunc("/api/log/self/stat", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleLogStats(w, r, svc, true)
	}))
	srv.HandleFunc("/api/log/export", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportLogs(w, r, svc)
	}))
	srv.HandlePrefix("/api/log/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPILogByID(w, r, svc)
	}))
	srv.HandlePrefix("/api/log", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPILogs(w, r, svc)
	}))

	srv.HandleFunc("/v1/account", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGetAccount(w, r, svc)
	}))

	srv.HandleFunc("/v1/redeem-codes", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodes(w, r, svc)
	}))
	srv.HandleFunc("/v1/redeem-codes/batch", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodesBatch(w, r, svc)
	}))
	srv.HandlePrefix("/v1/redeem-codes/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodeByCode(w, r, svc)
	}))
	srv.HandleFunc("/api/redemption/export", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportRedemptions(w, r, svc)
	}))
	srv.HandlePrefix("/api/redemption/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIRedemptionByCode(w, r, svc)
	}))
	srv.HandlePrefix("/api/redemption", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIRedemptions(w, r, svc)
	}))
	srv.HandleFunc("/v1/topup", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTopUp(w, r, svc)
	}))
	srv.HandleFunc("/v1/users/reset-quota", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleResetUserQuota(w, r, svc)
	}))
	srv.HandleFunc("/api/topup", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTopUp(w, r, svc)
	}))
	srv.HandleFunc("/api/channel/test", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTestChannels(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/test/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTestChannel(w, r, svc)
	}))
	srv.HandleFunc("/api/channel/update_balance", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateChannelBalances(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/update_balance/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUpdateChannelBalance(w, r, svc)
	}))
	srv.HandlePrefix("/api/reconciliation/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleReconciliationRunByID(w, r, svc)
	}))
	srv.HandleFunc("/api/reconciliation", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleReconciliationRuns(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/disable/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelStatusAlias(w, r, svc, 2, "/api/channel/disable/")
	}))
	srv.HandlePrefix("/api/channel/enable/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelStatusAlias(w, r, svc, 1, "/api/channel/enable/")
	}))
	srv.HandleFunc("/api/channel/export", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIExportChannels(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelByID(w, r, svc)
	}))
	srv.HandlePrefix("/api/channel", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannels(w, r, svc)
	}))

	return srv
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func newServiceReverseProxy(endpoint string) *httputil.ReverseProxy {
	if strings.TrimSpace(endpoint) == "" {
		return nil
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	return httputil.NewSingleHostReverseProxy(target)
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
	writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]bool{"admin": true}))
}

func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	distFS, err := fs.Sub(webFS, "static/web")
	if err != nil {
		http.Error(w, "frontend not available", http.StatusInternalServerError)
		return
	}

	// SPA fallback: any path without a file extension serves index.html so
	// client-side routes (/login, /dashboard, /tokens) load the React shell.
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || !strings.Contains(path, ".") {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		http.FileServer(http.FS(distFS)).ServeHTTP(w, r2)
		return
	}

	http.FileServer(http.FS(distFS)).ServeHTTP(w, r)
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
		return int32(p + 1)
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
	}
	writeJSON(w, http.StatusOK, apiResponse(success, message, resp))
}

func handleContentRoute(svc *service.AdminService, key string) http.HandlerFunc {
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
		AdminAuth(func(w http.ResponseWriter, r *http.Request) {
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
			adminv1.AdminUpdateUserRequest
		}
		if !decodeBody(w, r, &raw) {
			return
		}
		req := raw.AdminUpdateUserRequest
		if req.UserId == 0 {
			req.UserId = raw.ID
		}
		resp, err := svc.UpdateUser(r.Context(), &req)
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
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"status": status, "role": 0}))
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
		writeJSON(w, http.StatusOK, apiResponse(true, "", map[string]interface{}{"status": user.GetStatus(), "role": 0}))
	case "promote", "demote":
		writeJSON(w, http.StatusOK, apiResponse(false, "role management is not supported", nil))
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
	writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetUsers()))
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
			strconv.FormatInt(user.GetQuota(), 10),
			strconv.FormatInt(user.GetUsedQuota(), 10),
			strconv.FormatInt(int64(user.GetStatus()), 10),
		})
	}
	writeCSV(w, "admin-users.csv", []string{"id", "username", "display_name", "email", "group", "quota", "used_quota", "status"}, rows)
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
	writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetUsers()))
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
			adminv1.AdminUpdateChannelRequest
		}
		if !decodeBody(w, r, &raw) {
			return
		}
		req := raw.AdminUpdateChannelRequest
		if req.ChannelId == 0 {
			req.ChannelId = raw.ID
		}
		resp, err := svc.UpdateChannel(r.Context(), &req)
		writeOneAPIServiceResponse(w, resp, err)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
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

	resp, err := svc.ListLogs(r.Context(), &adminv1.ListLogsRequest{
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
	writeJSON(w, http.StatusOK, resp)
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
	writeJSON(w, http.StatusNotImplemented, apiResponse(false, "log delete is not implemented", nil))
}

func handleOneAPIDeleteLogs(w http.ResponseWriter, r *http.Request) {
	if getQueryInt64(r, "end_time", 0) <= 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, "end_time is required", nil))
		return
	}
	logEndpoint := strings.TrimRight(os.Getenv("LOG_HTTP_ENDPOINT"), "/")
	serviceToken := os.Getenv("SERVICE_TOKEN")
	if logEndpoint == "" || serviceToken == "" {
		writeJSON(w, http.StatusNotImplemented, apiResponse(false, "log delete is not configured", nil))
		return
	}
	targetURL, err := url.Parse(logEndpoint + "/v1/logs")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, "invalid log service endpoint", nil))
		return
	}
	targetURL.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, targetURL.String(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse(false, "failed to create log delete request", nil))
		return
	}
	req.Header.Set("Authorization", "Bearer "+serviceToken)
	resp, err := http.DefaultClient.Do(req)
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

func handleOneAPIListLogs(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
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
	writeJSON(w, http.StatusOK, apiResponse(true, "", resp.GetLogs()))
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
		var req adminv1.CreateRedeemCodeRequest
		if !decodeBody(w, r, &req) {
			return
		}
		resp, err := svc.CreateRedeemCode(r.Context(), &req)
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

func writeServiceResponse(w http.ResponseWriter, resp interface{}, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
