package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/authorization/management"
)

func (s *AdminService) AuthorizeConsole(ctx context.Context, raw string) (authorization.ResourceAuthorization, error) {
	if s == nil || s.iam == nil {
		return authorization.ResourceAuthorization{Mode: "legacy"}, nil
	}
	object := authorization.ObjectFacts{Context: authorization.Platform()}
	return s.iam.uc.ResourceAuthorization(ctx, raw, authorization.ResourceRequest{ExecutionPoint: "admin.console", Operation: "admin.console.enter", Object: &object})
}

// AuthenticateSelf verifies the session directly. Reading one's principal does
// not require console entry or the separate managed-user read permission.
func (s *AdminService) AuthenticateSelf(ctx context.Context, raw string) (int64, error) {
	if s.iam == nil {
		id, _, err := s.AuthorizeAdminToken(ctx, raw)
		return id, err
	}
	out, err := s.iam.uc.Execute(ctx, raw, "GetSessionAuthorization", management.Request{Context: authorization.Platform()})
	if err != nil {
		return 0, err
	}
	if out.Session == nil || out.Session.UserID <= 0 {
		return 0, status.Error(codes.Unauthenticated, "user session required")
	}
	return out.Session.UserID, nil
}
