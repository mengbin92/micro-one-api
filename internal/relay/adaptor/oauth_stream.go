package adaptor

import (
	"bufio"
	"io"
	"strings"

	"github.com/bytedance/sonic"

	"micro-one-api/internal/relay/apicompat"
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
			FinishReason: strPtr("error"),
			Delta:        apicompat.ChatDelta{Role: "assistant", Content: strPtr(streamErrorMessage)},
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
		Delta: &apicompat.AnthropicDelta{
			Type: "error",
			Text: streamErrorMessage,
		},
	}
	if sse, err := apicompat.ResponsesAnthropicEventToSSE(evt); err == nil {
		_, _ = io.WriteString(w, sse)
	}
}

func strPtr(s string) *string { return &s }

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
		if err := sonic.UnmarshalString(data, &evt); err != nil {
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
		writeResponsesStreamError(w)
		return
	}
	for _, rse := range apicompat.FinalizeAnthropicResponsesStream(state) {
		sse, err := apicompat.ResponsesEventToSSE(rse)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(w, sse)
	}
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
		if err := sonic.UnmarshalString(data, &evt); err != nil {
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
		if err := sonic.UnmarshalString(data, &evt); err != nil {
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
		if err := sonic.UnmarshalString(data, &evt); err != nil {
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

// sseData extracts the JSON payload from a "data: ..." SSE line. Returns
// ok=false for non-data lines, empty data and the [DONE] sentinel.
func sseData(line string) (string, bool) {
	const prefix = "data: "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	data := strings.TrimSpace(line[len(prefix):])
	if data == "" || data == "[DONE]" {
		return "", false
	}
	return data, true
}
