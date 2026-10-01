package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/iamdto"
	"micro-one-api/platform/security/serviceidentity"
)

func (s *IAMService) GetResourceAuthorization(ctx context.Context, p *v.ResourceAuthorizationRequest) (*v.ResourceAuthorizationReply, error) {
	if p == nil || !isServiceAuthenticated(ctx) {
		return nil, status.Error(codes.Unauthenticated, "service credential required")
	}
	point, ok := authorization.Execution(p.ExecutionPoint)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "unknown execution point")
	}
	mode, err := s.identity.AuthorizationMode(ctx)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	principal := serviceidentity.FromContext(ctx)
	if mode == "iam" && (!principal.Dedicated || principal.Name != point.Owner) {
		return nil, status.Error(codes.PermissionDenied, "verified data owner required")
	}
	if p.ModeOnly {
		if p.Operation != "" || p.Object != nil {
			return nil, status.Error(codes.InvalidArgument, "mode probe cannot request authority")
		}
		return &v.ResourceAuthorizationReply{AuthorizationMode: mode}, nil
	}
	credential, system := operatorCredential(ctx)
	if mode == "iam" && (credential == "" || system) {
		return nil, status.Error(codes.Unauthenticated, "user operator required")
	}
	req := authorization.ResourceRequest{ExecutionPoint: p.ExecutionPoint, Operation: p.Operation}
	if p.Object != nil {
		object := iamdto.AuthorizationObjectFactsFrom(p.Object)
		req.Object = &object
	}
	out, err := s.identity.GetResourceAuthorization(ctx, credential, req)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	reply := &v.ResourceAuthorizationReply{AuthorizationMode: out.Mode, ActorUserId: out.Query.ActorID, Versions: iamdto.AuthorizationVersionsTo(out.Query.Versions)}
	for _, scope := range out.Query.Allow {
		reply.Allow = append(reply.Allow, iamdto.AuthorizationScopeTo(scope))
	}
	for _, scope := range out.Query.Deny {
		reply.Deny = append(reply.Deny, iamdto.AuthorizationScopeTo(scope))
	}
	if !out.Query.ValidUntil.IsZero() {
		reply.ValidUntil = timestamppb.New(out.Query.ValidUntil)
	}
	if out.Decision != nil {
		reply.Decision = iamdto.AuthorizationDecisionTo(*out.Decision)
	}
	return reply, nil
}
