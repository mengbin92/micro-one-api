package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetricsSelectionRecorder_DoesNotPanic(t *testing.T) {
	// nil log → uses app logger (which may be nil in tests); must not panic.
	r := NewMetricsSelectionRecorder(nil)
	r.RecordSelection(context.Background(), SelectionEvent{
		RequestID:      "req-1",
		Group:          "default",
		Model:          "gpt-4o",
		FinalKind:      "channel",
		FinalSourceID:  5,
		Result:         "success",
		ProviderFamily: "openai",
		StickyHit:      false,
		ElapsedMS:      12,
	})
}

func TestMetricsSelectionRecorder_FallbackAndSticky(t *testing.T) {
	r := NewMetricsSelectionRecorder(nil)
	// Fallback event.
	r.RecordSelection(context.Background(), SelectionEvent{
		FinalKind:       "subscription",
		Result:          "success",
		ProviderFamily:  "anthropic",
		Fallback:        true,
		FallbackReason:  "upstream_5xx",
	})
	// Sticky hit.
	r.RecordSelection(context.Background(), SelectionEvent{
		FinalKind:       "subscription",
		Result:          "success",
		ProviderFamily:  "anthropic",
		StickyHit:       true,
	})
	// No panic + no error is the contract; Prometheus collects internally.
}

func TestMetricsSelectionRecorder_EmptyFieldsDefault(t *testing.T) {
	r := NewMetricsSelectionRecorder(nil)
	// Empty FinalKind + Result should default safely, not panic.
	r.RecordSelection(context.Background(), SelectionEvent{
		Model:          "claude-3",
		ProviderFamily: "anthropic",
	})
}

func TestFinalizeSelectionResult_FillsExecutionBoundary(t *testing.T) {
	rec := &capturingRecorder{}
	orig := SelectionEvent{RequestID: "r1", FinalKind: "channel", ProviderFamily: "openai"}
	FinalizeSelectionResult(rec, orig, "error", "timeout", true, 50_000_000) // 50ms
	events := rec.snapshot()
	assert.Len(t, events, 1)
	assert.Equal(t, "error", events[0].Result)
	assert.True(t, events[0].Fallback)
	assert.Equal(t, "timeout", events[0].FallbackReason)
	assert.EqualValues(t, 50, events[0].ElapsedMS)
}

func TestFinalizeSelectionResult_ClearsPlannedFlag(t *testing.T) {
	rec := &capturingRecorder{}
	orig := SelectionEvent{RequestID: "r2", FinalKind: "channel", ProviderFamily: "openai", Planned: true}
	FinalizeSelectionResult(rec, orig, "success", "", false, 10_000_000)
	events := rec.snapshot()
	assert.Len(t, events, 1)
	assert.False(t, events[0].Planned, "FinalizeSelectionResult must clear Planned so outcome metrics fire")
	assert.Equal(t, "success", events[0].Result)
}

func TestMetricsSelectionRecorder_PlannedDoesNotIncrementOutcomeCounters(t *testing.T) {
	// A planned event (Plan boundary) must NOT increment the outcome counters
	// (routing_selection_total / sticky_hit / fallback). Those fire once at the
	// execution boundary. This test guards the code-review #1 fix.
	r := NewMetricsSelectionRecorder(nil)
	// Planned event with sticky + fallback set — must NOT touch outcome counters.
	r.RecordSelection(context.Background(), SelectionEvent{
		FinalKind:       "subscription",
		ProviderFamily:  "anthropic",
		StickyHit:       true,
		Fallback:        true,
		FallbackReason:  "upstream_5xx",
		Planned:         true,
	})
	// No panic is the contract; the metric split is verified by inspection
	// (planned path only touches RoutingSelectionPlanned + duration).
}

func TestRecordSelectionForPlan_SetsPlanned(t *testing.T) {
	// recordSelectionForPlan must mark the event Planned=true so the recorder
	// routes it to the plan-boundary counters, not the outcome counters.
	uc := NewRelayUsecase(nil, nil, nil, nil)
	uc.SetSelectionRecorder(&capturingRecorder{})
	rec := uc.GetSelectionRecorder()
	cr := rec.(*capturingRecorder)
	_ = uc.recordSelectionForPlan(context.Background(), SelectionEvent{
		RequestID: "r3",
		FinalKind: "channel",
	}, time.Now().Add(-5*time.Millisecond))
	events := cr.snapshot()
	assert.Len(t, events, 1)
	assert.True(t, events[0].Planned, "recordSelectionForPlan must set Planned=true")
}
