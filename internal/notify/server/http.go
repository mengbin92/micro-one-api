package server

import (
	"net/http"

	khttp "github.com/go-kratos/kratos/v2/transport/http"

	"micro-one-api/internal/notify/service"
	"micro-one-api/internal/pkg/grpcgateway"
	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/pkg/xhttp"
)

// NewHTTPServer wires HTTP transport for notify-worker.
func NewHTTPServer(addr, grpcAddr string, svc *service.NotifyService) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(addr))...)

	gw := newGatewayHandler(grpcAddr)
	srv.HandlePrefix("/v1/notifications", gw)

	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
	srv.HandleFunc("/healthz", grpcgateway.HealthzHandler)
	return srv
}
