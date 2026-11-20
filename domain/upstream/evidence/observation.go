package evidence

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"
)

// ObservationVersion is the envelope version of the observation-only delivery
// contract. It is independent of execution.Version and never authorizes
// settlement.
const ObservationVersion int32 = 1

// MaxObservationEventBytes bounds one delivered observation event. The native
// usage subtree and per-send event count are separately bounded.
const MaxObservationEventBytes = 128 << 10

// Capture classifies what the transport actually produced for one upstream
// send. It is deliberately separate from Observation.Status: a complete
// response can still carry invalid usage, and a truncated response can still
// carry a valid earlier snapshot.
const (
	CaptureComplete         = "complete"
	CaptureClientCancel     = "client_cancel"
	CaptureUpstreamRejected = "upstream_rejected"
	CaptureUpstreamFailed   = "upstream_failed"
	CaptureTransportError   = "transport_error"
	CaptureTruncated        = "truncated"
	CaptureLimited          = "limited"
	CaptureUnknown          = "unknown"
	CaptureNotSent          = "not_sent"
)

// Purpose is bounded to the values the relay can prove. canary and
// internal_test are reserved for producers that can state them explicitly.
const (
	PurposeUser        = "user"
	PurposeCanary      = "canary"
	PurposeHealthProbe = "health_probe"
	PurposeInternal    = "internal_test"
	PurposeUnknown     = "unknown"
)

// SourceKindUnknown marks a send whose procurement source could not be
// verified. It never guesses a channel/account identity.
const SourceKindUnknown = "unknown"

// NotSentReason classifies a request that reached the send boundary but was
// never written to the supplier. It is not a send and carries no started or
// terminal event.
const (
	NotSentAdmissionRejected = "admission_rejected"
	NotSentTransportRefused  = "transport_refused"
)

// SendRef identifies one actual upstream business send. A protocol fallback, a
// reselected source, a redirect hop or a retried attempt is a distinct send.
type SendRef struct {
	ObservationID string `json:"observation_id"`
	SendID        string `json:"send_id"`
	RequestID     string `json:"request_id,omitempty"`
	RootRequestID string `json:"root_request_id,omitempty"`
	AttemptNumber int32  `json:"attempt_number"`
	// UserID is the verified request owner. It scopes evidence queries; it is
	// never an authorization credential.
	UserID     int64  `json:"user_id,omitempty"`
	Purpose    string `json:"purpose"`
	SourceKind string `json:"source_kind"`
	SourceID   int64  `json:"source_id,omitempty"`
	Model      string `json:"upstream_model_id,omitempty"`
	Protocol   string `json:"protocol"`
	Endpoint   string `json:"endpoint,omitempty"`
	APIVersion string `json:"api_version,omitempty"`
}

// SendEvent is the observation-only delivery envelope. It carries no final
// amount, settlement instruction, lease or financial policy. ObservedAt is a
// Unix millisecond timestamp: the window contract is millisecond precision, so
// second-granularity storage would silently widen or merge query windows.
type SendEvent struct {
	Version       int32   `json:"version"`
	ID            string  `json:"event_id"`
	Send          SendRef `json:"send"`
	Category      string  `json:"category"`
	Sequence      int64   `json:"sequence"`
	ObservedAt    int64   `json:"observed_at"`
	Capture       string  `json:"capture,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	Record        *Record `json:"record,omitempty"`
	BuildRevision string  `json:"build_revision"`
}

// Key is the deterministic event identity. Send-scoped categories key on the
// send; request-level facts (not_sent, unmatched responses) key on the
// observation and must never invent a send id.
func (e SendEvent) Key() string {
	if requestLevelCategory(e.Category) {
		identity, _ := jsonx.Marshal(e.Send)
		return fmt.Sprintf("%s/%x/%s/%d", e.Send.ObservationID, sha256.Sum256(identity), e.Category, e.Sequence)
	}
	return e.Send.SendID + "/" + e.Category + "/" + strconv.FormatInt(e.Sequence, 10)
}

func requestLevelCategory(category string) bool {
	return category == "not_sent" || category == "unmatched"
}

// Encode returns the canonical payload and its sha256 digest. Secondary
// samples are dropped from the tail when the byte cap is exceeded so the
// terminal summary survives; the drop is recorded, never silent.
func (e SendEvent) Encode() ([]byte, string, error) {
	if err := e.Validate(); err != nil {
		return nil, "", err
	}
	if e.Record != nil {
		copy := *e.Record
		e.Record = &copy
	}
	b, err := jsonx.Marshal(e)
	if err != nil {
		return nil, "", err
	}
	for len(b) > MaxObservationEventBytes && e.Record != nil && len(e.Record.Events) > 0 {
		e.Record.Events = e.Record.Events[:len(e.Record.Events)-1]
		e.Record.DroppedEvents++
		b, err = jsonx.Marshal(e)
		if err != nil {
			return nil, "", err
		}
	}
	if len(b) > MaxObservationEventBytes {
		return nil, "", fmt.Errorf("observation event exceeds limit")
	}
	return b, fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// Validate enforces the observation contract. It does not weaken
// execution.Event.Validate and grants no settlement authority.
func (e SendEvent) Validate() error {
	s := e.Send
	if e.Version != ObservationVersion || e.Sequence < 0 || e.ID != e.Key() || len(e.ID) > 255 || e.ObservedAt <= 0 {
		return fmt.Errorf("invalid observation identity")
	}
	if len(s.SendID) > 160 || s.ObservationID == "" || len(s.ObservationID) > 64 {
		return fmt.Errorf("invalid send identity")
	}
	if len(s.RequestID) > 128 || len(s.RootRequestID) > 128 || s.AttemptNumber < 1 {
		return fmt.Errorf("invalid request identity")
	}
	if !validPurpose(s.Purpose) {
		return fmt.Errorf("invalid purpose")
	}
	if s.SourceKind != "channel" && s.SourceKind != "subscription" && s.SourceKind != SourceKindUnknown {
		return fmt.Errorf("invalid source kind")
	}
	if s.SourceKind == SourceKindUnknown && s.SourceID != 0 || s.SourceKind != SourceKindUnknown && s.SourceID <= 0 {
		return fmt.Errorf("invalid source identity")
	}
	if len(s.Model) > 255 || s.Protocol == "" || len(s.Protocol) > 64 {
		return fmt.Errorf("invalid upstream identity")
	}
	if len(s.Endpoint) > 512 || strings.ContainsAny(s.Endpoint, "?#") {
		return fmt.Errorf("unsafe endpoint")
	}
	if len(s.APIVersion) > 128 {
		return fmt.Errorf("invalid api version")
	}
	switch e.Category {
	case "started":
		if s.SendID == "" || e.Sequence != 0 || e.Record != nil || e.Capture != "" {
			return fmt.Errorf("invalid started event")
		}
	case "checkpoint", "terminal":
		// A checkpoint/terminal must belong to a real send. Response-side
		// records without a send use the unmatched category instead.
		if s.SendID == "" || e.Record == nil || !validCapture(e.Capture) {
			return fmt.Errorf("invalid evidence event")
		}
	case "not_sent":
		// Not sent is not a send: no send id, no record, request-level key.
		if s.SendID != "" || e.Sequence != 0 || e.Record != nil || e.Capture != CaptureNotSent || !validNotSent(e.Reason) {
			return fmt.Errorf("invalid not_sent event")
		}
	case "unmatched":
		// A supplier response that cannot be tied to an actual send. It is an
		// association anomaly, never a fabricated send.
		if s.SendID != "" || e.Sequence < 0 || e.Record == nil || !validCapture(e.Capture) {
			return fmt.Errorf("invalid unmatched event")
		}
	default:
		return fmt.Errorf("invalid category")
	}
	if r := e.Record; r != nil {
		if len(r.Events) > MaxEvents {
			return fmt.Errorf("observation evidence exceeds limits")
		}
		for _, observation := range []Observation{r.Observation} {
			if len(observation.Usage) > MaxUsageBytes || len(observation.Fingerprint) > 2048 || len(observation.ProviderRequestID) > 256 {
				return fmt.Errorf("observation evidence exceeds limits")
			}
			if b := observation.Buckets; b != nil {
				sum := int64(0)
				for _, v := range b {
					if v < 0 || v > math.MaxInt64-sum {
						return fmt.Errorf("invalid observation buckets")
					}
					sum += v
				}
			}
		}
	}
	return nil
}

func validPurpose(purpose string) bool {
	switch purpose {
	case PurposeUser, PurposeCanary, PurposeHealthProbe, PurposeInternal, PurposeUnknown:
		return true
	default:
		return false
	}
}

func validCapture(capture string) bool {
	switch capture {
	case CaptureComplete, CaptureClientCancel, CaptureUpstreamRejected, CaptureUpstreamFailed,
		CaptureTransportError, CaptureTruncated, CaptureLimited, CaptureUnknown:
		return true
	default:
		return false
	}
}

func validNotSent(reason string) bool {
	return reason == NotSentAdmissionRejected || reason == NotSentTransportRefused
}

// DeliverResult is the bounded delivery outcome reported by the observation
// outbox. Local persistence and remote acknowledgement are distinct results.
const (
	DeliverAccepted = "accepted"
	DeliverAcked    = "acked"
	DeliverRetried  = "retried"
	DeliverFailed   = "failed"
	DeliverLost     = "lost"
	DeliverConflict = "conflict"
	DeliverRejected = "rejected"
)

// Sink is the observation-only seam at the shared send boundary. Implementations
// must never change the business response, the retry policy, the reservation or
// the settlement outcome; every method returns nothing on purpose.
type Sink interface {
	// Started persists the minimal send metadata before the upstream write.
	// protocol comes from the shared send seam, never from the parsed response.
	Started(protocol, endpoint, apiVersion string)
	// Checkpoint persists a bounded metering snapshot for the open send.
	Checkpoint(Record)
	// Terminal persists the single terminal state for the open send. A record
	// without an open send is an association anomaly, not a new send.
	Terminal(Record)
	// TransportFail closes an open send whose transport ended without a
	// response (write failure, upstream close, client disconnect).
	TransportFail(protocol, reason string)
	// Redirect closes the current hop and opens the next one. Each redirect
	// hop is a real HTTP send with its own identity.
	Redirect(protocol, path, apiVersion string, status int)
	// NotSent records a request rejected at the send boundary. No send exists,
	// but the requested protocol is still trustworthy metadata.
	NotSent(protocol, reason string)
}

type sinkKey struct{}
type expectedKey struct{}

// WithObservationSink attaches the observation seam. It is independent of the
// financial durable recorder so observation failures never reach business
// paths.
func WithObservationSink(ctx context.Context, sink Sink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, sinkKey{}, sink)
}

// WithoutObservationSink masks an inherited sink when a source is excluded.
func WithoutObservationSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, sinkKey{}, struct{}{})
}

// WithObservationExpected marks the request as one the deployment expects to be
// observed. The transport seam counts sends against this expectation even when
// the sink is not attached, so a missing sink cannot hide behind healthy
// outbox gauges.
func WithObservationExpected(ctx context.Context) context.Context {
	return context.WithValue(ctx, expectedKey{}, true)
}

// ObservationExpected reports whether observation was expected for this request.
func ObservationExpected(ctx context.Context) bool {
	expected, _ := ctx.Value(expectedKey{}).(bool)
	return expected
}

// WithoutObservationExpected clears the expectation flag for a request that is
// deliberately outside the observation scope. It never changes the sink.
func WithoutObservationExpected(ctx context.Context) context.Context {
	return context.WithValue(ctx, expectedKey{}, false)
}

// ObservationSinkFrom returns the attached seam, if any.
func ObservationSinkFrom(ctx context.Context) Sink {
	sink, _ := ctx.Value(sinkKey{}).(Sink)
	return sink
}

// ClassifyCapture derives the transport verdict from the observed record. The
// downstream cancellation signal is only trusted when the caller can prove the
// client actually went away; a plain context.Canceled or deadline is not proof.
func ClassifyCapture(r Record, downstreamCanceled bool) string {
	if downstreamCanceled {
		return CaptureClientCancel
	}
	if r.Execution == "execution_unknown" || r.Observation.Reason == "transport_error" && r.HTTPStatus == 0 {
		return CaptureTransportError
	}
	if r.HTTPStatus != 0 && (r.HTTPStatus < 200 || r.HTTPStatus >= 300) {
		return CaptureUpstreamRejected
	}
	if r.Observation.Protocol == StepAudioRealtime {
		if stepAudioUnsuccessful(r.Observation.Terminal) {
			return CaptureUpstreamFailed
		}
		if r.Observation.Terminal == "unknown" {
			return CaptureUnknown
		}
	}
	if r.Observation.Terminal == "failed" || r.Execution == "upstream_rejected" {
		return CaptureUpstreamRejected
	}
	switch r.Observation.Reason {
	case "frame_limit", "usage_limit":
		return CaptureLimited
	}
	if r.Complete {
		return CaptureComplete
	}
	if r.Observation.Status == "unobserved" {
		return CaptureLimited
	}
	if r.Execution == "incomplete" {
		return CaptureTruncated
	}
	return CaptureUnknown
}

// NormalizeStatus bounds the observation status to the contract enum.
func NormalizeStatus(status string) string {
	switch status {
	case "verified", "missing", "invalid", "ambiguous", "unsupported", "unobserved":
		return status
	default:
		return "unobserved"
	}
}

// NewSendEvent builds an immutable observation event. ObservedAt is stored in
// milliseconds so query windows are exact.
func NewSendEvent(send SendRef, category string, sequence int64, record *Record, capture, reason string, now time.Time) SendEvent {
	e := SendEvent{Version: ObservationVersion, Send: send, Category: category, Sequence: sequence, ObservedAt: now.UnixMilli(), Capture: capture, Reason: reason, Record: record, BuildRevision: BuildRevision}
	if category == "started" {
		e.Capture = ""
	}
	e.ID = e.Key()
	return e
}

// DeliveryReceipt is what the Log owner confirms for one observation event.
// It covers database commit and digest agreement only, never billing or
// settlement completion.
type DeliveryReceipt struct {
	Accepted  bool
	Duplicate bool
	Status    string
	Digest    string
}
