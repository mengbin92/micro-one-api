package server

import (
	"context"
	"time"

	relaybiz "micro-one-api/internal/biz"
)

// ── v0.11.0 Phase 3 §3.4: execution-boundary selection finalization ────────
//
// finalizeSelectionFromResult emits the execution-boundary half of the routing
// selection observation for the main relay handlers (chat/completions,
// responses, raw, anthropic). These handlers use RetryExecutor directly rather
// than the relayOrchestrator, so they must finalize the plan's SelectionEvent
// themselves after the retry loop returns.
//
// The retry ExecuteResult now carries explicit Fallback / FallbackReason /
// FirstErr fields (set by the executor when a genuine source switch happens),
// so the finalizer does NOT infer fallback from Attempt > 0. This correctly
// distinguishes same-source retries from real fallbacks (code review HIGH-1).
//
// This function is nil-safe and never blocks the hot path.

// finalizeSelectionFromResult finalizes the plan's SelectionEvent using the
// retry ExecuteResult. It is called after the retry executor returns.
func (s *HTTPServer) finalizeSelectionFromResult(plan *relaybiz.RelayPlan, result *relaybiz.ExecuteResult, latency time.Duration) {
	if s == nil || plan == nil || plan.SelectionEvent == nil {
		return
	}
	recorder := s.relayUsecase.GetSelectionRecorder()
	if recorder == nil {
		return
	}

	resultLabel := "success"
	fallback := false
	fallbackReason := ""

	if result != nil {
		// Use the executor's explicit fallback signal — NOT Attempt > 0.
		// A same-source retry is not a fallback; only a genuine source switch
		// counts (code review HIGH-1).
		fallback = result.Fallback
		fallbackReason = result.FallbackReason
		if result.Err != nil {
			resultLabel = classifyResultLabel(result.Err)
		}
		// Update the final source to the channel that actually served the
		// request (may differ from the Plan-time selection after a switch).
		if result.Channel != nil && result.Channel.ID > 0 {
			plan.SelectionEvent.FinalSourceID = result.Channel.ID
			if result.Channel.SubscriptionAccountID > 0 {
				plan.SelectionEvent.FinalKind = relaybiz.UpstreamRouteSubscription.String()
				plan.SelectionEvent.FinalSourceID = result.Channel.SubscriptionAccountID
			} else {
				plan.SelectionEvent.FinalKind = relaybiz.UpstreamRouteChannel.String()
			}
		}
	}

	relaybiz.FinalizeSelectionResult(recorder, *plan.SelectionEvent, resultLabel, fallbackReason, fallback, latency)
}

// classifyResultLabel maps an upstream error to the low-cardinality result
// label used by routing_selection_total. Client-side errors (4xx) are
// "client_error"; upstream failures (5xx, network, timeout) are "error".
func classifyResultLabel(err error) string {
	if err == nil {
		return "success"
	}
	status := relaybiz.UpstreamStatus(err)
	if status >= 400 && status < 500 {
		return "client_error"
	}
	return "error"
}

// finalizeSelectionDirect finalizes a SelectionEvent without a retry result.
// Used by the orchestrator path and the subscription adaptor path where the
// caller knows the outcome directly (not via RetryExecutor).
func (s *HTTPServer) finalizeSelectionDirect(plan *relaybiz.RelayPlan, resultLabel, fallbackReason string, fallback bool, finalSourceID int64, latency time.Duration) {
	if s == nil || plan == nil || plan.SelectionEvent == nil {
		return
	}
	recorder := s.relayUsecase.GetSelectionRecorder()
	if recorder == nil {
		return
	}
	if finalSourceID > 0 {
		plan.SelectionEvent.FinalSourceID = finalSourceID
	}
	relaybiz.FinalizeSelectionResult(recorder, *plan.SelectionEvent, resultLabel, fallbackReason, fallback, latency)
}

// finalizeSelectionFromResultCtx is a context-aware wrapper used by handlers
// that already hold a context (keeps the signature consistent with the
// orchestrator path).
func (s *HTTPServer) finalizeSelectionFromResultCtx(_ context.Context, plan *relaybiz.RelayPlan, result *relaybiz.ExecuteResult, latency time.Duration) {
	s.finalizeSelectionFromResult(plan, result, latency)
}
