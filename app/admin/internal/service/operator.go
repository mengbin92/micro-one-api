package service

import (
	"context"
	"google.golang.org/grpc/metadata"
	"micro-one-api/domain/authorization"
	"strings"
)

// Replace outgoing metadata instead of appending: a client-supplied duplicate
// or caller/actor hint cannot shadow the authenticated operator.
func operatorRPCContext(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	md.Delete("x-authorization-reason")
	md.Delete("x-authorization-reason-bin")
	if reason := authorization.WriteReason(ctx); reason != "" {
		// Keep printable ASCII compatible with older receivers during rollout.
		key := "x-authorization-reason"
		if strings.ContainsFunc(reason, func(r rune) bool { return r < ' ' || r > '~' }) {
			key = "x-authorization-reason-bin"
		}
		md.Set(key, reason)
	}
	if credential := operatorCredential(ctx); credential != "" {
		md.Set("x-operator-authorization", "Bearer "+credential)
	}
	return metadata.NewOutgoingContext(ctx, md)
}
