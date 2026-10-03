package server

import (
	"micro-one-api/platform/authz"
	"net/http"
	"strings"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"micro-one-api/app/notify/internal/service"
	"micro-one-api/platform/http"
	"micro-one-api/platform/metrics"
)

// NewHTTPServer wires HTTP transport for notify-worker.
func NewHTTPServer(addr string, svc *service.NotifyService) *khttp.Server {
	srv := xhttp.NewServer(khttp.Address(addr))
	srv.HandleFunc("/v1/alerts/alertmanager", authz.HTTPContext("/api.notify.v1.NotifyService/CreateNotification", svc.HandleAlertmanager))
	srv.HandleFunc("/v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authz.HTTPContext("/api.notify.v1.NotifyService/ListNotifications", svc.HandleListNotifications)(w, r)
		case http.MethodPost:
			authz.HTTPContext("/api.notify.v1.NotifyService/CreateNotification", svc.HandleCreateNotification)(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
	srv.HandlePrefix("/v1/notifications/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			authz.HTTPContext("/api.notify.v1.NotifyService/UpdateNotificationStatus", svc.HandleUpdateNotificationStatus)(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/acknowledge") {
			authz.HTTPContext("/api.notify.v1.NotifyService/AcknowledgeNotification", svc.HandleNotificationManagement)(w, r)
			return
		}
		authz.HTTPContext("/api.notify.v1.NotifyService/GetNotification", svc.HandleGetNotification)(w, r)
	}))
	srv.HandleFunc("/v1/notification-rules", authz.HTTPContext("/api.notify.v1.NotifyService/ListNotificationRules", svc.HandleNotificationManagement))
	srv.HandlePrefix("/v1/notification-rules/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := "/api.notify.v1.NotifyService/UpdateNotificationRule"
		if strings.HasSuffix(r.URL.Path, "/test") {
			method = "/api.notify.v1.NotifyService/TestNotificationRule"
		}
		authz.HTTPContext(method, svc.HandleNotificationManagement)(w, r)
	}))
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return srv
}
