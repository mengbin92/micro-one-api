package server

import (
	"context"
	"crypto/subtle"
	"os"
	"strings"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NewGRPCServer wires gRPC transport for billing-service.
//
// M4 hardening: the gRPC port previously exposed every money-moving RPC
// (TopUpQuota, PurchaseSubscription, RefundPaymentOrder, CreateRedeemCode,
// MarkPaymentOrderPaid) without authentication, while the HTTP
// reconciliation endpoint was already protected by ServiceAuth. We now
// require the same `authorization: Bearer <SERVICE_TOKEN>` metadata used
// by the HTTP layer, validated with a constant-time compare. The server
// fails closed when SERVICE_TOKEN is unset.
func NewGRPCServer(addr string, svc *service.BillingService) *kgrpc.Server {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(serviceTokenUnaryInterceptor(serviceToken)),
		kgrpc.StreamInterceptor(serviceTokenStreamInterceptor(serviceToken)),
	)
	billingv1.RegisterBillingServiceServer(srv, svc)
	return srv
}

func serviceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := validateServiceToken(ctx, serviceToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func serviceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := validateServiceToken(ss.Context(), serviceToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func validateServiceToken(ctx context.Context, serviceToken string) error {
	if serviceToken == "" {
		return status.Error(codes.PermissionDenied, "service token not configured")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 || !strings.HasPrefix(values[0], "Bearer ") {
		return status.Error(codes.Unauthenticated, "missing or invalid authorization header")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(serviceToken)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid service token")
	}
	return nil
}
