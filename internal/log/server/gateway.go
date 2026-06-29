package server

import (
	"net/http"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/internal/pkg/grpcgateway"
)

func newGatewayHandler(grpcAddr string) http.Handler {
	gw := grpcgateway.NewServeMux()
	grpcgateway.MustRegister(logv1.RegisterLogServiceHandlerFromEndpoint(
		grpcgateway.BackgroundContext(),
		gw,
		grpcgateway.EndpointForListenAddr(grpcAddr),
		grpcgateway.DialOptions(),
	))
	return gw
}
