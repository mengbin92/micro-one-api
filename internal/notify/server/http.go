package server

import (
	"net/http"

	khttp "github.com/go-kratos/kratos/v2/transport/http"

	"micro-one-api/internal/notify/service"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/pkg/xhttp"
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
	srv.HandleFunc("/v1/notifications/", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleGetNotification(w, r)
	})
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
