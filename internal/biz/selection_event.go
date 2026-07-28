package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ── v0.11.0 Phase 3 §3.4: selection / execution boundary records ────────────
//
// The roadmap requires structured records at the selection and execution
// boundaries: candidate sources, the final source, sticky hit, priority tier,
// fallback reason, execution result and latency. These feed the Prometheus
// metrics (§3.5, low-cardinality labels) and the admin operations view (§3.6,
// full detail in structured logs / traces). channel/account/model identifiers
// stay OUT of Prometheus labels (cardinality) and go only into the structured
// event/log.

// SelectionEvent is the structured record emitted at the selection boundary
// (RelayUsecase.Plan) and finalised at the execution boundary (after the
// upstream call returns). A zero SelectionEvent is valid; callers only set the
// fields they know.
type SelectionEvent struct {
	// RequestID ties the event to the relay request span.
	RequestID string
	// Group is the tenancy group (low cardinality, safe for labels).
	Group string
	// Model is the canonical public model id (HIGH cardinality — labels only
	// use a coarse provider_family derived from it, never the raw model).
	Model string

	// ── selection boundary ──
	// CandidateKinds lists the source kinds that had at least one candidate
	// (channel / subscription). Used to compute "traffic share by source".
	CandidateKinds []string
	// FinalKind is the selected source kind ("channel" / "subscription").
	FinalKind string
	// FinalSourceID is the selected channel or account id (structured log only).
	FinalSourceID int64
	// StickyHit is true when a sticky binding short-circuited selection.
	StickyHit bool
	// PriorityTier is the priority value of the winning tier (structured log).
	PriorityTier int64
	// SelectionReason is empty on success; on failure it carries the reason
	// ("no_channel", "no_subscription", "all_circuit_open", ...).
	SelectionReason string

	// ── execution boundary ──
	// Fallback is true when the request retried onto a different source after
	// the first choice failed. FallbackReason carries why ("upstream_5xx",
	// "timeout", "circuit_open", ...).
	Fallback       bool
	FallbackReason string
	// Result is "success" / "error" / "client_error" (low cardinality).
	// Empty at Plan time (before execution); FinalizeSelectionResult fills it.
	Result string
	// ProviderFamily is the coarse provider family ("openai", "anthropic",
	// "google", "zhipu", ...) derived from the model; safe for labels.
	ProviderFamily string
	// ElapsedMS is the wall-clock selection+execution latency.
	ElapsedMS int64
	// Planned marks a Plan-boundary event (emitted before execution). The
	// recorder uses it to split metrics: planned events increment
	// RoutingSelectionPlanned only; execution-finalized events increment
	// RoutingSelectionTotal / StickyHit / Fallback once. Without this split,
	// every request double-counts routing_selection_total (code review #1).
	Planned bool
	// At is when the event was emitted.
	At time.Time
}

// SelectionRecorder is the seam RelayUsecase calls to emit SelectionEvents.
// The default no-op recorder is safe (events are dropped); production wires
// the logging+metrics recorder in the server layer. Keeping the seam narrow
// means the hot path never blocks on logging or label formatting.
type SelectionRecorder interface {
	RecordSelection(ctx context.Context, event SelectionEvent)
}

// noopSelectionRecorder drops every event. Used when no recorder is wired
// (tests, disabled observability).
type noopSelectionRecorder struct{}

func (noopSelectionRecorder) RecordSelection(context.Context, SelectionEvent) {}

// NewNoopSelectionRecorder returns a SelectionRecorder that drops events.
func NewNoopSelectionRecorder() SelectionRecorder { return noopSelectionRecorder{} }

// selectionRecorderHolder lets RelayUsecase carry an optional recorder without
// a constructor change (existing call sites stay working). Set via SetRecorder.
type selectionRecorderHolder struct {
	mu       sync.RWMutex
	recorder SelectionRecorder
}

func (h *selectionRecorderHolder) get() SelectionRecorder {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.recorder == nil {
		return noopSelectionRecorder{}
	}
	return h.recorder
}

func (h *selectionRecorderHolder) set(r SelectionRecorder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recorder = r
}

// ProviderFamilyForModel derives a coarse provider family from a canonical
// model id. This is the ONLY model-derived value allowed in Prometheus labels
// (the raw model id is high-cardinality and stays in structured logs). The
// mapping is intentionally coarse so the label set stays bounded.
func ProviderFamilyForModel(model string) string {
	switch {
	case startsWith(model, "gpt-"), startsWith(model, "o1"), startsWith(model, "o3"), startsWith(model, "o4"), startsWith(model, "chatgpt"):
		return "openai"
	case startsWith(model, "claude-"):
		return "anthropic"
	case startsWith(model, "gemini-"):
		return "google"
	case startsWith(model, "glm-"):
		return "zhipu"
	case startsWith(model, "deepseek"):
		return "deepseek"
	case startsWith(model, "qwen"):
		return "alibaba"
	default:
		return "other"
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// generateSelectionRequestID mints a short correlation id when the caller did
// not supply one. crypto/rand keeps it unique enough for trace correlation; it
// is NOT used for idempotency (that is the billing reservation id).
func generateSelectionRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
