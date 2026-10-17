package xdb

import (
	"context"
	"errors"
	"net"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

type tracingPlugin struct{}
type dbSpanState struct {
	parent context.Context
	span   trace.Span
}

func (tracingPlugin) Name() string { return "safe-operation-tracing" }
func (tracingPlugin) Initialize(db *gorm.DB) error {
	before := func(operation string) func(*gorm.DB) {
		return func(db *gorm.DB) {
			parent := db.Statement.Context
			if parent == nil {
				parent = context.Background()
			}
			ctx, span := otel.Tracer("micro-one-api/storage").Start(parent, "db."+operation, trace.WithAttributes(attribute.String("db.operation.name", operation)))
			db.InstanceSet("safe.trace."+operation, dbSpanState{parent, span})
			db.Statement.Context = ctx
		}
	}
	after := func(operation string) func(*gorm.DB) {
		return func(db *gorm.DB) {
			value, ok := db.InstanceGet("safe.trace." + operation)
			if !ok {
				return
			}
			state := value.(dbSpanState)
			if db.Error != nil && !errors.Is(db.Error, gorm.ErrRecordNotFound) {
				state.span.SetStatus(codes.Error, "storage_error")
			}
			state.span.End()
			db.Statement.Context = state.parent
		}
	}
	registrations := []func() error{
		func() error { return db.Callback().Query().Before("*").Register("safe.trace.before", before("query")) }, func() error { return db.Callback().Query().After("*").Register("safe.trace.after", after("query")) },
		func() error {
			return db.Callback().Create().Before("*").Register("safe.trace.before", before("insert"))
		}, func() error { return db.Callback().Create().After("*").Register("safe.trace.after", after("insert")) },
		func() error {
			return db.Callback().Update().Before("*").Register("safe.trace.before", before("update"))
		}, func() error { return db.Callback().Update().After("*").Register("safe.trace.after", after("update")) },
		func() error {
			return db.Callback().Delete().Before("*").Register("safe.trace.before", before("delete"))
		}, func() error { return db.Callback().Delete().After("*").Register("safe.trace.after", after("delete")) },
		func() error { return db.Callback().Raw().Before("*").Register("safe.trace.before", before("raw")) }, func() error { return db.Callback().Raw().After("*").Register("safe.trace.after", after("raw")) },
		func() error { return db.Callback().Row().Before("*").Register("safe.trace.before", before("row")) }, func() error { return db.Callback().Row().After("*").Register("safe.trace.after", after("row")) },
	}
	for _, register := range registrations {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}

type redisTraceHook struct{}

func (redisTraceHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (redisTraceHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, span := otel.Tracer("micro-one-api/storage").Start(ctx, "cache.redis")
		operation := cmd.Name()
		if len(operation) > 32 {
			operation = "other"
		}
		span.SetAttributes(attribute.String("db.operation.name", operation))
		err := next(ctx, cmd)
		if operation == "get" || operation == "hget" {
			span.SetAttributes(attribute.Bool("cache.hit", err == nil))
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			span.SetStatus(codes.Error, "cache_error")
		}
		span.End()
		return err
	}
}
func (redisTraceHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		ctx, span := otel.Tracer("micro-one-api/storage").Start(ctx, "cache.pipeline", trace.WithAttributes(attribute.Int("cache.commands", len(commands))))
		err := next(ctx, commands)
		if err != nil && !errors.Is(err, redis.Nil) {
			span.SetStatus(codes.Error, "cache_error")
		}
		span.End()
		return err
	}
}
