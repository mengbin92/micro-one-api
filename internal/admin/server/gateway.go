package server

import (
	"net/http"

	adminv1 "micro-one-api/api/admin/v1"
	"micro-one-api/internal/pkg/grpcgateway"
)

func newGatewayHandler(grpcAddr string) http.Handler {
	gw := grpcgateway.NewServeMux()
	grpcgateway.MustRegister(adminv1.RegisterAdminServiceHandlerFromEndpoint(
		grpcgateway.BackgroundContext(),
		gw,
		grpcgateway.EndpointForListenAddr(grpcAddr),
		grpcgateway.DialOptions(),
	))
	return gw
}
