package xgrpc

import (
	"context"
	"crypto/subtle"
	"micro-one-api/platform/security/serviceidentity"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ValidateServiceToken requires authorization: Bearer <SERVICE_TOKEN> and
// fails closed when the server-side token is not configured.
func ValidateServiceToken(ctx context.Context, serviceToken string) error {
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

// ServiceTokenUnaryInterceptor authenticates dedicated opaque callers and
// the legacy shared compatibility principal against fixed full-method policies.
func ServiceTokenUnaryInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return ServiceIdentityUnaryInterceptor(serviceidentity.FromEnvironment(serviceToken))
}

func ServiceIdentityUnaryInterceptor(verifier *serviceidentity.Verifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := serviceidentity.Lookup(info.FullMethod); !ok {
			return nil, status.Error(codes.PermissionDenied, "unclassified RPC")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			return nil, status.Error(codes.Unauthenticated, "service credential required")
		}
		principal, err := verifier.Authenticate(strings.TrimPrefix(values[0], "Bearer "))
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid service credential")
		}
		if principal.Dedicated && !principal.CanCall(info.FullMethod) {
			return nil, status.Error(codes.PermissionDenied, "caller capability denied")
		}
		ctx = serviceidentity.WithPrincipal(ctx, principal)
		return handler(ctx, req)
	}
}

// No service currently declares a streaming capability. Streaming additions
// must supply a separate fixed policy before an implementation is reachable.
func ServiceTokenStreamInterceptor(serviceToken string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := ValidateServiceToken(ss.Context(), serviceToken); err != nil {
			return err
		}
		return status.Error(codes.PermissionDenied, "stream capability not registered")
	}
}
