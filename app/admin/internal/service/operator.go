package service

import (
	"context"
	"google.golang.org/grpc/metadata"
)

// Replace outgoing metadata instead of appending: a client-supplied duplicate
// or caller/actor hint cannot shadow the authenticated operator.
func operatorRPCContext(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	if credential := operatorCredential(ctx); credential != "" {
		md.Set("x-operator-authorization", "Bearer "+credential)
	}
	return metadata.NewOutgoingContext(ctx, md)
}
