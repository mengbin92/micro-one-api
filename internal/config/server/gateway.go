package server

import (
	"net/http"

	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/internal/pkg/grpcgateway"
)

func newGatewayHandler(grpcAddr string) http.Handler {
	gw := grpcgateway.NewServeMux()
	grpcgateway.MustRegister(configv1.RegisterConfigServiceHandlerFromEndpoint(
		grpcgateway.BackgroundContext(),
		gw,
		grpcgateway.EndpointForListenAddr(grpcAddr),
		grpcgateway.DialOptions(),
	))
	return gw
}
