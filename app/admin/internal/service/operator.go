package service

import (
	"context"
	"google.golang.org/grpc/metadata"
	"micro-one-api/domain/authorization"
)

// Replace outgoing metadata instead of appending: a client-supplied duplicate
// or caller/actor hint cannot shadow the authenticated operator.
func operatorRPCContext(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	md.Delete("x-authorization-reason")
	if reason := authorization.WriteReason(ctx); reason != "" {
		md.Set("x-authorization-reason", reason)
	}
	if credential := operatorCredential(ctx); credential != "" {
		md.Set("x-operator-authorization", "Bearer "+credential)
	}
	return metadata.NewOutgoingContext(ctx, md)
}
