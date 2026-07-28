package biz

import (
	"context"
	"testing"

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
