package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/platform/security/serviceidentity"
)

func (s *IdentityService) managedIdentityContext(ctx context.Context) (context.Context, error) {
	mode, err := s.uc.AuthorizationMode(ctx)
	if err != nil {
		return ctx, mapIdentityErrorToGRPC(err)
	}
	if mode == "legacy" {
		return ctx, nil
	}
	p := serviceidentity.FromContext(ctx)
	if !p.Dedicated || p.Name != "admin" {
		return ctx, status.Error(codes.PermissionDenied, "dedicated admin service required")
	}
	raw, system := operatorCredential(ctx)
	if raw == "" || system {
		return ctx, status.Error(codes.Unauthenticated, "user operator required")
	}
	return ctx, nil
}
