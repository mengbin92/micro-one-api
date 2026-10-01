package server

import (
	"micro-one-api/platform/authz"
	"os"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for channel-service.
func NewGRPCServer(addr string, svc *service.ChannelService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		// Metrics outermost: server-side handling latency (incl. auth) is the
		// discriminating signal when client-side dependency latency moves
		// (O5 attribution: relay-side contention vs downstream slowdown).
		kgrpc.UnaryInterceptor(
			xgrpc.MetricsUnaryServerInterceptor("channel-service"),
			xgrpc.ServiceTokenUnaryInterceptor(os.Getenv("SERVICE_TOKEN")), authz.OperatorUnaryInterceptor(),
			authz.CoverageUnaryInterceptor(svc.OwnerAuthorizationClient(), "channel.channels.list", channelReadyMethods),
		),
		kgrpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(os.Getenv("SERVICE_TOKEN"))),
	)
	channelv1.RegisterChannelServiceServer(srv, svc)
	return srv
}

var channelReadyMethods = func() []string {
	out := []string{}
	for _, method := range []string{"GetChannel", "ListChannels", "CreateChannel", "UpdateChannel", "DeleteChannel", "ChangeChannelStatus", "GetSubscriptionAccount", "ListSubscriptionAccounts", "CreateSubscriptionAccount", "UpdateSubscriptionAccount", "DeleteSubscriptionAccount", "ChangeSubscriptionAccountStatus", "ResetSubscriptionAccountQuota", "ClearSubscriptionAccountError", "ListRoutingGroups", "GetRoutingGroup", "CreateRoutingGroup", "SetRoutingGroupState", "SetRoutingGroupResourceOverrides"} {
		out = append(out, "/api.channel.v1.ChannelService/"+method)
	}
	return out
}()
