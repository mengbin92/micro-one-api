package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	coderws "github.com/coder/websocket"
)

// openAIWSRelayOptions configures the bidirectional Responses WebSocket relay.
type openAIWSRelayOptions struct {
	writeTimeout    time.Duration
	idleTimeout     time.Duration
	onTurnComplete  func(turn openAIWSTurnResult)
	beforeWriteDown func(msgType coderws.MessageType, payload []byte) error
	readClientFrame func(ctx context.Context, conn openAIWSFrameConn) (coderws.MessageType, []byte, error)
}

// openAIWSRelayUsage is the per-turn / per-session usage accumulator. The field
// names mirror rawUsage so the same billing/log pipeline can consume it.
type openAIWSRelayUsage struct {
	promptTokens     int64
	completionTokens int64
	cacheReadTokens  int64
	totalTokens      int64
}

// openAIWSTurnResult is reported once per upstream terminal event
// (response.completed / response.done / response.failed / ...). The relay uses
// it to drive quota commit and usage logging.
type openAIWSTurnResult struct {
	requestID         string
	terminalEventType string
	usage             openAIWSRelayUsage
	duration          time.Duration
}

// openAIWSRelayResult is the aggregate result of a relay session across all
// turns.
type openAIWSRelayResult struct {
	usage             openAIWSRelayUsage
	lastRequestID     string
	terminalEventType string
	clientToUpstream  int64
	upstreamToClient  int64
	droppedDownstream int64
}

// openAIWSRelayExit describes why a relay session ended.
type openAIWSRelayExit struct {
	stage           string
	err             error
	graceful        bool
	wroteDownstream bool
}

// openAIWSTerminalEvents is the set of upstream events that signal the end of a
// response turn. Mirrors sub2api's isOpenAIWSTerminalEvent.
var openAIWSTerminalEvents = map[string]struct{}{
	"response.completed":  {},
	"response.done":       {},
	"response.failed":     {},
	"response.incomplete": {},
	"response.cancelled":  {},
	"response.canceled":   {},
}

// openAIWSUsageEvents is the set of upstream events that may carry usage in the
// response.usage field. Mirrors sub2api's openAIWSEventShouldParseUsage.
var openAIWSUsageEvents = map[string]struct{}{
	"response.completed":  {},
	"response.done":       {},
	"response.failed":     {},
	"response.incomplete": {},
	"response.cancelled":  {},
	"response.canceled":   {},
}

// openAIWSTokenEvents is the set of events that indicate streaming tokens have
// begun for the current turn. Used to compute first-token latency.
var openAIWSTokenEvents = map[string]struct{}{
	"response.created":           {},
	"response.in_progress":       {},
	"response.output_item.added": {},
	"response.output_item.done":  {},
}

func isOpenAIWSTerminalEvent(eventType string) bool {
	_, ok := openAIWSTerminalEvents[strings.TrimSpace(eventType)]
	return ok
}

func shouldParseOpenAIWSUsage(eventType string) bool {
	_, ok := openAIWSUsageEvents[strings.TrimSpace(eventType)]
	return ok
}

// openAIWSRelayState tracks the accumulated usage, request model and turn
// timing bookkeeping for a relay session.
type openAIWSRelayState struct {
	mu             sync.Mutex
	usage          openAIWSRelayUsage
	lastResponseID string
	terminalEvent  string
	turnStartByID  map[string]time.Time
}

func newOpenAIWSRelayState() *openAIWSRelayState {
	return &openAIWSRelayState{
		turnStartByID: make(map[string]time.Time, 8),
	}
}

// observeUpstreamFrame parses a single upstream text frame, extracts its event
// type / response id / usage, and accumulates state. Binary frames are passed
// through without parsing. Returns the parsed event (may be zero-value for
// binary or non-JSON frames).
func (st *openAIWSRelayState) observeUpstreamFrame(payload []byte, msgType coderws.MessageType, now time.Time) (eventType, responseID string, terminal bool) {
	if msgType != coderws.MessageText || len(payload) == 0 {
		return "", "", false
	}

	var frame map[string]interface{}
	if err := sonic.Unmarshal(payload, &frame); err != nil {
		return "", "", false
	}

	rawType, _ := frame["type"].(string)
	eventType = strings.TrimSpace(rawType)

	// response.id is the canonical location; response_id is a legacy fallback.
	if response, ok := frame["response"].(map[string]interface{}); ok {
		if rid, _ := response["id"].(string); strings.TrimSpace(rid) != "" {
			responseID = strings.TrimSpace(rid)
		}
	}
	if responseID == "" {
		if rid, _ := frame["response_id"].(string); strings.TrimSpace(rid) != "" {
			responseID = strings.TrimSpace(rid)
		}
	}

	if eventType == "" {
		return "", "", false
	}

	// Record turn start when we first observe a response id so terminal-event
	// duration can be measured per-turn.
	if responseID != "" {
		st.mu.Lock()
		if _, ok := st.turnStartByID[responseID]; !ok {
			st.turnStartByID[responseID] = now
		}
		st.mu.Unlock()
	}

	if shouldParseOpenAIWSUsage(eventType) {
		if usage, ok := parseOpenAIWSFrameUsage(frame); ok {
			st.mu.Lock()
			st.usage.promptTokens += usage.promptTokens
			st.usage.completionTokens += usage.completionTokens
			st.usage.cacheReadTokens += usage.cacheReadTokens
			if usage.totalTokens > 0 {
				st.usage.totalTokens += usage.totalTokens
			} else {
				st.usage.totalTokens += usage.promptTokens + usage.completionTokens
			}
			st.mu.Unlock()
		}
	}

	if isOpenAIWSTerminalEvent(eventType) {
		terminal = true
		st.mu.Lock()
		st.terminalEvent = eventType
		if responseID != "" {
			st.lastResponseID = responseID
		}
		st.mu.Unlock()
	}
	return eventType, responseID, terminal
}

// finishTurn reports a completed turn through the onTurnComplete callback. It
// computes the turn duration from the recorded start time for this response id.
func (st *openAIWSRelayState) finishTurn(opts *openAIWSRelayOptions, eventType, responseID string, now time.Time) {
	if opts == nil || opts.onTurnComplete == nil || responseID == "" {
		return
	}
	var duration time.Duration
	st.mu.Lock()
	if startAt, ok := st.turnStartByID[responseID]; ok {
		duration = now.Sub(startAt)
		if duration < 0 {
			duration = 0
		}
		delete(st.turnStartByID, responseID)
	}
	usage := st.usage
	st.mu.Unlock()
	if duration == 0 {
		// No recorded start (e.g. terminal event without a prior response id
		// frame): do not emit a misleading duration.
		return
	}
	opts.onTurnComplete(openAIWSTurnResult{
		requestID:         responseID,
		terminalEventType: eventType,
		usage:             usage,
		duration:          duration,
	})
}

// snapshot returns the accumulated usage / terminal event for relayResult.
func (st *openAIWSRelayState) snapshot() (openAIWSRelayUsage, string, string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.usage, st.lastResponseID, st.terminalEvent
}

// parseOpenAIWSFrameUsage extracts usage fields from a terminal event frame.
// It reads response.usage.{input_tokens,output_tokens,input_tokens_details.cached_tokens},
// falling back to the prompt_tokens / completion_tokens aliases. Mirrors
// sub2api's parseUsageAndAccumulate field selection.
func parseOpenAIWSFrameUsage(frame map[string]interface{}) (openAIWSRelayUsage, bool) {
	response, ok := frame["response"].(map[string]interface{})
	if !ok {
		return openAIWSRelayUsage{}, false
	}
	usageMap, ok := response["usage"].(map[string]interface{})
	if !ok {
		return openAIWSRelayUsage{}, false
	}

	inputTokens := numberField(usageMap, "input_tokens", "prompt_tokens")
	outputTokens := numberField(usageMap, "output_tokens", "completion_tokens")
	cachedTokens := cacheReadTokensFromUsageMap(usageMap)

	if inputTokens == 0 && outputTokens == 0 {
		return openAIWSRelayUsage{}, false
	}
	total := inputTokens + outputTokens
	return openAIWSRelayUsage{
		promptTokens:     inputTokens,
		completionTokens: outputTokens,
		cacheReadTokens:  cachedTokens,
		totalTokens:      total,
	}, true
}

// relayOpenAIWSFrames runs the bidirectional pump between the client and
// upstream WebSocket connections. It blocks until either side closes, an idle
// timeout fires, or the context is cancelled. It mirrors sub2api's Relay
// function but is adapted to this server's FrameConn + rawUsage.
//
// The first client message (response.create) is written to the upstream before
// the pumps start, matching the Codex Responses WS handshake model.
func relayOpenAIWSFrames(
	ctx context.Context,
	clientConn openAIWSFrameConn,
	upstreamConn openAIWSFrameConn,
	firstClientMessage []byte,
	opts openAIWSRelayOptions,
) (openAIWSRelayResult, *openAIWSRelayExit) {
	result := openAIWSRelayResult{}
	if clientConn == nil || upstreamConn == nil {
		return result, &openAIWSRelayExit{stage: "relay_init", err: errors.New("relay connection is nil")}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	nowFn := time.Now
	writeTimeout := opts.writeTimeout
	if writeTimeout <= 0 {
		writeTimeout = 2 * time.Minute
	}
	idleTimeout := opts.idleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 5 * time.Minute
	}
	readClientFrame := opts.readClientFrame
	if readClientFrame == nil {
		readClientFrame = func(ctx context.Context, conn openAIWSFrameConn) (coderws.MessageType, []byte, error) {
			return conn.ReadFrame(ctx)
		}
	}

	state := newOpenAIWSRelayState()
	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	lastActivity := atomic.Int64{}
	lastActivity.Store(nowFn().UnixNano())
	markActivity := func() {
		lastActivity.Store(nowFn().UnixNano())
	}

	writeUpstream := func(msgType coderws.MessageType, payload []byte) error {
		writeCtx, cancel := context.WithTimeout(relayCtx, writeTimeout)
		defer cancel()
		return upstreamConn.WriteFrame(writeCtx, msgType, payload)
	}
	writeClient := func(msgType coderws.MessageType, payload []byte) error {
		writeCtx, cancel := context.WithTimeout(relayCtx, writeTimeout)
		defer cancel()
		return clientConn.WriteFrame(writeCtx, msgType, payload)
	}

	clientToUpstream := atomic.Int64{}
	upstreamToClient := atomic.Int64{}

	// Write the first response.create frame to the upstream before starting the
	// pumps (Codex Responses WS protocol expects the create event first).
	if err := writeUpstream(coderws.MessageText, firstClientMessage); err != nil {
		return result, &openAIWSRelayExit{stage: "write_first_upstream", err: err, wroteDownstream: false}
	}
	clientToUpstream.Add(1)
	markActivity()

	exitCh := make(chan openAIWSRelayExit, 3)

	// client -> upstream pump
	go func() {
		for {
			msgType, payload, err := readClientFrame(relayCtx, clientConn)
			if err != nil {
				exitCh <- openAIWSRelayExit{
					stage:    "read_client",
					err:      err,
					graceful: isOpenAIWSDisconnectError(err),
				}
				return
			}
			markActivity()
			if err := writeUpstream(msgType, payload); err != nil {
				exitCh <- openAIWSRelayExit{stage: "write_upstream", err: err}
				return
			}
			clientToUpstream.Add(1)
			markActivity()
		}
	}()

	// upstream -> client pump
	go func() {
		wroteDownstream := false
		for {
			msgType, payload, err := upstreamConn.ReadFrame(relayCtx)
			if err != nil {
				exitCh <- openAIWSRelayExit{
					stage:           "read_upstream",
					err:             err,
					graceful:        isOpenAIWSDisconnectError(err),
					wroteDownstream: wroteDownstream,
				}
				return
			}
			markActivity()

			if opts.beforeWriteDown != nil {
				if err := opts.beforeWriteDown(msgType, payload); err != nil {
					exitCh <- openAIWSRelayExit{
						stage:           "before_write_down",
						err:             err,
						wroteDownstream: wroteDownstream,
					}
					return
				}
			}

			if msgType == coderws.MessageText {
				eventType, responseID, terminal := state.observeUpstreamFrame(payload, msgType, nowFn())
				if terminal {
					state.finishTurn(&opts, eventType, responseID, nowFn())
				}
			}

			if err := writeClient(msgType, payload); err != nil {
				exitCh <- openAIWSRelayExit{
					stage:           "write_client",
					err:             err,
					wroteDownstream: wroteDownstream,
				}
				return
			}
			wroteDownstream = true
			upstreamToClient.Add(1)
			markActivity()
		}
	}()

	// idle watchdog
	go func() {
		ticker := time.NewTicker(idleTimeout / 4)
		defer ticker.Stop()
		for {
			select {
			case <-relayCtx.Done():
				return
			case <-ticker.C:
				last := time.Unix(0, lastActivity.Load())
				if time.Since(last) >= idleTimeout {
					exitCh <- openAIWSRelayExit{stage: "idle_timeout", err: errors.New("relay idle timeout"), graceful: false}
					return
				}
			}
		}
	}()

	exit := <-exitCh
	relayCancel()

	// Best-effort close both sides; the pump that errored already noticed.
	_ = upstreamConn.Close()
	_ = clientConn.Close()

	usage, lastID, terminal := state.snapshot()
	result.usage = usage
	result.lastRequestID = lastID
	result.terminalEventType = terminal
	result.clientToUpstream = clientToUpstream.Load()
	result.upstreamToClient = upstreamToClient.Load()
	return result, &exit
}

// isOpenAIWSDisconnectError reports whether an error represents a normal /
// graceful WebSocket disconnection (client closed the tab, sent a close frame,
// or the connection was torn down). Mirrors sub2api's isDisconnectError.
func isOpenAIWSDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if coderws.CloseStatus(err) != -1 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe")
}
