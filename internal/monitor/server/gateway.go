package server

import (
	"net/http"

	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/internal/pkg/grpcgateway"
)

func newGatewayHandler(grpcAddr string) http.Handler {
	gw := grpcgateway.NewServeMux()
	grpcgateway.MustRegister(monitorv1.RegisterMonitorServiceHandlerFromEndpoint(
		grpcgateway.BackgroundContext(),
		gw,
		grpcgateway.EndpointForListenAddr(grpcAddr),
		grpcgateway.DialOptions(),
	))
	return gw
}
