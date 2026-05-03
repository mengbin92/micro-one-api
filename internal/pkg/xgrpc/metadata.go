package xgrpc

import (
	"context"

	"micro-one-api/internal/pkg/xtrace"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const traceIDKey = "x-trace-id"

// WithTraceID returns a new context with the trace ID propagated as gRPC metadata.
func WithTraceID(ctx context.Context) context.Context {
	traceID := xtrace.ExtractTraceID(ctx)
	if traceID == "" {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	}
	md = md.Copy()
	md.Set(traceIDKey, traceID)
	return metadata.NewOutgoingContext(ctx, md)
}

// TraceIDFromIncoming extracts the trace ID from incoming gRPC metadata and stores it in context.
func TraceIDFromIncoming(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	vals := md.Get(traceIDKey)
	if len(vals) > 0 && vals[0] != "" {
		return xtrace.WithTraceID(ctx, vals[0])
	}
	return ctx
}

// UnaryClientInterceptor returns a gRPC unary client interceptor that propagates trace IDs.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = WithTraceID(ctx)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that extracts trace IDs from metadata.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx = TraceIDFromIncoming(ctx)
		return handler(ctx, req)
	}
}
