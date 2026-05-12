package server

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	adminv1 "micro-one-api/api/admin/v1"
	"micro-one-api/internal/admin/service"
	"micro-one-api/internal/pkg/metrics"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

//go:embed static/admin.html
var adminHTML string

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
func NewHTTPServer(addr string, svc *service.AdminService) *khttp.Server {
	srv := khttp.NewServer(
		khttp.Address(addr),
	)

	// Health and metrics (unauthenticated)
	srv.HandleFunc("/", handleAdminPage)
	srv.HandleFunc("/admin", handleAdminPage)
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

	// Protected admin endpoints
	srv.HandleFunc("/api/user/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUsers(w, r, svc)
	}))
	srv.HandlePrefix("/api/user/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIUserByID(w, r, svc)
	}))
	srv.HandleFunc("/v1/users", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUsers(w, r, svc)
	}))
	srv.HandlePrefix("/v1/users/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUserByID(w, r, svc)
	}))

	srv.HandleFunc("/api/channel/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannels(w, r, svc)
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
	srv.HandleFunc("/api/option/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
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
	srv.HandleFunc("/api/log/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPILogs(w, r, svc)
	}))
	srv.HandlePrefix("/api/log/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPILogByID(w, r, svc)
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
	srv.HandleFunc("/api/redemption/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodes(w, r, svc)
	}))
	srv.HandlePrefix("/api/redemption/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIRedemptionByCode(w, r, svc)
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
		handleChannelBalanceUnsupported(w, r)
	}))
	srv.HandlePrefix("/api/channel/update_balance/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleChannelBalanceUnsupported(w, r)
	}))
	srv.HandlePrefix("/api/channel/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleOneAPIChannelByID(w, r, svc)
	}))

	return srv
}

func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(adminHTML))
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
	switch r.Method {
	case http.MethodGet:
		if strings.Trim(r.URL.Path, "/") == "api/user/search" {
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

func handleOneAPIUserByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if strings.Trim(r.URL.Path, "/") == "api/user/search" {
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
	switch r.Method {
	case http.MethodGet:
		if strings.Trim(r.URL.Path, "/") == "api/channel/search" {
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
	if trimmed == "api/channel/search" || trimmed == "api/channel/models" {
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
	resp, err := svc.ListChannels(r.Context(), &adminv1.AdminListChannelsRequest{Page: 1, PageSize: 1000})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	models := map[int64][]string{}
	for _, channel := range resp.GetChannels() {
		var names []string
		for _, model := range strings.Split(channel.GetModels(), ",") {
			model = strings.TrimSpace(model)
			if model != "" {
				names = append(names, model)
			}
		}
		models[channel.GetId()] = names
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", models))
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

func handleChannelBalanceUnsupported(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
		"success": false,
		"message": "channel balance refresh requires provider-specific balance adapters",
	})
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
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if strings.Trim(r.URL.Path, "/") == "api/log/search" {
		handleOneAPISearchLogs(w, r, svc)
		return
	}
	handleOneAPIListLogs(w, r, svc)
}

func handleOneAPILogByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if strings.Trim(r.URL.Path, "/") == "api/log/search" {
		handleOneAPISearchLogs(w, r, svc)
		return
	}
	writeJSON(w, http.StatusNotImplemented, apiResponse(false, "log delete is not implemented", nil))
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
	if strings.Trim(r.URL.Path, "/") == "api/redemption/search" {
		handleRedeemCodes(w, r, svc)
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
