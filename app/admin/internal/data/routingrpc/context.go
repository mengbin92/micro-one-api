// Package routingrpc adapts internal routing reference reads to dedicated
// service capabilities. User access facts and writes retain their operator.
package routingrpc

import (
	"context"
	"google.golang.org/grpc/metadata"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
)

func ReferenceContext(ctx context.Context) context.Context {
	if !biz.IsRoutingReferenceRead(ctx) {
		return ctx
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	md.Delete("x-authorization-reason")
	return metadata.NewOutgoingContext(authorization.WithCredential(ctx, ""), md)
}
