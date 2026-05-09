package server

import (
	"crypto/subtle"
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
	srv.HandleFunc("/v1/users", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUsers(w, r, svc)
	}))
	srv.HandlePrefix("/v1/users/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleUserByID(w, r, svc)
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

	srv.HandleFunc("/v1/logs", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleListLogs(w, r, svc)
	}))

	srv.HandleFunc("/v1/account", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleGetAccount(w, r, svc)
	}))

	srv.HandleFunc("/v1/redeem-codes", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodes(w, r, svc)
	}))
	srv.HandlePrefix("/v1/redeem-codes/", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleRedeemCodeByCode(w, r, svc)
	}))
	srv.HandleFunc("/v1/topup", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTopUp(w, r, svc)
	}))
	srv.HandleFunc("/api/topup", AdminAuth(func(w http.ResponseWriter, r *http.Request) {
		handleTopUp(w, r, svc)
	}))

	return srv
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

func handleUserByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
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

func writeServiceResponse(w http.ResponseWriter, resp interface{}, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
