package server

import (
	"micro-one-api/platform/authz"
	"os"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/notify/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer wires gRPC transport for notify-worker.
func NewGRPCServer(addr string, svc *service.NotifyService) *kgrpc.Server {
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(os.Getenv("SERVICE_TOKEN")), authz.OperatorUnaryInterceptor(), authz.CoverageUnaryInterceptor(svc.OwnerAuthorizationClient(), "notify.notifications", notifyReadyMethods)),
		kgrpc.StreamInterceptor(xgrpc.ServiceTokenStreamInterceptor(os.Getenv("SERVICE_TOKEN"))),
	)
	notifyv1.RegisterNotifyServiceServer(srv, svc)
	return srv
}

var notifyReadyMethods = []string{
	"/api.notify.v1.NotifyService/AcknowledgeNotification",
	"/api.notify.v1.NotifyService/ListNotificationRules",
	"/api.notify.v1.NotifyService/UpdateNotificationRule",
	"/api.notify.v1.NotifyService/TestNotificationRule",
	"/api.notify.v1.NotifyService/GetNotification",
	"/api.notify.v1.NotifyService/ListNotifications",
}
