package service

import (
	"context"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"micro-one-api/domain/authorization"
)

// Fixed handlers select the resource and identifier. Protobuf fields only carry
// client preconditions; no stored revision is promoted into the caller's CAS.
func ownerWriteContext(ctx context.Context, req proto.Message, resource string, id int64, revisionField string) context.Context {
	if req == nil {
		return ctx
	}
	message := req.ProtoReflect()
	if !message.IsValid() {
		return ctx
	}
	fields := message.Descriptor().Fields()
	if reason := fields.ByName("reason"); reason != nil {
		if value := message.Get(reason).String(); value != "" {
			ctx = authorization.WithWriteReason(ctx, value)
		}
	}
	if revision := fields.ByName(protoreflect.Name(revisionField)); revision != nil {
		ctx = authorization.WithExpectedRevision(ctx, resource, id, message.Get(revision).Int())
	}
	return ctx
}
