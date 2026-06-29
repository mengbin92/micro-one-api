package server

import (
	"net/http"

	khttp "github.com/go-kratos/kratos/v2/transport/http"

	"micro-one-api/internal/config/service"
	"micro-one-api/internal/pkg/grpcgateway"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/pkg/xhttp"
)

// NewHTTPServer wires HTTP transport for config-service.
func NewHTTPServer(addr, grpcAddr string, svc *service.ConfigService) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)

	gw := newGatewayHandler(grpcAddr)
	srv.HandlePrefix("/v1/configs", gw)

	srv.HandleFunc("/api/notice", svc.HandleOneAPIContent("system", "notice", ""))
	srv.HandleFunc("/api/about", svc.HandleOneAPIContent("system", "about", ""))
	srv.HandleFunc("/api/home_page_content", svc.HandleOneAPIContent("system", "home_page_content", ""))
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", grpcgateway.HealthzHandler)
	return srv
}
