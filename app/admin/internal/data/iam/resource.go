package iam

import (
	"context"
	"google.golang.org/grpc/metadata"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/iamdto"
)

func (r *repo) ResourceAuthorization(ctx context.Context, raw string, req authorization.ResourceRequest) (authorization.ResourceAuthorization, error) {
	if r.client == nil {
		return authorization.ResourceAuthorization{}, biz.ErrIAMResourceUnavailable
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	if raw != "" {
		md.Set("x-operator-authorization", "Bearer "+raw)
	}
	p := &v.ResourceAuthorizationRequest{ExecutionPoint: req.ExecutionPoint, Operation: req.Operation}
	if req.Object != nil {
		p.Object = iamdto.AuthorizationObjectFactsTo(*req.Object)
	}
	reply, err := r.client.GetResourceAuthorization(metadata.NewOutgoingContext(ctx, md), p)
	if err != nil {
		return authorization.ResourceAuthorization{}, err
	}
	out := authorization.ResourceAuthorization{Mode: reply.AuthorizationMode, Query: authorization.QueryScope{ActorID: reply.ActorUserId, Versions: iamdto.AuthorizationVersionsFrom(reply.Versions)}}
	for _, scope := range reply.Allow {
		out.Query.Allow = append(out.Query.Allow, iamdto.AuthorizationScopeFrom(scope))
	}
	for _, scope := range reply.Deny {
		out.Query.Deny = append(out.Query.Deny, iamdto.AuthorizationScopeFrom(scope))
	}
	if reply.ValidUntil != nil {
		out.Query.ValidUntil = reply.ValidUntil.AsTime()
	}
	if reply.Decision != nil {
		d := iamdto.AuthorizationDecisionFrom(reply.Decision)
		out.Decision = &d
	}
	return out, nil
}

func (r *repo) ResourceMode(ctx context.Context, point string) (string, error) {
	if r.client == nil {
		return "", biz.ErrIAMResourceUnavailable
	}
	reply, err := r.client.GetResourceAuthorization(ctx, &v.ResourceAuthorizationRequest{ExecutionPoint: point, ModeOnly: true})
	if err != nil {
		return "", err
	}
	if reply == nil || (reply.AuthorizationMode != "legacy" && reply.AuthorizationMode != "iam") {
		return "", biz.ErrIAMResourceUnavailable
	}
	return reply.AuthorizationMode, nil
}
