package server

import (
	"context"
	"testing"

	"micro-one-api/platform/grpc/xgrpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestLogGRPCServiceTokenValidation(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer service-token"))
	if err := xgrpc.ValidateServiceToken(ctx, "service-token"); err != nil {
		t.Fatalf("ValidateServiceToken() error = %v", err)
	}

	err := xgrpc.ValidateServiceToken(context.Background(), "service-token")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing metadata code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer wrong-token"))
	err = xgrpc.ValidateServiceToken(ctx, "service-token")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer service-token"))
	err = xgrpc.ValidateServiceToken(ctx, "")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unconfigured token code = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}
