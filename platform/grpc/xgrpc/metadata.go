package xgrpc

import (
	"context"
	"time"

	"micro-one-api/platform/metrics"
	"micro-one-api/platform/tracing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = WithTraceID(ctx)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that extracts trace IDs from metadata.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = TraceIDFromIncoming(ctx)
		return handler(ctx, req)
	}
}

// MetricsUnaryServerInterceptor returns a gRPC unary server interceptor that records Prometheus metrics.
func MetricsUnaryServerInterceptor(service string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start).Seconds()

		code := status.Code(err).String()
		metrics.GRPCRequestTotal.WithLabelValues(service, info.FullMethod, code).Inc()
		metrics.GRPCRequestDuration.WithLabelValues(service, info.FullMethod).Observe(duration)

		return resp, err
	}
}
