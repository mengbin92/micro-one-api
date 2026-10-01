package server

import (
	"micro-one-api/platform/authz"
	"net/http"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	"micro-one-api/app/config/internal/service"
	"micro-one-api/platform/http"
	"micro-one-api/platform/metrics"
)

// NewHTTPServer wires HTTP transport for config-service.
func NewHTTPServer(addr string, svc *service.ConfigService) *khttp.Server {
	srv := xhttp.NewServer(khttp.Address(addr))
	srv.HandlePrefix("/v1/configs/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rest := r.URL.Path[len("/v1/configs/"):]
			if countSlashes(rest) >= 1 {
				authz.HTTPContext("/api.config.v1.ConfigService/GetConfig", svc.HandleGetConfig)(w, r)
			} else {
				authz.HTTPContext("/api.config.v1.ConfigService/ListConfigs", svc.HandleListConfigs)(w, r)
			}
		case http.MethodPut:
			authz.HTTPContext("/api.config.v1.ConfigService/SetConfig", svc.HandleSetConfig)(w, r)
		case http.MethodDelete:
			authz.HTTPContext("/api.config.v1.ConfigService/DeleteConfig", svc.HandleDeleteConfig)(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}))
	srv.HandleFunc("/api/notice", svc.HandleOneAPIContent("system", "notice", ""))
	srv.HandleFunc("/api/about", svc.HandleOneAPIContent("system", "about", ""))
	srv.HandleFunc("/api/home_page_content", svc.HandleOneAPIContent("system", "home_page_content", ""))
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

// countSlashes counts the number of '/' characters in s.
func countSlashes(s string) int {
	n := 0
	for _, c := range s {
		if c == '/' {
			n++
		}
	}
	return n
}
