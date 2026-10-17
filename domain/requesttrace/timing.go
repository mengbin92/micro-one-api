package requesttrace

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
)

const TimingSource = "relay-request-timing"
const TimingVersion = 1

// TimingSummary is a domain snapshot, independent of protobuf, storage and the
// tracing SDK being enabled. Durations are milliseconds; nil means unobserved.
// It contains no request/response content, credentials or raw upstream errors.
type TimingSummary struct {
	Version              int32             `json:"version"`
	ObservationID        string            `json:"observation_id"`
	ObservationKind      string            `json:"observation_kind"`
	ConnectionID         string            `json:"connection_id,omitempty"`
	OTelTraceID          string            `json:"otel_trace_id,omitempty"`
	SpanID               string            `json:"span_id,omitempty"`
	CompatibilityTraceID string            `json:"compatibility_trace_id,omitempty"`
	RootRequestID        string            `json:"root_request_id,omitempty"`
	UserID               int64             `json:"user_id"`
	Endpoint             string            `json:"endpoint"`
	Protocol             string            `json:"protocol"`
	ExecutionPath        string            `json:"execution_path"`
	Outcome              string            `json:"outcome"`
	StartedAtMS          int64             `json:"started_at_ms"`
	OutputFinishedAtMS   *int64            `json:"output_finished_at_ms,omitempty"`
	HandlerFinishedAtMS  *int64            `json:"handler_finished_at_ms,omitempty"`
	TurnFinishedAtMS     *int64            `json:"turn_finished_at_ms,omitempty"`
	RPCFinishedAtMS      *int64            `json:"rpc_finished_at_ms,omitempty"`
	OutputDurationMS     *float64          `json:"output_duration_ms,omitempty"`
	HandlerDurationMS    *float64          `json:"handler_duration_ms,omitempty"`
	TurnDurationMS       *float64          `json:"turn_duration_ms,omitempty"`
	RPCDurationMS        *float64          `json:"rpc_duration_ms,omitempty"`
	FirstContentMS       *float64          `json:"first_content_ms,omitempty"`
	OutputComplete       bool              `json:"output_complete"`
	OutputOutcome        string            `json:"output_outcome,omitempty"`
	CollectionState      string            `json:"collection_state"`
	MissingReasons       []string          `json:"missing_reasons,omitempty"`
	Attempts             []TimingAttempt   `json:"attempts,omitempty"`
	Stages               []TimingStage     `json:"stages,omitempty"`
	SettlementSnapshot   *TimingSettlement `json:"settlement_snapshot,omitempty"`
}

type TimingAttempt struct {
	Number          int32   `json:"number"`
	RequestID       string  `json:"request_id"`
	ReservationID   *string `json:"reservation_id,omitempty"`
	SourceKind      string  `json:"source_kind,omitempty"`
	SourceID        int64   `json:"source_id,omitempty"`
	UpstreamModelID string  `json:"upstream_model_id,omitempty"`
	Outcome         string  `json:"outcome"`
}

type TimingStage struct {
	Phase      string  `json:"phase"`
	DurationMS float64 `json:"duration_ms"`
	Count      int32   `json:"count"`
}

type TimingSettlement struct {
	Status       string `json:"status"`
	ObservedAtMS int64  `json:"observed_at_ms"`
}

// TimingPhases is intentionally private: callers cannot expand the telemetry
// vocabulary with model names, IDs or arbitrary errors.
var timingPhases = map[string]bool{
	"request.parse": true, "relay.plan": true, "auth.snapshot": true,
	"relay.attempt": true, "admission.check": true, "credential.resolve": true,
	"request.transform": true, "quota.reserve": true, "quota.commit": true,
	"quota.release": true, "upstream.http": true, "response.transform": true,
	"response.stream": true, "retry.backoff": true, "route.reselect": true,
	"usage.log": true, "health.report": true,
	"ws.accept": true, "ws.first_frame": true, "ws.pool.acquire": true,
	"ws.upstream.dial": true, "cache.l1": true,
}

func ValidTimingPhase(phase string) bool { return timingPhases[phase] }

func ValidTimingOutcome(outcome string) bool {
	switch outcome {
	case "success", "rejected", "failed", "cancelled", "incomplete", "unknown":
		return true
	}
	return false
}

func TimingDedupeKey(observationID string) string { return TimingSource + ":" + observationID }

func (s *TimingSummary) Validate() error {
	if s == nil {
		return fmt.Errorf("request timing summary is required")
	}
	id, err := uuid.Parse(s.ObservationID)
	if err != nil || id == uuid.Nil || id.String() != s.ObservationID || s.Version != TimingVersion {
		return fmt.Errorf("invalid timing version or observation_id")
	}
	if s.UserID < 0 || s.StartedAtMS <= 0 || !ValidTimingOutcome(s.Outcome) {
		return fmt.Errorf("invalid timing owner, start or outcome")
	}
	if s.ConnectionID != "" {
		if _, err := uuid.Parse(s.ConnectionID); err != nil {
			return fmt.Errorf("invalid connection identity")
		}
	}
	switch s.ObservationKind {
	case "http", "ws_turn", "grpc":
	default:
		return fmt.Errorf("invalid observation_kind")
	}
	switch s.Protocol {
	case "http", "sse", "websocket", "grpc":
	default:
		return fmt.Errorf("invalid timing protocol")
	}
	if (s.ObservationKind == "http" && s.Protocol != "http" && s.Protocol != "sse") ||
		(s.ObservationKind == "ws_turn" && s.Protocol != "websocket") ||
		(s.ObservationKind == "grpc" && s.Protocol != "grpc") {
		return fmt.Errorf("timing protocol does not match observation kind")
	}
	if s.ObservationKind == "grpc" && (s.OutputDurationMS != nil || s.OutputFinishedAtMS != nil || s.FirstContentMS != nil) {
		return fmt.Errorf("unary RPC does not observe client output milestones")
	}
	if s.FirstContentMS != nil && s.Protocol != "sse" && s.Protocol != "websocket" {
		return fmt.Errorf("first content requires a streaming output protocol")
	}
	if s.ObservationKind != "grpc" && (s.RPCDurationMS != nil || s.RPCFinishedAtMS != nil) ||
		s.ObservationKind != "ws_turn" && (s.TurnDurationMS != nil || s.TurnFinishedAtMS != nil) ||
		s.ObservationKind == "ws_turn" && (s.HandlerDurationMS != nil || s.HandlerFinishedAtMS != nil) {
		return fmt.Errorf("timing endpoint does not belong to observation kind")
	}
	switch s.CollectionState {
	case "recorded", "partial", "not_recorded":
	default:
		return fmt.Errorf("invalid collection_state")
	}
	if s.OTelTraceID != "" {
		if !validHexID(s.OTelTraceID, 32) {
			return fmt.Errorf("invalid otel_trace_id")
		}
	}
	if s.SpanID != "" {
		if !validHexID(s.SpanID, 16) || s.OTelTraceID == "" {
			return fmt.Errorf("invalid span_id")
		}
	}
	if len(s.RootRequestID) > 128 || len(s.CompatibilityTraceID) > 128 || len(s.Endpoint) > 256 ||
		!strings.HasPrefix(s.Endpoint, "/") || strings.ContainsAny(s.Endpoint, "?#\r\n") ||
		s.ExecutionPath == "" || len(s.ExecutionPath) > 32 {
		return fmt.Errorf("invalid timing route or identity")
	}
	for _, value := range []*float64{s.OutputDurationMS, s.HandlerDurationMS, s.TurnDurationMS, s.RPCDurationMS, s.FirstContentMS} {
		if value != nil && !validDuration(*value) {
			return fmt.Errorf("invalid timing duration")
		}
	}
	for _, value := range []*int64{s.OutputFinishedAtMS, s.HandlerFinishedAtMS, s.TurnFinishedAtMS, s.RPCFinishedAtMS} {
		if value != nil && *value < s.StartedAtMS {
			return fmt.Errorf("invalid timing finish")
		}
	}
	for _, pair := range []struct {
		finish   *int64
		duration *float64
	}{
		{s.OutputFinishedAtMS, s.OutputDurationMS}, {s.HandlerFinishedAtMS, s.HandlerDurationMS},
		{s.TurnFinishedAtMS, s.TurnDurationMS}, {s.RPCFinishedAtMS, s.RPCDurationMS},
	} {
		if (pair.finish == nil) != (pair.duration == nil) {
			return fmt.Errorf("timing finish and duration must have matching presence")
		}
	}
	if s.OutputComplete && (s.OutputFinishedAtMS == nil || s.OutputDurationMS == nil) {
		return fmt.Errorf("complete output requires an observed finish and duration")
	}
	switch s.OutputOutcome {
	case "", "complete", "incomplete", "write_failed", "cancelled":
	default:
		return fmt.Errorf("invalid output outcome")
	}
	if len(s.Attempts) > 128 || len(s.Stages) > len(timingPhases) || len(s.MissingReasons) > 32 {
		return fmt.Errorf("timing summary exceeds collection limits")
	}
	for _, reason := range s.MissingReasons {
		if !validReasonCode(reason) {
			return fmt.Errorf("invalid timing missing reason")
		}
	}
	seen := make(map[int32]bool, len(s.Attempts))
	for _, a := range s.Attempts {
		if a.Number < 1 || seen[a.Number] || len(a.RequestID) > 128 ||
			a.SourceID < 0 || len(a.UpstreamModelID) > 255 || !ValidTimingOutcome(a.Outcome) {
			return fmt.Errorf("invalid timing attempt")
		}
		if a.SourceKind != "" && a.SourceKind != "channel" && a.SourceKind != "subscription" {
			return fmt.Errorf("invalid timing attempt source")
		}
		if a.ReservationID != nil && (*a.ReservationID == "" || len(*a.ReservationID) > 64) {
			return fmt.Errorf("invalid timing reservation")
		}
		seen[a.Number] = true
	}
	stages := make(map[string]bool, len(s.Stages))
	for _, stage := range s.Stages {
		if !ValidTimingPhase(stage.Phase) || stages[stage.Phase] || !validDuration(stage.DurationMS) || stage.Count < 1 {
			return fmt.Errorf("invalid timing stage")
		}
		stages[stage.Phase] = true
	}
	if settlement := s.SettlementSnapshot; settlement != nil {
		switch settlement.Status {
		case "unknown", "not_reserved", "released", "committed", "enqueued", "failed":
		default:
			return fmt.Errorf("invalid settlement snapshot status")
		}
		if settlement.ObservedAtMS < s.StartedAtMS {
			return fmt.Errorf("invalid settlement observation time")
		}
	}
	return nil
}

func validDuration(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Clone isolates snapshots from concurrent recorder, caller and repository
// mutations, including optional values and nested attempt reservations.
func (s *TimingSummary) Clone() *TimingSummary {
	if s == nil {
		return nil
	}
	copy := *s
	copy.OutputFinishedAtMS = clonePointer(s.OutputFinishedAtMS)
	copy.HandlerFinishedAtMS = clonePointer(s.HandlerFinishedAtMS)
	copy.TurnFinishedAtMS = clonePointer(s.TurnFinishedAtMS)
	copy.RPCFinishedAtMS = clonePointer(s.RPCFinishedAtMS)
	copy.OutputDurationMS = clonePointer(s.OutputDurationMS)
	copy.HandlerDurationMS = clonePointer(s.HandlerDurationMS)
	copy.TurnDurationMS = clonePointer(s.TurnDurationMS)
	copy.RPCDurationMS = clonePointer(s.RPCDurationMS)
	copy.FirstContentMS = clonePointer(s.FirstContentMS)
	copy.SettlementSnapshot = clonePointer(s.SettlementSnapshot)
	copy.MissingReasons = append([]string(nil), s.MissingReasons...)
	copy.Stages = append([]TimingStage(nil), s.Stages...)
	copy.Attempts = append([]TimingAttempt(nil), s.Attempts...)
	for i := range copy.Attempts {
		copy.Attempts[i].ReservationID = clonePointer(copy.Attempts[i].ReservationID)
	}
	return &copy
}

func validHexID(id string, length int) bool {
	if len(id) != length || id == strings.Repeat("0", length) || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// Reasons are machine codes, never arbitrary upstream error messages.
func validReasonCode(reason string) bool {
	if reason == "" || len(reason) > 128 {
		return false
	}
	for _, c := range reason {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' {
			return false
		}
	}
	return true
}
