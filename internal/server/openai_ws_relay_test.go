package server

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
)

func TestIsOpenAIWSTerminalEvent(t *testing.T) {
	terminals := []string{"response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled"}
	for _, evt := range terminals {
		if !isOpenAIWSTerminalEvent(evt) {
			t.Errorf("expected %q to be terminal", evt)
		}
		if !isOpenAIWSTerminalEvent(" " + evt + " ") {
			t.Errorf("expected trimmed %q to be terminal", evt)
		}
	}
	nonTerminals := []string{"response.created", "response.output_text.delta", "response.in_progress", "", "rate_limits"}
	for _, evt := range nonTerminals {
		if isOpenAIWSTerminalEvent(evt) {
			t.Errorf("expected %q to NOT be terminal", evt)
		}
	}
}

func TestShouldParseOpenAIWSUsage(t *testing.T) {
	for _, evt := range []string{"response.completed", "response.done", "response.failed", "response.cancelled"} {
		if !shouldParseOpenAIWSUsage(evt) {
			t.Errorf("expected %q to carry usage", evt)
		}
	}
	for _, evt := range []string{"response.created", "response.in_progress", ""} {
		if shouldParseOpenAIWSUsage(evt) {
			t.Errorf("expected %q to NOT carry usage", evt)
		}
	}
}

func TestOpenAIWSTerminalModelHealth(t *testing.T) {
	tests := []struct {
		name    string
		turn    openAIWSTurnResult
		record  bool
		success bool
	}{
		{name: "completed", turn: openAIWSTurnResult{terminalEventType: "response.completed"}, record: true, success: true},
		{name: "done without status", turn: openAIWSTurnResult{terminalEventType: "response.done"}, record: true, success: true},
		{name: "done with completed status", turn: openAIWSTurnResult{terminalEventType: "response.done", responseStatus: "completed"}, record: true, success: true},
		{name: "done with failed status", turn: openAIWSTurnResult{terminalEventType: "response.done", responseStatus: "failed"}, record: true, success: false},
		{name: "done with incomplete status stays neutral", turn: openAIWSTurnResult{terminalEventType: "response.done", responseStatus: "incomplete"}},
		{name: "failed without error code", turn: openAIWSTurnResult{terminalEventType: "response.failed"}, record: true, success: false},
		{name: "failed with server error code", turn: openAIWSTurnResult{terminalEventType: "response.failed", errorCode: "server_error", errorMessage: "internal error"}, record: true, success: false},
		{name: "failed content policy is client-caused", turn: openAIWSTurnResult{terminalEventType: "response.failed", errorCode: "content_policy_violation", errorMessage: "output blocked"}},
		{name: "failed moderation via message", turn: openAIWSTurnResult{terminalEventType: "response.failed", errorMessage: "prompt flagged by moderation system"}},
		{name: "failed sensitive words via message", turn: openAIWSTurnResult{terminalEventType: "response.failed", errorCode: "upstream_error", errorMessage: "sensitive_words_detected"}},
		{name: "failed invalid request is client-caused", turn: openAIWSTurnResult{terminalEventType: "response.failed", errorCode: "invalid_request_error"}},
		{name: "incomplete", turn: openAIWSTurnResult{terminalEventType: "response.incomplete"}},
		{name: "cancelled", turn: openAIWSTurnResult{terminalEventType: "response.cancelled"}},
		{name: "canceled", turn: openAIWSTurnResult{terminalEventType: "response.canceled"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, success := openAIWSTerminalModelHealth(tt.turn)
			if record != tt.record || success != tt.success {
				t.Fatalf("openAIWSTerminalModelHealth(%+v) = (%v, %v), want (%v, %v)", tt.turn, record, success, tt.record, tt.success)
			}
		})
	}
}

func TestParseOpenAIWSTerminalOutcome(t *testing.T) {
	outcome := parseOpenAIWSTerminalOutcome(map[string]any{
		"response": map[string]any{
			"id":     "resp_1",
			"status": " failed ",
			"error": map[string]any{
				"code":    " content_policy_violation ",
				"message": " blocked ",
			},
		},
	})
	if outcome.status != "failed" || outcome.errorCode != "content_policy_violation" || outcome.errorMessage != "blocked" {
		t.Fatalf("parseOpenAIWSTerminalOutcome() = %+v", outcome)
	}

	// Missing response object / error object must yield zero values, not panic.
	empty := parseOpenAIWSTerminalOutcome(map[string]any{"response_id": "resp_2"})
	if empty != (openAIWSTurnOutcome{}) {
		t.Fatalf("parseOpenAIWSTerminalOutcome(no response) = %+v", empty)
	}
}

// TestRelayOpenAIWSFramesTerminalOutcomePropagation asserts the terminal
// payload outcome (status/error) reaches onTurnComplete instead of being
// dropped at the parse boundary, and does not leak into the next turn.
func TestRelayOpenAIWSFramesTerminalOutcomePropagation(t *testing.T) {
	clientConn := newFakeFrameConn(8)
	upstreamConn := newFakeFrameConn(8)

	created1 := []byte(`{"type":"response.created","response":{"id":"resp_a","status":"in_progress"}}`)
	doneFailed := []byte(`{"type":"response.done","response":{"id":"resp_a","status":"failed","error":{"code":"server_busy","message":"overloaded"},"usage":{"input_tokens":10,"output_tokens":5}}}`)
	created2 := []byte(`{"type":"response.created","response":{"id":"resp_b","status":"in_progress"}}`)
	completed2 := []byte(`{"type":"response.completed","response":{"id":"resp_b","status":"completed","usage":{"input_tokens":20,"output_tokens":2}}}`)
	go func() {
		go func() {
			for range upstreamConn.out {
			}
		}()
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: created1}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: doneFailed}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: created2}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: completed2}
		close(upstreamConn.in)
		go func() {
			for range clientConn.out {
			}
		}()
	}()

	var turns []openAIWSTurnResult
	opts := openAIWSRelayOptions{
		writeTimeout:   time.Second,
		idleTimeout:    2 * time.Second,
		onTurnComplete: func(turn openAIWSTurnResult) { turns = append(turns, turn) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, exit := relayOpenAIWSFrames(ctx, clientConn, upstreamConn, []byte(`{"type":"response.create","model":"gpt-5"}`), opts)
	if exit == nil {
		t.Fatal("expected non-nil exit")
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turn callbacks, got %d (exit=%+v)", len(turns), exit)
	}
	if turns[0].responseStatus != "failed" || turns[0].errorCode != "server_busy" || turns[0].errorMessage != "overloaded" {
		t.Fatalf("turn 0 outcome not propagated: %+v", turns[0])
	}
	if turns[1].responseStatus != "completed" || turns[1].errorCode != "" {
		t.Fatalf("turn 1 outcome leaked from previous turn: %+v", turns[1])
	}
	if record, success := openAIWSTerminalModelHealth(turns[0]); !record || success {
		t.Fatalf("response.done + failed status must classify as failure, got record=%v success=%v", record, success)
	}
}

func TestParseOpenAIWSFrameUsage(t *testing.T) {
	t.Run("input_tokens aliases", func(t *testing.T) {
		frame := map[string]any{
			"response": map[string]any{
				"usage": map[string]any{
					"input_tokens":  float64(120),
					"output_tokens": float64(40),
					"input_tokens_details": map[string]any{
						"cached_tokens": float64(10),
					},
				},
			},
		}
		u, ok := parseOpenAIWSFrameUsage(frame)
		if !ok {
			t.Fatal("expected usage parsed")
		}
		if u.promptTokens != 120 || u.completionTokens != 40 || u.cacheReadTokens != 10 || u.totalTokens != 160 {
			t.Errorf("unexpected usage: %+v", u)
		}
	})
	t.Run("prompt_tokens aliases", func(t *testing.T) {
		frame := map[string]any{
			"response": map[string]any{
				"usage": map[string]any{
					"prompt_tokens":     float64(50),
					"completion_tokens": float64(20),
				},
			},
		}
		u, ok := parseOpenAIWSFrameUsage(frame)
		if !ok {
			t.Fatal("expected usage parsed")
		}
		if u.promptTokens != 50 || u.completionTokens != 20 || u.totalTokens != 70 {
			t.Errorf("unexpected usage: %+v", u)
		}
	})
	t.Run("no usage block", func(t *testing.T) {
		frame := map[string]any{
			"response": map[string]any{
				"status": "completed",
			},
		}
		if _, ok := parseOpenAIWSFrameUsage(frame); ok {
			t.Fatal("expected no usage")
		}
	})
	t.Run("zero tokens", func(t *testing.T) {
		frame := map[string]any{
			"response": map[string]any{
				"usage": map[string]any{
					"input_tokens":  float64(0),
					"output_tokens": float64(0),
				},
			},
		}
		if _, ok := parseOpenAIWSFrameUsage(frame); ok {
			t.Fatal("expected no usage for zero tokens")
		}
	})
}

func TestObserveUpstreamFrameAccumulatesUsage(t *testing.T) {
	state := newOpenAIWSRelayState()
	now := time.Now()

	completed := []byte(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":20}}}}`)
	eventType, responseID, terminal := state.observeUpstreamFrame(completed, coderws.MessageText, now)
	if eventType != "response.completed" || responseID != "resp_1" || !terminal {
		t.Fatalf("unexpected observe result: type=%s id=%s terminal=%v", eventType, responseID, terminal)
	}
	usage, _, _ := state.snapshot()
	if usage.promptTokens != 100 || usage.completionTokens != 50 || usage.cacheReadTokens != 20 {
		t.Errorf("unexpected accumulated usage: %+v", usage)
	}
}

// fakeFrameConn is an in-memory openAIWSFrameConn for relay tests. It uses two
// independent buffered channels: `in` feeds ReadFrame (simulating the peer
// sending frames to us) and `out` collects WriteFrame (frames we send to the
// peer). Keeping them decoupled lets a test inject upstream events into `in`
// while draining `out` without the send-on-closed races a loopback pair would
// introduce.
type fakeFrameConn struct {
	in     chan fakeFrame
	out    chan fakeFrame
	closed atomic.Bool
}

type fakeFrame struct {
	msgType coderws.MessageType
	payload []byte
	err     error
}

func newFakeFrameConn(buf int) *fakeFrameConn {
	return &fakeFrameConn{
		in:  make(chan fakeFrame, buf),
		out: make(chan fakeFrame, buf),
	}
}

func (c *fakeFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	select {
	case f, ok := <-c.in:
		if !ok {
			return coderws.MessageText, nil, errors.New("read closed")
		}
		return f.msgType, f.payload, f.err
	case <-ctx.Done():
		return coderws.MessageText, nil, ctx.Err()
	}
}

func (c *fakeFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	select {
	case c.out <- fakeFrame{msgType: msgType, payload: payload}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeFrameConn) Close() error {
	c.closed.Swap(true)
	return nil
}

func TestRelayOpenAIWSFramesCompletesTerminalTurn(t *testing.T) {
	clientConn := newFakeFrameConn(8)
	upstreamConn := newFakeFrameConn(8)

	// Simulate the upstream emitting response.created then response.completed
	// (with usage), then closing. The relay must observe the terminal event,
	// fire the turn callback, and exit once the upstream read returns closed.
	created := []byte(`{"type":"response.created","response":{"id":"resp_42"}}`)
	completed := []byte(`{"type":"response.completed","response":{"id":"resp_42","usage":{"input_tokens":10,"output_tokens":5,"input_tokens_details":{"cached_tokens":2}}}}`)
	go func() {
		// Drain client->upstream frames (the first response.create) so
		// writeUpstream does not block.
		go func() {
			for range upstreamConn.out {
			}
		}()
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: created}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: completed}
		close(upstreamConn.in)
		// Drain downstream frames the relay writes to the client so writeClient
		// does not block.
		go func() {
			for range clientConn.out {
			}
		}()
	}()

	var turns []openAIWSTurnResult
	opts := openAIWSRelayOptions{
		writeTimeout:   time.Second,
		idleTimeout:    2 * time.Second,
		onTurnComplete: func(turn openAIWSTurnResult) { turns = append(turns, turn) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, exit := relayOpenAIWSFrames(ctx, clientConn, upstreamConn, []byte(`{"type":"response.create","model":"gpt-5"}`), opts)
	if exit == nil {
		t.Fatal("expected non-nil exit")
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn callback, got %d (exit=%+v)", len(turns), exit)
	}
	if turns[0].requestID != "resp_42" {
		t.Errorf("expected resp_42, got %s", turns[0].requestID)
	}
	if turns[0].usage.promptTokens != 10 || turns[0].usage.completionTokens != 5 {
		t.Errorf("unexpected turn usage: %+v", turns[0].usage)
	}
	if result.usage.promptTokens != 10 {
		t.Errorf("unexpected aggregate usage: %+v", result.usage)
	}
}

// TestRelayOpenAIWSFramesPerTurnUsageDelta pins the multi-turn billing
// contract: st.usage accumulates across the connection, but each turn callback
// must carry only that turn's usage. Handing every turn the cumulative
// snapshot would commit turn N against turns 1..N all over again.
func TestRelayOpenAIWSFramesPerTurnUsageDelta(t *testing.T) {
	clientConn := newFakeFrameConn(8)
	upstreamConn := newFakeFrameConn(8)

	created1 := []byte(`{"type":"response.created","response":{"id":"resp_1"}}`)
	completed1 := []byte(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5}}}`)
	created2 := []byte(`{"type":"response.created","response":{"id":"resp_2"}}`)
	completed2 := []byte(`{"type":"response.completed","response":{"id":"resp_2","usage":{"input_tokens":30,"output_tokens":7}}}`)
	go func() {
		go func() {
			for range upstreamConn.out {
			}
		}()
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: created1}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: completed1}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: created2}
		upstreamConn.in <- fakeFrame{msgType: coderws.MessageText, payload: completed2}
		close(upstreamConn.in)
		go func() {
			for range clientConn.out {
			}
		}()
	}()

	var turns []openAIWSTurnResult
	opts := openAIWSRelayOptions{
		writeTimeout:   time.Second,
		idleTimeout:    2 * time.Second,
		onTurnComplete: func(turn openAIWSTurnResult) { turns = append(turns, turn) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, exit := relayOpenAIWSFrames(ctx, clientConn, upstreamConn, []byte(`{"type":"response.create","model":"gpt-5"}`), opts)
	if exit == nil {
		t.Fatal("expected non-nil exit")
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turn callbacks, got %d (exit=%+v)", len(turns), exit)
	}
	if turns[0].usage.promptTokens != 10 || turns[0].usage.completionTokens != 5 {
		t.Errorf("unexpected turn 1 usage: %+v", turns[0].usage)
	}
	if turns[1].usage.promptTokens != 30 || turns[1].usage.completionTokens != 7 {
		t.Errorf("unexpected turn 2 usage (want delta 30/7, not cumulative 40/12): %+v", turns[1].usage)
	}
	// The aggregate snapshot stays cumulative for logging.
	if result.usage.promptTokens != 40 || result.usage.completionTokens != 12 {
		t.Errorf("unexpected aggregate usage: %+v", result.usage)
	}
}

func TestRelayOpenAIWSFramesIdleTimeout(t *testing.T) {
	clientConn := newFakeFrameConn(8)
	upstreamConn := newFakeFrameConn(8)

	// Drain both out channels so the first writeUpstream succeeds and the
	// pumps then block on reads with no activity, letting the idle watchdog
	// fire.
	go func() {
		for range upstreamConn.out {
		}
	}()
	go func() {
		for range clientConn.out {
		}
	}()

	opts := openAIWSRelayOptions{
		writeTimeout: 200 * time.Millisecond,
		idleTimeout:  150 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, exit := relayOpenAIWSFrames(ctx, clientConn, upstreamConn, []byte(`{"type":"response.create"}`), opts)
	if exit == nil {
		t.Fatal("expected non-nil exit")
	}
	if exit.stage != "idle_timeout" {
		t.Errorf("expected idle_timeout exit, got %s", exit.stage)
	}
	if !strings.Contains(exit.err.Error(), "idle") {
		t.Errorf("expected idle timeout error, got %v", exit.err)
	}
}

func TestIsOpenAIWSDisconnectError(t *testing.T) {
	if !isOpenAIWSDisconnectError(context.Canceled) {
		t.Error("expected context.Canceled to be a disconnect")
	}
	if !isOpenAIWSDisconnectError(errors.New("EOF")) {
		t.Error("expected EOF to be a disconnect")
	}
	if isOpenAIWSDisconnectError(errors.New("totally unrelated")) {
		t.Error("expected unrelated error to NOT be a disconnect")
	}
}
