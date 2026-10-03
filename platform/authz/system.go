package authz

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

// RequireSystem checks the consuming handler as well as transport admission.
// A capability for a different RPC cannot authorize a system mutation. Local
// usecase work has no external transport marker and keeps its internal path.
func RequireSystem(ctx context.Context, resolver authorization.Resolver, point, method string) error {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, method) {
		return nil
	}
	modes, ok := resolver.(interface {
		Mode(context.Context, string) (string, error)
	})
	if !ok {
		return ErrUnavailable
	}
	mode, err := modes.Mode(ctx, point)
	if err != nil {
		return err
	}
	if mode == "legacy" {
		return nil
	}
	if mode != "iam" {
		return ErrUnavailable
	}
	return status.Error(codes.PermissionDenied, "dedicated system capability required")
}
