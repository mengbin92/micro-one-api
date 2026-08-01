package biz

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// capturingRecorder collects SelectionEvents for test assertions.
type capturingRecorder struct {
	mu     sync.Mutex
	events []SelectionEvent
}

func (r *capturingRecorder) RecordSelection(_ context.Context, e SelectionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *capturingRecorder) snapshot() []SelectionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SelectionEvent, len(r.events))
	copy(out, r.events)
	return out
}

func TestProviderFamilyForModel(t *testing.T) {
	cases := map[string]string{
		"gpt-4o":            "openai",
		"gpt-4o-mini":       "openai",
		"o1-preview":        "openai",
		"o3-mini":           "openai",
		"chatgpt-4o-latest": "openai",
		"claude-sonnet-4-5": "anthropic",
		"claude-3-opus":     "anthropic",
		"gemini-2.0-flash":  "google",
		"glm-5.2":           "zhipu",
		"deepseek-chat":     "deepseek",
		"qwen-max":          "alibaba",
		"unknown-model":     "other",
		"":                  "other",
	}
	for model, want := range cases {
		assert.Equal(t, want, ProviderFamilyForModel(model), "model=%q", model)
	}
}

func TestUpstreamRouteKindString(t *testing.T) {
	assert.Equal(t, "channel", UpstreamRouteChannel.String())
	assert.Equal(t, "subscription", UpstreamRouteSubscription.String())
	assert.Equal(t, "unknown", UpstreamRouteKind(99).String())
}

func TestNoopSelectionRecorderIsSafe(t *testing.T) {
	r := NewNoopSelectionRecorder()
	// Must not panic.
	r.RecordSelection(context.Background(), SelectionEvent{Model: "gpt-4o"})
}

func TestSelectionRecorderHolder_DefaultsToNoop(t *testing.T) {
	var h selectionRecorderHolder
	// get() on a zero holder returns a no-op recorder, not nil.
	r := h.get()
	assert.NotNil(t, r)
	r.RecordSelection(context.Background(), SelectionEvent{}) // no panic
}

func TestSelectionRecorderHolder_SetAndGet(t *testing.T) {
	var h selectionRecorderHolder
	rec := &capturingRecorder{}
	h.set(rec)
	h.get().RecordSelection(context.Background(), SelectionEvent{Model: "claude-3"})
	assert.Len(t, rec.snapshot(), 1)
}
