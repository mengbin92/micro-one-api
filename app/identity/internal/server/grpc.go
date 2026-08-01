package server

import (
	"context"
	"crypto/subtle"
	"os"
	"strings"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/service"
	apptimeout "micro-one-api/pkg/timeout"

	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NewGRPCServer wires gRPC transport for identity-service.
//
// H1 hardening: the previously-unauthenticated gRPC server exposed every
// Identity RPC (CreateUser, SetUserRole, CreateAccessToken, DeleteUser,
// ListUsers) to any network-reachable caller. We now require a service
// token in the `authorization: Bearer <SERVICE_TOKEN>` metadata, validated
// with a constant-time compare against the shared SERVICE_TOKEN env var
// (the same scheme the billing HTTP layer uses). When SERVICE_TOKEN is
// unset the server fails closed (every RPC is denied) rather than running
// open — this is deliberate to force operators to configure the shared
// secret before exposing the gRPC port.
//
// On successful service-token validation the interceptor stamps the context
// and forwards the separate operator credential. SetUserRole validates that
// credential as either the identified user's session or ADMIN_TOKEN; the
// request's operator_user_id never authenticates the caller by itself.
func NewGRPCServer(addr string, svc *service.IdentityService) *kgrpc.Server {
	serviceToken := os.Getenv("SERVICE_TOKEN")
	srv := kgrpc.NewServer(
		kgrpc.Address(addr),
		kgrpc.Timeout(apptimeout.GetGRPCTimeout()),
		kgrpc.UnaryInterceptor(serviceTokenUnaryInterceptor(serviceToken)),
		kgrpc.StreamInterceptor(serviceTokenStreamInterceptor(serviceToken)),
	)
	identityv1.RegisterIdentityServiceServer(srv, svc)
	return srv
}

func serviceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := validateServiceToken(ctx, serviceToken); err != nil {
			return nil, err
		}
		return handler(operatorCredentialContext(service.ServiceAuthenticatedContext(ctx)), req)
	}
}

func serviceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := validateServiceToken(ss.Context(), serviceToken); err != nil {
			return err
		}
		return handler(srv, &serviceAuthenticatedStream{ServerStream: ss})
	}
}

// serviceAuthenticatedStream wraps a gRPC ServerStream so the context
// carries the service-authenticated marker (serviceCallerKey) for stream
// RPCs, mirroring the unary interceptor.
type serviceAuthenticatedStream struct {
	grpc.ServerStream
}

func (s *serviceAuthenticatedStream) Context() context.Context {
	return operatorCredentialContext(service.ServiceAuthenticatedContext(s.ServerStream.Context()))
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
	if len(values) == 0 || !strings.HasPrefix(values[0], "Bearer ") {
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
	if len(values) == 0 {
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
