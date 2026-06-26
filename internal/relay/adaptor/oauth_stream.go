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
