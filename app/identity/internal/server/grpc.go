package server

import (
	"context"
	"crypto/subtle"
	"os"
	"strings"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	"micro-one-api/platform/grpc/xgrpc"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NewGRPCServer authenticates independent opaque service credentials and fixed
// full-method caller policies. The shared token remains a legacy compatibility
// principal; IAM user operations additionally verify the operator JWT/JTI.
// Root rescue uses its separate, explicitly enabled credential path.
func NewGRPCServer(addr string, svc *service.IdentityService, iam ...*service.IAMService) *kgrpc.Server {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		// Metrics outermost: server-side handling latency (incl. auth) is the
		// discriminating signal when client-side dependency latency moves
		// (O5 attribution: relay-side contention vs downstream slowdown).
		kgrpc.UnaryInterceptor(
			xgrpc.MetricsUnaryServerInterceptor("identity-service"),
			serviceTokenUnaryInterceptor(serviceToken),
		),
		kgrpc.StreamInterceptor(serviceTokenStreamInterceptor(serviceToken)),
	)
	identityv1.RegisterIdentityServiceServer(srv, svc)
	if len(iam) > 0 && iam[0] != nil {
		identityv1.RegisterIAMServiceServer(srv, iam[0])
	}
	return srv
}

func serviceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	identityAuth := xgrpc.ServiceTokenUnaryInterceptor(serviceToken)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == identityv1.IAMService_RescueRootCredential_FullMethodName {
			if err := validateServiceToken(ctx, os.Getenv("IAM_RESCUE_SERVICE_TOKEN")); err != nil {
				return nil, err
			}
			return handler(operatorCredentialContext(service.RescueAuthenticatedContext(ctx)), req)
		}
		return identityAuth(ctx, req, info, func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			if len(md.Get("x-operator-authorization")) > 1 {
				return nil, status.Error(codes.Unauthenticated, "ambiguous operator credential")
			}
			return handler(operatorCredentialContext(service.ServiceAuthenticatedContext(ctx)), req)
		})
	}
}

func serviceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return xgrpc.ServiceTokenStreamInterceptor(serviceToken)
}

func validateServiceToken(ctx context.Context, serviceToken string) error {
	// Fail closed when the shared secret is not configured. This prevents
	// an accidental open port from re-introducing the H1 exposure.
	if serviceToken == "" {
		return status.Error(codes.PermissionDenied, "service token not configured")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return status.Error(codes.Unauthenticated, "missing or invalid authorization header")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(serviceToken)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid service token")
	}
	return nil
}

func operatorCredentialContext(ctx context.Context) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("x-operator-authorization")
	if len(values) != 1 {
		return ctx
	}
	credential := strings.TrimSpace(values[0])
	credential = strings.TrimPrefix(credential, "Bearer ")
	if credential == "" {
		return ctx
	}
	system := os.Getenv("ADMIN_TOKEN") != "" &&
		subtle.ConstantTimeCompare([]byte(credential), []byte(os.Getenv("ADMIN_TOKEN"))) == 1
	return service.WithOperatorCredential(ctx, credential, system)
}
