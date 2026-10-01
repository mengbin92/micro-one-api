package server

import (
	"micro-one-api/platform/authz"
	"net/http"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"micro-one-api/app/monitor/internal/service"
	"micro-one-api/platform/http"
	"micro-one-api/platform/metrics"
)

// NewHTTPServer wires HTTP transport for monitor-worker.
func NewHTTPServer(addr string, svc *service.MonitorService) *khttp.Server {
	srv := xhttp.NewServer(khttp.Address(addr))
	srv.HandleFunc("/v1/health-checks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authz.HTTPContext("/api.monitor.v1.MonitorService/ListHealthChecks", svc.HandleListHealthChecks)(w, r)
		case http.MethodPost:
			authz.HTTPContext("/api.monitor.v1.MonitorService/SaveHealthCheck", svc.HandleRecordHealthCheck)(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
	srv.HandleFunc("/v1/alert-rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authz.HTTPContext("/api.monitor.v1.MonitorService/ListAlertRules", svc.HandleListAlertRules)(w, r)
		case http.MethodPost:
			authz.HTTPContext("/api.monitor.v1.MonitorService/CreateAlertRule", svc.HandleCreateAlertRule)(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
	srv.HandlePrefix("/v1/alert-rules/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authz.HTTPContext("/api.monitor.v1.MonitorService/GetAlertRule", svc.HandleGetAlertRule)(w, r)
		case http.MethodPut:
			authz.HTTPContext("/api.monitor.v1.MonitorService/UpdateAlertRule", svc.HandleUpdateAlertRule)(w, r)
		case http.MethodDelete:
			authz.HTTPContext("/api.monitor.v1.MonitorService/DeleteAlertRule", svc.HandleDeleteAlertRule)(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
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
