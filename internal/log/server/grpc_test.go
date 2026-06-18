package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestValidateServiceToken(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer service-token"))
	if err := validateServiceToken(ctx, "service-token"); err != nil {
		t.Fatalf("validateServiceToken() error = %v", err)
	}

	err := validateServiceToken(context.Background(), "service-token")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing metadata code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer wrong-token"))
	err = validateServiceToken(ctx, "service-token")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer service-token"))
	err = validateServiceToken(ctx, "")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unconfigured token code = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}
