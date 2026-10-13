package server

import (
	"context"
	"os"
	"runtime/debug"
	"sync"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"

	"go.uber.org/zap"
)

type nativeShadowKey struct{}
type nativeShadowAttempt struct {
	mu                       sync.Mutex
	last                     evidence.Record
	saw                      bool
	fields                   []zap.Field
	requestID, source, model string
	sourceID                 int64
	previous                 evidence.Recorder
}

var nativeShadowBuildID = func() string {
	if evidence.BuildRevision != "unknown" {
		return evidence.BuildRevision
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return "git:" + setting.Value
			}
		}
	}
	return "unknown"
}()

// P0 is opt-in observation: this context never changes the billing envelope,
// routing, retry policy, or user/provider price snapshots.
func withNativeUsageShadow(ctx context.Context, channel *relaybiz.Channel, requestID, model string) context.Context {
	if os.Getenv("RELAY_USAGE_SHADOW") != "true" {
		return ctx
	}
	trace := requesttrace.FromContext(ctx)
	source, id := "unknown", int64(0)
	if channel != nil {
		source, id = relaybiz.UpstreamSourceChannel, channel.ID
		if channel.SubscriptionAccountID > 0 {
			source, id = relaybiz.UpstreamSourceSubscription, channel.SubscriptionAccountID
		}
	}
	previous := evidence.RecorderFromContext(ctx)
	if old, ok := ctx.Value(nativeShadowKey{}).(*nativeShadowAttempt); ok {
		if (requestID == "" || old.requestID == requestID) && old.source == source && old.sourceID == id && old.model == model {
			return ctx
		}
		previous = old.previous
	}
	session := &nativeShadowAttempt{requestID: requestID, source: source, sourceID: id, model: model, previous: previous, fields: []zap.Field{
		zap.String("request_id", requestID), zap.String("root_request_id", trace.RootRequestID), zap.Int32("attempt_number", trace.Number),
		zap.String("source_kind", source), zap.Int64("source_id", id), zap.String("upstream_model_id", model),
		zap.String("build_revision", nativeShadowBuildID), zap.String("parser_revision", evidence.ParserRevision),
		zap.String("cost_basis", "unknown"), zap.String("verification_scope", "none"),
	}}
	ctx = context.WithValue(ctx, nativeShadowKey{}, session)
	return evidence.WithRecorder(ctx, func(record evidence.Record) {
		session.mu.Lock()
		session.last, session.saw = record, true
		session.mu.Unlock()
		metrics.NativeUsageShadowTotal.WithLabelValues(record.Observation.Protocol, record.Observation.Status, source, record.Execution).Inc()
		fields := append([]zap.Field(nil), session.fields...)
		fields = append(fields, zap.Any("native_evidence", record))
		applogger.Log.Info("native usage shadow", fields...)
		if previous != nil {
			previous(record)
		}
	})
}

func compareNativeUsageShadow(ctx context.Context, actual *relaybiz.UsageEnvelope) {
	session, ok := ctx.Value(nativeShadowKey{}).(*nativeShadowAttempt)
	if !ok {
		return
	}
	session.mu.Lock()
	record, saw := session.last, session.saw
	session.mu.Unlock()
	if !saw {
		return
	}
	fields := append([]zap.Field(nil), session.fields...)
	fields = append(fields, zap.String("shadow_status", record.Observation.Status), zap.Any("shadow_buckets", record.Observation.Buckets))
	if actual != nil {
		fields = append(fields, zap.String("settlement_parse_status", string(actual.ParseStatus)), zap.Any("settlement_canonical", actual.Canonical), zap.String("settlement_reason", actual.DecisionReason))
	}
	applogger.Log.Info("native usage shadow comparison", fields...)
}
