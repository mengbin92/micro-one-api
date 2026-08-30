package xgrpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestValidateServiceToken(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want codes.Code
	}{
		{name: "missing server token", ctx: context.Background(), want: codes.PermissionDenied},
		{name: "missing metadata", ctx: context.Background(), want: codes.Unauthenticated},
		{name: "wrong token", ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer wrong")), want: codes.Unauthenticated},
		{name: "valid token", ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer secret")), want: codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverToken := "secret"
			if tt.name == "missing server token" {
				serverToken = ""
			}
			err := ValidateServiceToken(tt.ctx, serverToken)
			if got := status.Code(err); got != tt.want {
				t.Fatalf("status code = %s, want %s (err=%v)", got, tt.want, err)
			}
		})
	}
}
