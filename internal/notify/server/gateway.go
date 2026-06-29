package server

import (
	"net/http"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/internal/pkg/grpcgateway"
)

func newGatewayHandler(grpcAddr string) http.Handler {
	gw := grpcgateway.NewServeMux()
	grpcgateway.MustRegister(notifyv1.RegisterNotifyServiceHandlerFromEndpoint(
		grpcgateway.BackgroundContext(),
		gw,
		grpcgateway.EndpointForListenAddr(grpcAddr),
		grpcgateway.DialOptions(),
	))
	return gw
}
