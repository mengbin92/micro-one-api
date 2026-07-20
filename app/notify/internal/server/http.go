package server

import (
	"net/http"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"micro-one-api/app/notify/internal/service"
	"micro-one-api/platform/metrics"
	"micro-one-api/platform/http"
)

// NewHTTPServer wires HTTP transport for notify-worker.
func NewHTTPServer(addr string, svc *service.NotifyService) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)
	srv.HandleFunc("/v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			svc.HandleListNotifications(w, r)
		case http.MethodPost:
			svc.HandleCreateNotification(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
	srv.HandlePrefix("/v1/notifications/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc.HandleGetNotification(w, r)
	}))
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	return srv
}
