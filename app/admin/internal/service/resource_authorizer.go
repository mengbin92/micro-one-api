package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/authorization/management"
)

type adminResourceAuthorizer struct{ service *AdminService }

// ResourceAuthorizer uses the existing dedicated admin-to-identity connection.
// Shared-domain consumers remain admin execution points; the actual remote
// owner still performs its own resource query before storage access.
func (s *AdminService) ResourceAuthorizer() authorization.Resolver { return adminResourceAuthorizer{s} }
func (r adminResourceAuthorizer) Query(ctx context.Context, point, operation, credential string) (authorization.ResourceAuthorization, error) {
	if r.service == nil || r.service.iam == nil || r.service.iam.uc == nil {
		if authorization.External(ctx) {
			return authorization.ResourceAuthorization{}, status.Error(codes.Unavailable, "resource authorization unavailable")
		}
		return authorization.ResourceAuthorization{Mode: "legacy"}, nil
	}
	if credential == "" {
		credential = operatorCredential(ctx)
	}
	out, err := r.service.iam.uc.ResourceAuthorization(ctx, credential, authorization.ResourceRequest{ExecutionPoint: point, Operation: operation})
	if err != nil {
		return out, err
	}
	if out.Mode != "legacy" && out.Mode != "iam" {
		return out, status.Error(codes.Unavailable, "authorization mode unavailable")
	}
	if out.Mode == "iam" && (out.Query.ActorID <= 0 || len(out.Query.Allow) == 0) {
		return out, status.Error(codes.PermissionDenied, "operation not permitted")
	}
	return out, nil
}
func (r adminResourceAuthorizer) OptionalQuery(ctx context.Context, point, operation, credential string) (authorization.ResourceAuthorization, error) {
	out, err := r.Query(ctx, point, operation, credential)
	if status.Code(err) == codes.PermissionDenied {
		actor, mode, actorErr := r.ResolveActor(ctx, point, credential)
		if actorErr != nil {
			return authorization.ResourceAuthorization{Mode: mode}, actorErr
		}
		if mode != "iam" || actor.UserID <= 0 {
			return authorization.ResourceAuthorization{Mode: mode}, status.Error(codes.PermissionDenied, "verified actor required")
		}
		return authorization.ResourceAuthorization{Mode: mode, Query: authorization.QueryScope{ActorID: actor.UserID, ValidUntil: actor.ExpiresAt}}, nil
	}
	return out, err
}

// AuthorizeSection preflights a fixed section before any business task starts.
// global is required for sources that cannot express a resource predicate.
func (s *AdminService) AuthorizeSection(ctx context.Context, point string, global bool, operations ...string) error {
	for _, operation := range operations {
		out, err := s.ResourceAuthorizer().Query(ctx, point, operation, operatorCredential(ctx))
		if err != nil {
			return err
		}
		if out.Mode == "iam" && (global && !out.Query.Global()) {
			return status.Error(codes.PermissionDenied, "section requires global authorization")
		}
	}
	return nil
}

func (r adminResourceAuthorizer) Mode(ctx context.Context, point string) (string, error) {
	if r.service == nil || r.service.iam == nil || r.service.iam.uc == nil {
		if authorization.External(ctx) {
			return "", status.Error(codes.Unavailable, "resource authorization unavailable")
		}
		return "legacy", nil
	}
	mode, err := r.service.iam.uc.ResourceMode(ctx, point)
	if err == nil && mode != "legacy" && mode != "iam" {
		err = status.Error(codes.Unavailable, "authorization mode unavailable")
	}
	return mode, err
}
func (r adminResourceAuthorizer) ResolveActor(ctx context.Context, point, credential string) (authorization.Actor, string, error) {
	mode, err := r.Mode(ctx, point)
	if err != nil || mode != "iam" {
		return authorization.Actor{}, mode, err
	}
	if credential == "" {
		credential = operatorCredential(ctx)
	}
	out, err := r.service.iam.uc.Execute(ctx, credential, "GetSessionAuthorization", management.Request{Context: authorization.Platform()})
	if err != nil {
		return authorization.Actor{}, mode, err
	}
	if out.Session == nil || out.Session.UserID <= 0 || out.Session.SessionID == "" || out.Session.ExpiresAt.IsZero() {
		return authorization.Actor{}, mode, status.Error(codes.Unauthenticated, "user session required")
	}
	return authorization.Actor{UserID: out.Session.UserID, SessionID: out.Session.SessionID, ExpiresAt: out.Session.ExpiresAt}, mode, nil
}
