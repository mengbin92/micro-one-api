package adaptor

import (
	"bufio"
	"io"
	"strings"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/internal/apicompat"
)

// pumpAnthropicToResponses reads an Anthropic Messages SSE stream from src and
// writes a Responses SSE stream to w. It is the streaming bridge used by the
// ClaudeOAuthAdaptor when the client inbound format is Responses. The pipe
// writer is closed when the stream ends or an error occurs.

// streamError is emitted when the upstream SSE stream breaks mid-way (network
// disconnect or an oversized line exceeded the scanner buffer). Rather than
// silently finalizing as if the stream ended cleanly, we emit a terminal
// error event so the client knows the response was truncated.
const streamErrorMessage = "upstream stream interrupted"

// writeResponsesStreamError emits a Responses-style error event followed by
// the terminal "response.done" marker, then closes the pipe.
func writeResponsesStreamError(w *io.PipeWriter) {
	evt := apicompat.ResponsesStreamEvent{
		Type: "response.failed",
		Response: &apicompat.ResponsesResponse{
			Status: "failed",
			Error:  &apicompat.ResponsesError{Code: "stream_interrupted", Message: streamErrorMessage},
		},
	}
	if sse, err := apicompat.ResponsesEventToSSE(evt); err == nil {
		_, _ = io.WriteString(w, sse)
	}
}

// writeChatStreamError emits a ChatCompletions-style error chunk followed by
// the [DONE] sentinel, then closes the pipe.
func writeChatStreamError(w *io.PipeWriter) {
	chunk := apicompat.ChatCompletionsChunk{
		ID:      "chatcmpl-stream-error",
		Object:  "chat.completion.chunk",
		Created: 0,
		Model:   "",
		Choices: []apicompat.ChatChunkChoice{{
			Index:        0,
			FinishReason: new("error"),
			Delta:        apicompat.ChatDelta{Role: "assistant", Content: new(streamErrorMessage)},
		}},
	}
	if sse, err := apicompat.ChatChunkToSSE(chunk); err == nil {
		_, _ = io.WriteString(w, sse)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// writeAnthropicStreamError emits an Anthropic-style error event then closes
// the pipe.
func writeAnthropicStreamError(w *io.PipeWriter) {
	evt := apicompat.AnthropicStreamEvent{
		Type: "error",
		Error: &apicompat.AnthropicError{
			Type:    "api_error",
			Message: streamErrorMessage,
		},
	}
	if sse, err := apicompat.ResponsesAnthropicEventToSSE(evt); err == nil {
		_, _ = io.WriteString(w, sse)
	}
}

//go:fix inline
func strPtr(s string) *string { return new(s) }

func pumpAnthropicToResponses(src io.Reader, w *io.PipeWriter) {
	defer w.Close()
	scanner := bufio.NewScanner(src)
	// SSE events can be large (reasoning deltas); raise the per-line cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	state := apicompat.NewAnthropicEventToResponsesState()
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := sseData(line)
		if !ok {
			continue
		}
		var evt apicompat.AnthropicStreamEvent
		if err := jsonx.UnmarshalFromString(data, &evt); err != nil {
			continue
		}
		for _, rse := range apicompat.AnthropicEventToResponsesEvents(&evt, state) {
			sse, err := apicompat.ResponsesEventToSSE(rse)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(w, sse); err != nil {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// Upstream stream broke mid-way (disconnect / oversized line). Emit a
		// terminal error event so the client knows the response was truncated,
		// then stop — do NOT emit synthetic finalize events that would imply a
		// clean stream end.
		// CR 2026-08-05: only emit response.failed when the stream did NOT
		// already reach a normal terminal state. If message_stop was processed
		// (response.completed already emitted) and only then the connection
		// errored, a second terminal event would be contradictory.
		if !state.CompletedSent {
			writeResponsesStreamError(w)
		}
		// CR 2026-08-05: the [DONE] sentinel MUST follow the terminal event so
		// the client knows the SSE stream has ended cleanly. Without it the
		// client keeps waiting for more data and reports "stream disconnected
		// before completion".
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	// CR 2026-08-05: if the upstream closed before message_start arrived,
	// FinalizeAnthropicResponsesStream returns nil (CreatedSent=false) and no
	// terminal event is emitted — the pipe closes silently and the client
	// reports "stream closed before response.completed". Synthesise a
	// response.failed so the stream always ends with a terminal event.
	terminalEvents := apicompat.FinalizeAnthropicResponsesStream(state)
	if len(terminalEvents) == 0 && !state.CreatedSent {
		terminalEvents = []apicompat.ResponsesStreamEvent{{
			Type: "response.failed",
			Response: &apicompat.ResponsesResponse{
				Status: "failed",
				Error:  &apicompat.ResponsesError{Code: "stream_interrupted", Message: "upstream stream closed before any event"},
			},
		}}
	}
	for _, rse := range terminalEvents {
		sse, err := apicompat.ResponsesEventToSSE(rse)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// pumpAnthropicToChat reads an Anthropic Messages SSE stream from src and
// writes a ChatCompletions SSE stream to w. It chains Anthropic→Responses and
// Responses→ChatCompletions conversions. Used by the ClaudeOAuthAdaptor when
// the client inbound format is ChatCompletions.
func pumpAnthropicToChat(src io.Reader, w *io.PipeWriter, model string) {
	defer w.Close()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	anthState := apicompat.NewAnthropicEventToResponsesState()
	chatState := apicompat.NewResponsesEventToChatState()
	chatState.Model = model
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := sseData(line)
		if !ok {
			continue
		}
		var evt apicompat.AnthropicStreamEvent
		if err := jsonx.UnmarshalFromString(data, &evt); err != nil {
			continue
		}
		for _, rse := range apicompat.AnthropicEventToResponsesEvents(&evt, anthState) {
			for _, chunk := range apicompat.ResponsesEventToChatChunks(&rse, chatState) {
				sse, err := apicompat.ChatChunkToSSE(chunk)
				if err != nil {
					continue
				}
				if _, err := io.WriteString(w, sse); err != nil {
					return
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		writeChatStreamError(w)
		return
	}
	// Finalize both chains.
	for _, rse := range apicompat.FinalizeAnthropicResponsesStream(anthState) {
		for _, chunk := range apicompat.ResponsesEventToChatChunks(&rse, chatState) {
			sse, err := apicompat.ChatChunkToSSE(chunk)
			if err != nil {
				continue
			}
			_, _ = io.WriteString(w, sse)
		}
	}
	for _, chunk := range apicompat.FinalizeResponsesChatStream(chatState) {
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// pumpResponsesToAnthropic reads a Responses SSE stream from src and writes an
// Anthropic Messages SSE stream to w. Used by the CodexOAuthAdaptor when the
// client inbound format is Anthropic Messages.
func pumpResponsesToAnthropic(src io.Reader, w *io.PipeWriter) {
	defer w.Close()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	state := apicompat.NewResponsesEventToAnthropicState()
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := sseData(line)
		if !ok {
			continue
		}
		var evt apicompat.ResponsesStreamEvent
		if err := jsonx.UnmarshalFromString(data, &evt); err != nil {
			continue
		}
		for _, ase := range apicompat.ResponsesEventToAnthropicEvents(&evt, state) {
			sse, err := apicompat.ResponsesAnthropicEventToSSE(ase)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(w, sse); err != nil {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		writeAnthropicStreamError(w)
		return
	}
	for _, ase := range apicompat.FinalizeResponsesAnthropicStream(state) {
		sse, err := apicompat.ResponsesAnthropicEventToSSE(ase)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
}

// pumpResponsesToChat reads a Responses SSE stream from src and writes a
// ChatCompletions SSE stream to w. Used by the CodexOAuthAdaptor when the
// client inbound format is ChatCompletions.
func pumpResponsesToChat(src io.Reader, w *io.PipeWriter, model string) {
	defer w.Close()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	state := apicompat.NewResponsesEventToChatState()
	state.Model = model
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := sseData(line)
		if !ok {
			continue
		}
		var evt apicompat.ResponsesStreamEvent
		if err := jsonx.UnmarshalFromString(data, &evt); err != nil {
			continue
		}
		for _, chunk := range apicompat.ResponsesEventToChatChunks(&evt, state) {
			sse, err := apicompat.ChatChunkToSSE(chunk)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(w, sse); err != nil {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		writeChatStreamError(w)
		return
	}
	for _, chunk := range apicompat.FinalizeResponsesChatStream(state) {
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// pumpChatToResponses reads an OpenAI Chat Completions SSE stream and writes
// Responses SSE. It is shared by API-key adaptors whose native wire protocol
// is Chat Completions.
func pumpChatToResponses(src io.Reader, w *io.PipeWriter, model string) {
	defer w.Close()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	state := apicompat.NewChatCompletionsToResponsesStreamState(model)
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		var chunk apicompat.ChatCompletionsChunk
		if err := jsonx.UnmarshalFromString(data, &chunk); err != nil {
			writeResponsesStreamError(w)
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		if chatChunkHasErrorFinish(&chunk) {
			writeResponsesStreamError(w)
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		for _, event := range apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, state) {
			sse, err := apicompat.ResponsesEventToSSE(event)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(w, sse); err != nil {
				return
			}
		}
	}
	if scanner.Err() != nil {
		writeResponsesStreamError(w)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	for _, event := range apicompat.FinalizeChatCompletionsResponsesStream(state) {
		sse, err := apicompat.ResponsesEventToSSE(event)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// pumpChatToAnthropic composes the Chat -> Responses -> Anthropic stream
// bridges. The Anthropic stage buffers tool calls so parallel OpenAI deltas
// are emitted as ordered, non-overlapping Anthropic content blocks.
func pumpChatToAnthropic(src io.Reader, w *io.PipeWriter, model string) {
	defer w.Close()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	chatState := apicompat.NewChatCompletionsToResponsesStreamState(model)
	anthropicState := apicompat.NewBufferedResponsesEventToAnthropicState()
	anthropicState.Model = model
	sawChunk := false
	sawDone := false

	emitResponsesEvent := func(event apicompat.ResponsesStreamEvent) bool {
		for _, anthropicEvent := range apicompat.ResponsesEventToAnthropicEvents(&event, anthropicState) {
			sse, err := apicompat.ResponsesAnthropicEventToSSE(anthropicEvent)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(w, sse); err != nil {
				return false
			}
		}
		return true
	}

	for scanner.Scan() {
		line := scanner.Text()
		if isSSEDone(line) {
			sawDone = true
			continue
		}
		data, ok := sseData(line)
		if !ok {
			continue
		}
		var chunk apicompat.ChatCompletionsChunk
		if err := jsonx.UnmarshalFromString(data, &chunk); err != nil {
			writeAnthropicStreamError(w)
			return
		}
		if chatChunkHasErrorFinish(&chunk) {
			writeAnthropicStreamError(w)
			return
		}
		sawChunk = true
		for _, event := range apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, chatState) {
			if !emitResponsesEvent(event) {
				return
			}
		}
	}
	if scanner.Err() != nil {
		writeAnthropicStreamError(w)
		return
	}
	// A clean TCP EOF is not a successful model response unless at least one
	// valid chunk plus either a finish_reason or an explicit [DONE] sentinel
	// were observed. Some compatible providers omit finish_reason but still
	// terminate correctly with [DONE]. A bare EOF remains an interruption.
	if !sawChunk || (chatState.FinishReason == "" && !sawDone) {
		writeAnthropicStreamError(w)
		return
	}
	if chatState.FinishReason == "" {
		chatState.FinishReason = "stop"
	}
	for _, event := range apicompat.FinalizeChatCompletionsResponsesStream(chatState) {
		if !emitResponsesEvent(event) {
			return
		}
	}
	for _, event := range apicompat.FinalizeResponsesAnthropicStream(anthropicState) {
		sse, err := apicompat.ResponsesAnthropicEventToSSE(event)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
}

func chatChunkHasErrorFinish(chunk *apicompat.ChatCompletionsChunk) bool {
	if chunk == nil {
		return false
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && *choice.FinishReason == "error" {
			return true
		}
	}
	return false
}

func isSSEDone(line string) bool {
	data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
	return ok && strings.TrimSpace(data) == "[DONE]"
}

// sseData extracts the JSON payload from a "data: ..." SSE line. Returns
// ok=false for non-data lines, empty data and the [DONE] sentinel.
func sseData(line string) (string, bool) {
	// CR 2026-08-05: the SSE spec allows an optional space after the colon
	// ("data: value" or "data:value"). Standard Anthropic uses "data: ";
	// some Anthropic-compatible vendors (e.g. kimi) emit "data:" without the
	// space. The stricter prefix check silently dropped every data line
	// from those vendors, so the converter saw an empty stream and never
	// emitted response.completed.
	const prefix = "data:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	data := strings.TrimSpace(line[len(prefix):])
	if data == "" || data == "[DONE]" {
		return "", false
	}
	return data, true
}
