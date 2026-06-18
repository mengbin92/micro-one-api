package server

import (
	"net/http"

	khttp "github.com/go-kratos/kratos/v2/transport/http"

	"micro-one-api/internal/config/service"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/pkg/xhttp"
)

// NewHTTPServer wires HTTP transport for config-service.
func NewHTTPServer(addr string, svc *service.ConfigService) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)
	srv.HandleFunc("/v1/configs/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rest := r.URL.Path[len("/v1/configs/"):]
			if countSlashes(rest) >= 1 {
				svc.HandleGetConfig(w, r)
			} else {
				svc.HandleListConfigs(w, r)
			}
		case http.MethodPut:
			svc.HandleSetConfig(w, r)
		case http.MethodDelete:
			svc.HandleDeleteConfig(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
	srv.HandleFunc("/api/notice", svc.HandleOneAPIContent("system", "notice", ""))
	srv.HandleFunc("/api/about", svc.HandleOneAPIContent("system", "about", ""))
	srv.HandleFunc("/api/home_page_content", svc.HandleOneAPIContent("system", "home_page_content", ""))
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
