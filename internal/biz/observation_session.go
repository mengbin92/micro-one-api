package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

// observationSession is the relay-side producer of observation-only usage
// evidence. It is intentionally separate from the financial durable recorder:
// local observation failures are counted and logged, never returned to the
// business path.
type observationSession struct {
	mu          sync.Mutex
	downstream  context.Context
	delivery    *ObservationDelivery
	base        evidence.SendRef
	endpoint    string
	apiVersion  string
	protocol    string
	eventSeq    int64
	unmatched   int64
	send        evidence.SendRef
	open        bool
	terminal    bool
	last        evidence.Observation
	checkpoints int
	gapReported bool
}

const (
	observationPersistTimeout = 2 * time.Second
	observationMaxCheckpoints = evidence.MaxEvents
)

func ObservationEnabled() bool {
	return os.Getenv("RELAY_USAGE_OBSERVATION_V1") == "true"
}

type observationKey struct{}
type observationDeliveryKey struct{}

// WithObservationDelivery carries the process observation outbox to the
// attempt-scoped send seams. It is a value, not server state, so tests and
// multiple servers cannot leak a delivery into each other.
func WithObservationDelivery(ctx context.Context, delivery *ObservationDelivery) context.Context {
	// A disabled producer keeps its delivery only to drain retained evidence;
	// it must not mark live requests as "expected to be observed", or a
	// legitimate rollback would trip the detached-seam alert.
	if !ObservationEnabled() {
		return ctx
	}
	ctx = evidence.WithObservationExpected(ctx)
	if delivery == nil {
		return ctx
	}
	return context.WithValue(ctx, observationDeliveryKey{}, delivery)
}

func ObservationDeliveryFrom(ctx context.Context) *ObservationDelivery {
	delivery, _ := ctx.Value(observationDeliveryKey{}).(*ObservationDelivery)
	return delivery
}

func (d *ObservationDelivery) AllowsSource(kind string, id int64, model string) bool {
	if d == nil {
		return false
	}
	return d.scope.Allows(kind, id, model)
}

// WithUsageObservation attaches the observation-only send seam. It is
// independent of RELAY_NATIVE_USAGE_V2_ALLOWLIST and RELAY_EXECUTION_DELIVERY_V1:
// an empty financial allowlist must still leave user evidence.
func WithUsageObservation(ctx context.Context, channel *Channel, requestID, model string) context.Context {
	source, id := evidence.SourceKindUnknown, int64(0)
	if channel != nil {
		source, id = UpstreamSourceChannel, channel.ID
		if channel.SubscriptionAccountID > 0 {
			source, id = UpstreamSourceSubscription, channel.SubscriptionAccountID
		}
	}
	trace := requesttrace.FromContext(ctx)
	purpose := evidence.PurposeUser
	if trace.HealthProbeID != "" {
		purpose = evidence.PurposeHealthProbe
	}
	// The verified owner is available even when request timing is disabled.
	return withUsageObservation(ctx, evidence.SendRef{
		RequestID: requestID, RootRequestID: trace.RootRequestID, AttemptNumber: trace.Number,
		UserID: requesttrace.VerifiedUser(ctx), Purpose: purpose, SourceKind: source, SourceID: id, Model: model,
	})
}

// WithProbeUsageObservation attributes a send to its admitted operator target.
func WithProbeUsageObservation(ctx context.Context, attemptID, purpose, source string, id int64, model string) context.Context {
	return withUsageObservation(ctx, evidence.SendRef{
		RequestID: attemptID, RootRequestID: attemptID, AttemptNumber: 1,
		Purpose: purpose, SourceKind: source, SourceID: id, Model: model,
	})
}

func withUsageObservation(ctx context.Context, base evidence.SendRef) context.Context {
	if !ObservationEnabled() {
		return ctx
	}
	delivery := ObservationDeliveryFrom(ctx)
	if delivery == nil {
		return ctx
	}
	if !delivery.AllowsSource(base.SourceKind, base.SourceID, base.Model) {
		// A deliberately excluded source must not be counted as an expected
		// send: that would turn the detached-seam alert into a false positive
		// during a per-source rollout.
		ctx = context.WithValue(ctx, observationKey{}, (*observationSession)(nil))
		return evidence.WithoutObservationSink(evidence.WithoutObservationExpected(ctx))
	}
	ctx = evidence.WithObservationExpected(ctx)
	base.ObservationID = requesttrace.ObservationID(ctx)
	if base.ObservationID == "" {
		base.ObservationID = base.RequestID
		if base.ObservationID == "" {
			base.ObservationID = base.RootRequestID
		}
		switch {
		case base.ObservationID == "":
			base.ObservationID = uuid.NewString()
		case len(base.ObservationID) > 64:
			digest := sha256.Sum256([]byte(base.ObservationID))
			base.ObservationID = hex.EncodeToString(digest[:])
		}
	}
	if base.AttemptNumber < 1 {
		base.AttemptNumber = 1
	}
	if old, ok := ctx.Value(observationKey{}).(*observationSession); ok && old != nil {
		if old.base == base {
			return ctx
		}
	}
	session := &observationSession{downstream: ctx, delivery: delivery, base: base}
	ctx = context.WithValue(ctx, observationKey{}, session)
	return evidence.WithObservationSink(ctx, session)
}

// ObservationTurn supplies a sink when no admission callback is available.
// A later turn keeps its real observation identity but cannot invent a billing
// request id; production admission attaches that identity separately.
func ObservationTurn(ctx context.Context, index int64) (evidence.Sink, bool) {
	base, ok := ctx.Value(observationKey{}).(*observationSession)
	if !ok || base == nil {
		return nil, false
	}
	if index <= 1 {
		return base, true
	}
	ref := base.base
	ref.ObservationID = requesttrace.ObservationID(ctx)
	if ref.ObservationID == "" || ref.ObservationID == base.base.ObservationID {
		ref.ObservationID = uuid.NewString()
	}
	ref.RequestID = ""
	ref.SendID = ""
	turn := &observationSession{downstream: ctx, delivery: base.delivery, base: ref}
	return turn, true
}

func (s *observationSession) nextSend() evidence.SendRef {
	ref := s.base
	// A random per-session component makes the send id collision-free across
	// reselections, fallbacks and process restarts while keeping the
	// observation/attempt prefix for correlation.
	ref.SendID = ref.ObservationID + "/" + strconv.FormatInt(int64(ref.AttemptNumber), 10) + "/" + uuid.NewString()
	return ref
}

// Started is called before the supplier write. A new send always opens a new
// identity, so retries, fallbacks and reselections never share a terminal.
func (s *observationSession) Started(protocol, endpoint, apiVersion string) {
	if s == nil {
		return
	}
	var superseded *evidence.SendEvent
	s.mu.Lock()
	if s.open && !s.terminal {
		// A previous send never got a terminal. Close it as superseded instead
		// of leaving an unterminated send or reusing its identity.
		s.eventSeq++
		record := evidence.Record{Observation: evidence.Observation{Protocol: s.protocol, Status: "missing", Reason: "superseded"}, Execution: "incomplete"}
		event := evidence.NewSendEvent(s.withProtocolLocked(record), "terminal", s.eventSeq, &record, evidence.CaptureUnknown, "superseded", time.Now())
		superseded = &event
	}
	s.send = s.nextSend()
	s.open, s.terminal = true, false
	s.eventSeq = 0
	s.checkpoints = 0
	s.last = evidence.Observation{}
	s.protocol, s.endpoint, s.apiVersion = protocol, endpoint, apiVersion
	if s.send.Protocol == "" {
		s.send.Protocol = protocol
	}
	if s.send.Protocol == "" {
		s.send.Protocol = "other"
	}
	s.send.Endpoint, s.send.APIVersion = endpoint, apiVersion
	send := s.send
	s.mu.Unlock()
	if superseded != nil {
		s.finishEvent(*superseded, nil, evidence.CaptureUnknown, "missing")
	}
	s.record(evidence.NewSendEvent(send, "started", 0, nil, "", "", time.Now()))
	metrics.UsageObservationStarted.WithLabelValues(
		protocolLabel(protocol), boundedSourceKind(s.base.SourceKind), boundedPurpose(s.base.Purpose),
	).Inc()
}

func (s *observationSession) Checkpoint(record evidence.Record) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.open || s.terminal || s.checkpoints >= observationMaxCheckpoints {
		s.mu.Unlock()
		return
	}
	if reflect.DeepEqual(s.last, record.Observation) {
		s.mu.Unlock()
		return
	}
	s.last = record.Observation
	s.checkpoints++
	s.eventSeq++
	event := evidence.NewSendEvent(s.withProtocolLocked(record), "checkpoint", s.eventSeq, &record, evidence.ClassifyCapture(record, false), "", time.Now())
	s.mu.Unlock()
	s.record(event)
}

// Terminal is called once per send. A record without an open send is an
// association anomaly: it never fabricates a send that was not written to the
// supplier.
func (s *observationSession) Terminal(record evidence.Record) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.open || s.terminal {
		ref := s.base
		ref.SendID = ""
		ref.Protocol = record.Observation.Protocol
		if ref.Protocol == "" {
			ref.Protocol = s.protocol
		}
		if ref.Protocol == "" {
			ref.Protocol = "other"
		}
		ref.Endpoint, ref.APIVersion = s.endpoint, s.apiVersion
		event := evidence.NewSendEvent(ref, "unmatched", s.unmatched, &record, evidence.ClassifyCapture(record, s.downstreamCanceledLocked()), record.Observation.Reason, time.Now())
		s.unmatched++
		s.mu.Unlock()
		s.record(event)
		metrics.RecordUsageObservationGap("unmatched", float64(time.Now().Unix()))
		return
	}
	s.terminal, s.open = true, false
	s.eventSeq++
	canceled := s.downstreamCanceledLocked()
	capture := evidence.ClassifyCapture(record, canceled)
	if capture == evidence.CaptureComplete && s.base.SourceKind == UpstreamSourceChannel && s.base.SourceID == 1 {
		record.Observation = record.ChannelOneChatObservation()
	}
	send := s.withProtocolLocked(record)
	event := evidence.NewSendEvent(send, "terminal", s.eventSeq, &record, capture, record.Observation.Reason, time.Now())
	s.mu.Unlock()
	s.finishEvent(event, &record, capture, evidence.NormalizeStatus(record.Observation.Status))
}

// TransportFail closes an open send whose transport ended without a usable
// response. A send that was written but never terminated must still get one
// explicit failed terminal.
func (s *observationSession) TransportFail(protocol, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.open || s.terminal {
		s.mu.Unlock()
		return
	}
	if protocol == "" {
		protocol = s.protocol
	}
	if protocol == "" {
		protocol = "other"
	}
	s.terminal, s.open = true, false
	s.eventSeq++
	record := evidence.Record{
		Observation:      evidence.Observation{Protocol: protocol, ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, Status: "missing", Reason: reason},
		Execution:        "execution_unknown",
		Stream:           protocol == evidence.StepAudioRealtime,
		UpstreamEndpoint: s.endpoint,
		APIVersion:       s.apiVersion,
	}
	if reason == "truncated" {
		record.Execution = "incomplete"
	}
	capture := evidence.ClassifyCapture(record, s.downstreamCanceledLocked())
	send := s.withProtocolLocked(record)
	event := evidence.NewSendEvent(send, "terminal", s.eventSeq, &record, capture, reason, time.Now())
	s.mu.Unlock()
	s.finishEvent(event, &record, capture, "missing")
}

// Redirect closes the current hop and opens the next one. The redirect policy
// itself is unchanged; this only records that one Do produced several real
// sends.
func (s *observationSession) Redirect(protocol, path, apiVersion string, status int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.open || s.terminal {
		s.mu.Unlock()
		return
	}
	s.terminal, s.open = true, false
	s.eventSeq++
	record := evidence.Record{
		Observation:      evidence.Observation{Protocol: protocol, ParserRevision: evidence.ParserRevision, ContractRevision: evidence.ContractRevision, Status: "missing", Reason: "redirect_hop"},
		HTTPStatus:       status,
		Execution:        "upstream_rejected",
		UpstreamEndpoint: s.endpoint,
		APIVersion:       s.apiVersion,
	}
	send := s.withProtocolLocked(record)
	capture := evidence.ClassifyCapture(record, false)
	event := evidence.NewSendEvent(send, "terminal", s.eventSeq, &record, capture, "redirect_hop", time.Now())
	s.mu.Unlock()
	s.finishEvent(event, &record, capture, "missing")
	s.Started(protocol, path, apiVersion)
}

// NotSent records a request refused at the send boundary. It is not a send:
// no send id, no started and no terminal event are fabricated.
func (s *observationSession) NotSent(protocol, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	ref := s.base
	ref.SendID = ""
	if protocol != "" {
		ref.Protocol = protocol
	}
	if ref.Protocol == "" {
		ref.Protocol = "other"
	}
	s.mu.Unlock()
	s.record(evidence.NewSendEvent(ref, "not_sent", 0, nil, evidence.CaptureNotSent, reason, time.Now()))
}

func (s *observationSession) withProtocolLocked(record evidence.Record) evidence.SendRef {
	ref := s.send
	if ref.Protocol == "" {
		ref.Protocol = record.Observation.Protocol
	}
	if ref.Protocol == "" {
		ref.Protocol = s.protocol
	}
	if ref.Protocol == "" {
		ref.Protocol = "other"
	}
	ref.Endpoint, ref.APIVersion = s.endpoint, s.apiVersion
	return ref
}

func (s *observationSession) downstreamCanceledLocked() bool {
	// Only a plain cancellation proves the client went away. A deadline is not
	// proof, and upstream client timeouts never touch this context.
	return s.downstream != nil && s.downstream.Err() == context.Canceled
}

// finishEvent records one terminal and its bounded metrics. It is called after
// the session lock is released.
func (s *observationSession) finishEvent(event evidence.SendEvent, _ *evidence.Record, capture, status string) {
	s.record(event)
	protocol := event.Send.Protocol
	metrics.UsageObservationTerminal.WithLabelValues(
		protocolLabel(protocol), boundedSourceKind(event.Send.SourceKind), boundedPurpose(event.Send.Purpose), capture, status,
	).Inc()
	now := float64(time.Now().Unix())
	if capture == evidence.CaptureComplete && status != "verified" && event.Send.Purpose == evidence.PurposeUser {
		metrics.RecordUsageObservationAnomaly(protocolLabel(protocol), boundedSourceKind(event.Send.SourceKind), evidence.NormalizeStatus(status), now)
	}
	switch {
	case capture == evidence.CaptureLimited, capture == evidence.CaptureUnknown, status == "unobserved",
		event.Category == "unmatched", event.Category == "not_sent":
		metrics.RecordUsageObservationGap(gapKind(event, capture, status), now)
	}
}

func gapKind(event evidence.SendEvent, capture, status string) string {
	if event.Category == "unmatched" || event.Category == "not_sent" {
		return "unmatched"
	}
	switch {
	case capture == evidence.CaptureLimited:
		return "limited"
	case status == "unobserved":
		return "unobserved"
	default:
		return "unknown"
	}
}

// record persists locally with a detached timeout. It never returns an error:
// the relay business response, retry policy and settlement are unaffected.
func (s *observationSession) record(event evidence.SendEvent) {
	if err := event.Validate(); err != nil {
		metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverRejected).Inc()
		applogger.Log.Warn("usage observation rejected", zap.String("event_id", event.ID), zap.Error(err))
		return
	}
	if _, _, err := event.Encode(); err != nil {
		metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverRejected).Inc()
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.downstream), observationPersistTimeout)
	defer cancel()
	if err := s.delivery.Record(ctx, event); err != nil {
		metrics.RecordUsageObservationGap("lost", float64(time.Now().Unix()))
		metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverLost).Inc()
		applogger.Log.Warn("usage observation degraded", zap.String("event_id", event.ID), zap.String("category", event.Category), zap.Error(err))
		return
	}
	metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverAccepted).Inc()
}

// protocolLabel maps unexpected protocol values to a bounded enum so a producer
// bug cannot create unbounded label cardinality.
func protocolLabel(protocol string) string {
	switch protocol {
	case "openai_chat", "responses", "anthropic_messages", "gemini", "embeddings", evidence.StepAudioRealtime, "other":
		return protocol
	}
	return "other"
}

// boundedSourceKind and boundedPurpose validate against their own finite
// enums; they must never reuse the protocol enum.
func boundedSourceKind(kind string) string {
	switch kind {
	case "channel", "subscription", "unknown":
		return kind
	}
	return "unknown"
}

func boundedPurpose(purpose string) string {
	switch purpose {
	case evidence.PurposeUser, evidence.PurposeHealthProbe:
		return purpose
	case evidence.PurposeCanary, evidence.PurposeInternal:
		return purpose
	}
	return evidence.PurposeUnknown
}
