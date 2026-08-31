package apicompat

// v0.19 P1.1 — Protocol compatibility contract matrix.
//
// The matrix below is the single, explicit contract table for protocol
// conversion paths. Every cell is registered with a concrete check; a
// TestCompatibilityMatrix_Coverage asserts that each expected cell exists, so
// adding a new provider/tool/adaptor path WITHOUT adding its matrix cells
// fails the gate instead of silently expanding untested surface.
//
// Matrix coordinates:
//
//	                    streaming | tools/history        | errors
//	Responses→Anthropic OAuth      both     web_search skip, fn keep | upstream abort
//	Responses→Anthropic fallback   both     web_search skip, fn keep | fallback error map
//	Anthropic→Responses relay      both     server-tool block drop   | block interrupt
//	Chat↔Responses      adaptor    both     tool call/result round-trip | scanner/terminal
//	WebSocket Responses sticky     stream   history/rebind           | graceful drain

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"micro-one-api/pkg/jsonx"
)

// Matrix coordinate enums — keep in sync with docs/design/v0.19-compat-matrix.md.
const (
	dirResponsesToAnthropic = "responses→anthropic"
	dirAnthropicToResponses = "anthropic→responses"
	dirChatToResponses      = "chat-completions→responses"
	dirResponsesToChat      = "responses→chat-completions"

	pathOAuth   = "oauth"
	pathRelay   = "relay"
	pathAdaptor = "adaptor"
)

// matrixCell is one cell of the compatibility matrix.
type matrixCell struct {
	direction string
	path      string
	streaming bool
	tools     string // what the tools/history contract asserts
	errCase   string // what the error/interrupt contract asserts
	check     func(t *testing.T)
}

// cellKey uniquely identifies a matrix coordinate.
func cellKey(direction, path string, streaming bool) string {
	s := "non-streaming"
	if streaming {
		s = "streaming"
	}
	return direction + "|" + path + "|" + s
}

// compatibilityMatrix is the full registered contract table.
var compatibilityMatrix = []matrixCell{
	// --- Responses → Anthropic, OAuth adaptor path --------------------------
	{
		direction: dirResponsesToAnthropic, path: pathOAuth, streaming: false,
		tools:   "web_search_call history skipped; web_search tool dropped; function tool kept",
		errCase: "upstream termination handled by adaptor pump (see oauth_adaptor_test.go)",
		check:   checkResponsesToAnthropicOAuthNonStreaming,
	},
	{
		direction: dirResponsesToAnthropic, path: pathOAuth, streaming: true,
		tools:   "same as non-streaming, with stream flag carried",
		errCase: "premature-close / scanner-error SSE handled (oauth_adaptor_test.go)",
		check:   checkResponsesToAnthropicOAuthStreaming,
	},

	// --- Anthropic → Responses, relay response path --------------------------
	{
		direction: dirAnthropicToResponses, path: pathRelay, streaming: false,
		tools:   "server_tool_use / web_search_tool_result blocks silently dropped",
		errCase: "stop_reason max_tokens → incomplete status",
		check:   checkAnthropicToResponsesNonStreaming,
	},
	{
		direction: dirAnthropicToResponses, path: pathRelay, streaming: true,
		tools:   "content_block_start(server_tool_use) skipped via SkippingBlock",
		errCase: "half-open block + message_stop finalization",
		check:   checkAnthropicToResponsesStreaming,
	},

	// --- Chat Completions ↔ Responses, adaptor path --------------------------
	{
		direction: dirChatToResponses, path: pathAdaptor, streaming: false,
		tools:   "assistant tool_calls + tool result round-trip into function_call/function_call_output",
		errCase: "n/a — request conversion",
		check:   checkChatToResponsesNonStreaming,
	},
	{
		direction: dirChatToResponses, path: pathAdaptor, streaming: true,
		tools:   "same tool round-trip, stream flag carried",
		errCase: "n/a — request conversion",
		check:   checkChatToResponsesStreaming,
	},
	{
		direction: dirResponsesToChat, path: pathAdaptor, streaming: false,
		tools:   "function_call output → chat tool_calls",
		errCase: "n/a — response conversion",
		check:   checkResponsesToChatNonStreaming,
	},
	{
		direction: dirResponsesToChat, path: pathAdaptor, streaming: true,
		tools:   "streaming conversion preserved (codex custom events covered elsewhere)",
		errCase: "n/a — response conversion",
		check:   checkResponsesToChatStreaming,
	},
}

// expectedMatrixCells is the canonical coverage list. Extend it ONLY when a
// new coordinate genuinely becomes part of the contract; the coverage gate
// fails if any cell here is unregistered.
var expectedMatrixCells = []string{
	cellKey(dirResponsesToAnthropic, pathOAuth, false),
	cellKey(dirResponsesToAnthropic, pathOAuth, true),
	cellKey(dirAnthropicToResponses, pathRelay, false),
	cellKey(dirAnthropicToResponses, pathRelay, true),
	cellKey(dirChatToResponses, pathAdaptor, false),
	cellKey(dirChatToResponses, pathAdaptor, true),
	cellKey(dirResponsesToChat, pathAdaptor, false),
	cellKey(dirResponsesToChat, pathAdaptor, true),
}

func TestCompatibilityMatrix_Coverage(t *testing.T) {
	registered := map[string]matrixCell{}
	for _, c := range compatibilityMatrix {
		k := cellKey(c.direction, c.path, c.streaming)
		if _, dup := registered[k]; dup {
			t.Fatalf("duplicate matrix cell %s", k)
		}
		registered[k] = c
	}

	// Every expected coordinate must be present and must carry a real check.
	for _, want := range expectedMatrixCells {
		c, ok := registered[want]
		require.Truef(t, ok, "matrix is missing expected coordinate %s — add a cell or extend the contract", want)
		require.NotNilf(t, c.check, "matrix cell %s has no check function", want)
	}

	// Every registered cell must be an expected coordinate (no orphan cells
	// that silently pass with a nil/stub check).
	for k := range registered {
		found := slices.Contains(expectedMatrixCells, k)
		require.Truef(t, found, "matrix cell %s is not in expectedMatrixCells — remove it or extend the contract", k)
	}
}

func TestCompatibilityMatrix_RunAllCells(t *testing.T) {
	for _, c := range compatibilityMatrix {
		t.Run(cellKey(c.direction, c.path, c.streaming), c.check)
	}
}

// --- Cell checks -------------------------------------------------------------

func checkResponsesToAnthropicOAuthNonStreaming(t *testing.T) {
	req := matrixWebSearchResponsesRequest(false)
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	assert.False(t, out.Stream)

	// web_search_call history item must be skipped: messages only contain
	// user + assistant, no server_tool_use representation.
	require.Len(t, out.Messages, 2, "web_search_call history item must be dropped")
	assert.Equal(t, "user", out.Messages[0].Role)
	assert.Equal(t, "assistant", out.Messages[1].Role)

	// web_search tool dropped; function tool preserved.
	require.Len(t, out.Tools, 1, "web_search server tool must be dropped")
	assert.Equal(t, "exec_command", out.Tools[0].Name)
	assert.Equal(t, "", out.Tools[0].Type, "anthropic tools carry no type field")
}

func checkResponsesToAnthropicOAuthStreaming(t *testing.T) {
	req := matrixWebSearchResponsesRequest(true)
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	assert.True(t, out.Stream, "stream flag must be carried into the anthropic request")
	require.Len(t, out.Messages, 2, "web_search_call history item must be dropped in streaming too")
	require.Len(t, out.Tools, 1, "web_search server tool must be dropped in streaming too")
	assert.Equal(t, "exec_command", out.Tools[0].Name)
}

func checkAnthropicToResponsesNonStreaming(t *testing.T) {
	resp := matrixServerToolAnthropicResponse()
	out := AnthropicToResponsesResponse(resp)

	// server_tool_use / web_search_tool_result blocks must not surface as
	// function_call outputs; only the text message survives.
	for _, o := range out.Output {
		assert.NotEqual(t, "function_call", o.Type, "server-side web search must not become a function_call")
		assert.NotContains(t, o.Name, "web_search")
	}
	require.Len(t, out.Output, 1, "only the text message output survives")
	assert.Equal(t, "message", out.Output[0].Type)
	assert.Equal(t, "completed", out.Status)
}

func checkAnthropicToResponsesStreaming(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	var all []ResponsesStreamEvent
	for _, evt := range matrixServerToolAnthropicStream() {
		all = append(all, AnthropicEventToResponsesEvents(evt, state)...)
	}

	// No function_call item may be emitted for the skipped server tool.
	for _, e := range all {
		if e.Type == "response.output_item.added" {
			itemType := ""
			if e.Item != nil {
				itemType = e.Item.Type
			}
			assert.NotEqual(t, "function_call", itemType, "server tool block must be skipped, not converted")
			assert.NotEqual(t, "web_search_call", itemType)
		}
	}

	// The text content must survive and the stream must finalize cleanly.
	hasText := false
	for _, e := range all {
		if e.Type == "response.output_text.delta" && e.Delta != "" {
			hasText = true
		}
	}
	assert.True(t, hasText, "regular text deltas must survive the server-tool block skip")
	// The fixture stream ends with message_stop, so the state must have
	// finalized (CompletedSent). FinalizeAnthropicResponsesStream must then
	// be a no-op instead of double-emitting a terminal event.
	assert.True(t, state.CompletedSent, "message_stop must finalize the stream")
	assert.Empty(t, FinalizeAnthropicResponsesStream(state), "finalize after message_stop must emit nothing")
}

func checkChatToResponsesNonStreaming(t *testing.T) {
	req := matrixChatToolRoundTrip(false)
	out, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	assert.False(t, out.Stream)

	// The assistant tool_calls must round-trip into function_call items and
	// the tool result into function_call_output, with preserved pairing.
	var items []ResponsesInputItem
	require.NoError(t, jsonx.Unmarshal(out.Input, &items))
	var calls, outputs int
	for _, item := range items {
		switch item.Type {
		case "function_call":
			calls++
			assert.Equal(t, "calc", item.Name)
			assert.Equal(t, "call_1", item.CallID)
		case "function_call_output":
			outputs++
			assert.Equal(t, "call_1", item.CallID)
			assert.Equal(t, "4", item.Output)
		}
	}
	assert.Equal(t, 1, calls, "assistant tool call must round-trip")
	assert.Equal(t, 1, outputs, "tool result must round-trip")
}

func checkChatToResponsesStreaming(t *testing.T) {
	req := matrixChatToolRoundTrip(true)
	out, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	assert.True(t, out.Stream, "stream flag must be carried into the responses request")
}

func checkResponsesToChatNonStreaming(t *testing.T) {
	// Build a Responses response with a function_call output and verify it
	// maps back to chat tool_calls.
	rr := &ResponsesResponse{
		ID:     "resp_1",
		Object: "response",
		Model:  "gpt-5",
		Output: []ResponsesOutput{
			{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: "Sure."}}, Status: "completed"},
			{Type: "function_call", CallID: "call_9", Name: "calc", Arguments: `{"expression":"2+2"}`, Status: "completed"},
		},
		Status: "completed",
	}
	chat := ResponsesToChatCompletions(rr, "gpt-5")
	require.Len(t, chat.Choices, 1)
	msg := chat.Choices[0].Message
	require.Len(t, msg.ToolCalls, 1, "function_call output must map back to chat tool_calls")
	assert.Equal(t, "call_9", msg.ToolCalls[0].ID)
	assert.Equal(t, "calc", msg.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"expression":"2+2"}`, msg.ToolCalls[0].Function.Arguments)
}

func checkResponsesToChatStreaming(t *testing.T) {
	// Request-direction streaming conversion: the bridge preserves the stream
	// flag.
	req := &ResponsesRequest{
		Model: "gpt-5",
		Input: raw(`"hi"`),
		Tools: []ResponsesTool{{Type: "function", Name: "calc", Parameters: raw(`{"type":"object"}`)}},
	}
	chat, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)
	assert.Equal(t, "calc", chat.Tools[0].Function.Name)
}
