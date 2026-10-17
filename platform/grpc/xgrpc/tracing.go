package xgrpc

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/security/serviceidentity"
	xtrace "micro-one-api/platform/tracing"
)

type rpcTraceKey struct{}
type rpcTraceState struct {
	mu             sync.Mutex
	method         string
	request, reply any
	phaseEnd       func(error)
	span           trace.Span
	parse          trace.Span
	recorder       *requesttrace.Recorder
	submit         func(context.Context, *requesttrace.TimingSummary) bool
}

type traceStats struct {
	client   bool
	verifier *serviceidentity.Verifier
	submit   func(context.Context, *requesttrace.TimingSummary) bool
}
type metadataCarrier metadata.MD

func (m metadataCarrier) Get(key string) string {
	values := metadata.MD(m).Get(key)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
func (m metadataCarrier) Set(key, value string) { metadata.MD(m).Set(key, value) }
func (m metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func ClientStatsHandler() stats.Handler { return &traceStats{client: true} }
func ServerStatsHandler(submit func(context.Context, *requesttrace.TimingSummary) bool) stats.Handler {
	return &traceStats{verifier: serviceidentity.FromEnvironment(os.Getenv("SERVICE_TOKEN")), submit: submit}
}
func (h *traceStats) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	state := &rpcTraceState{submit: h.submit, method: info.FullMethodName}
	if h.client {
		if phase := rpcPhase(info.FullMethodName); phase != "" {
			ctx, state.phaseEnd = requesttrace.Phase(ctx, phase)
		}
		var span trace.Span
		ctx, span = otel.Tracer("micro-one-api/grpc").Start(ctx, info.FullMethodName, trace.WithSpanKind(trace.SpanKindClient))
		state.span = span
		md, _ := metadata.FromOutgoingContext(ctx)
		md = md.Copy()
		if id := xtrace.ExtractTraceID(ctx); id != "" {
			md.Set(traceIDKey, id)
		}
		propagation.TraceContext{}.Inject(ctx, metadataCarrier(md))
		if id := requesttrace.Capture(ctx).ObservationID; id != "" {
			md.Set("x-observation-id", id)
		}
		ctx = metadata.NewOutgoingContext(ctx, md)
	} else {
		// Only authenticated service credentials can provide an internal parent.
		// Authentication failures still get a local transport span/summary.
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("authorization")
		if len(values) == 1 && strings.HasPrefix(values[0], "Bearer ") {
			if principal, err := h.verifier.Authenticate(strings.TrimPrefix(values[0], "Bearer ")); err == nil && (!principal.Dedicated || principal.CanCall(info.FullMethodName)) {
				ctx = propagation.TraceContext{}.Extract(ctx, metadataCarrier(md))
				ctx = requesttrace.WithObservationID(ctx, md.Get("x-observation-id"))
				ctx = TraceIDFromIncoming(ctx)
			}
		}
		if h.submit != nil && info.FullMethodName == "/api.relay.v1.RelayService/ChatCompletion" {
			ctx, state.recorder = requesttrace.StartObservation(ctx, "grpc", info.FullMethodName, "grpc", "grpc", true)
			state.span = trace.SpanFromContext(ctx)
		} else {
			ctx, state.span = otel.Tracer("micro-one-api/grpc").Start(ctx, info.FullMethodName, trace.WithSpanKind(trace.SpanKindServer))
		}
		_, state.parse = otel.Tracer("micro-one-api/grpc").Start(ctx, "request.parse")
	}
	state.span.SetAttributes(attribute.String("rpc.system", "grpc"), attribute.String("rpc.method", info.FullMethodName))
	return context.WithValue(ctx, rpcTraceKey{}, state)
}
func (h *traceStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	state, _ := ctx.Value(rpcTraceKey{}).(*rpcTraceState)
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	switch value := event.(type) {
	case *stats.OutPayload:
		if h.client {
			state.request = value.Payload
			observeReserveRequest(ctx, state.method, state.request)
		}
	case *stats.InPayload:
		if h.client {
			state.reply = value.Payload
		}
		if state.parse != nil {
			state.parse.End()
			state.parse = nil
		}
	case *stats.End:
		if h.client {
			observeRPCResult(ctx, state.method, state.request, state.reply, value.Error)
		}
		if state.parse != nil {
			state.parse.End()
			state.parse = nil
		}
		state.span.SetAttributes(attribute.String("rpc.status", status.Code(value.Error).String()))
		if value.Error != nil {
			state.span.SetStatus(otelcodes.Error, status.Code(value.Error).String())
		}
		if state.recorder != nil {
			state.submit(ctx, state.recorder.Finish(rpcOutcome(value.Error)))
		} else {
			state.span.End(trace.WithTimestamp(value.EndTime))
		}
		if state.phaseEnd != nil {
			phaseErr := value.Error
			if success, ok := state.reply.(interface{ GetSuccess() bool }); ok && !success.GetSuccess() {
				phaseErr = errors.New("operation_rejected")
			}
			state.phaseEnd(phaseErr)
		}
	}
}
func (h *traceStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (h *traceStats) HandleConn(context.Context, stats.ConnStats)                       {}

// HandlerTiming deliberately measures only the service handler. Transport
// parsing and response encoding/sending remain owned by stats Begin/End.
func HandlerTiming(ctx context.Context) func() {
	started := time.Now()
	return func() { requesttrace.Observation(ctx).HandlerFinished(started) }
}

func rpcPhase(method string) string {
	switch {
	case strings.HasSuffix(method, "/GetAuthSnapshot"):
		return "auth.snapshot"
	case strings.HasSuffix(method, "/ReserveQuota"):
		return "quota.reserve"
	case strings.HasSuffix(method, "/CommitQuota"):
		return "quota.commit"
	case strings.HasSuffix(method, "/ReleaseQuota"):
		return "quota.release"
	case strings.HasSuffix(method, "/IngestLog"):
		return "usage.log"
	case strings.Contains(method, "Health"):
		return "health.report"
	}
	return ""
}

type reserveMetadata interface {
	GetRequestId() string
	GetRootRequestId() string
	GetAttemptNumber() int32
	GetSourceKind() string
	GetUpstreamModelId() string
	GetChannelId() string
	GetSubscriptionAccountId() int64
}

func observeReserveRequest(ctx context.Context, method string, payload any) {
	if !strings.HasSuffix(method, "/ReserveQuota") {
		return
	}
	req, ok := payload.(reserveMetadata)
	if !ok {
		return
	}
	sourceID, _ := strconv.ParseInt(req.GetChannelId(), 10, 64)
	if req.GetSubscriptionAccountId() > 0 {
		sourceID = req.GetSubscriptionAccountId()
	}
	requesttrace.BindRoot(ctx, req.GetRootRequestId())
	requesttrace.RecordAttempt(ctx, requesttrace.TimingAttempt{Number: req.GetAttemptNumber(), RequestID: req.GetRequestId(), SourceKind: req.GetSourceKind(), SourceID: sourceID, UpstreamModelID: req.GetUpstreamModelId()})
}
func observeRPCResult(ctx context.Context, method string, request, reply any, err error) {
	switch {
	case strings.HasSuffix(method, "/GetAuthSnapshot"):
		if value, ok := reply.(interface {
			GetUserId() int64
			GetUserEnabled() bool
			GetTokenEnabled() bool
		}); ok && err == nil && value.GetUserEnabled() && value.GetTokenEnabled() {
			requesttrace.BindUser(ctx, value.GetUserId())
		}
	case strings.HasSuffix(method, "/ReserveQuota"):
		req, ok := request.(reserveMetadata)
		if !ok {
			return
		}
		attempt := requesttrace.TimingAttempt{Number: req.GetAttemptNumber(), RequestID: req.GetRequestId(), Outcome: "rejected"}
		if value, ok := reply.(interface {
			GetReservationId() string
			GetSuccess() bool
		}); ok && err == nil && value.GetSuccess() {
			id := value.GetReservationId()
			if id != "" {
				attempt.ReservationID = &id
			}
			attempt.Outcome = "unknown"
		}
		requesttrace.RecordAttempt(ctx, attempt)
	case strings.HasSuffix(method, "/CommitQuota"), strings.HasSuffix(method, "/ReleaseQuota"):
		state := "failed"
		if value, ok := reply.(interface{ GetSuccess() bool }); ok && err == nil && value.GetSuccess() {
			state = "committed"
			if strings.HasSuffix(method, "/ReleaseQuota") {
				state = "released"
			}
			if value, ok := reply.(interface{ GetAsyncEnqueued() bool }); ok && value.GetAsyncEnqueued() {
				state = "enqueued"
			}
		}
		requesttrace.Observation(ctx).Update(func(s *requesttrace.TimingSummary) {
			s.SettlementSnapshot = &requesttrace.TimingSettlement{Status: state, ObservedAtMS: time.Now().UnixMilli()}
		})
		outcome := "success"
		if state == "failed" || strings.HasSuffix(method, "/ReleaseQuota") {
			outcome = "failed"
		}
		if request, ok := request.(interface{ GetSuccess() bool }); ok && !request.GetSuccess() {
			outcome = "failed"
		}
		number := requesttrace.FromContext(ctx).Number
		if ref, ok := request.(interface{ GetReservationId() string }); ok {
			number = requesttrace.AttemptNumberForReservation(ctx, ref.GetReservationId())
		}
		requesttrace.RecordAttempt(ctx, requesttrace.TimingAttempt{Number: number, Outcome: outcome})
		requesttrace.FinishAttempt(ctx, number, outcome)
	}
}

func rpcOutcome(err error) string {
	switch status.Code(err) {
	case codes.OK:
		return "success"
	case codes.Canceled:
		return "cancelled"
	case codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.ResourceExhausted:
		return "rejected"
	default:
		return "failed"
	}
}
