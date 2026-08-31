package server

// v0.19 P1.1 — Protocol compatibility contract matrix (server side).
//
// The apicompat package owns the pure converter matrix; this file registers
// the transport-layer coordinates that only exist here:
//
//	Responses→Anthropic fallback (API-key channels)
//	Responses→Chat fallback
//	WebSocket Responses sticky relay (terminal detection / graceful drain)
//
// The same coverage gate as apicompat: every expected coordinate must be
// registered with a real check, and every registered coordinate must be an
// expected one.

import (
	"slices"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

const (
	svcDirResponsesToAnthropic = "responses→anthropic"
	svcDirResponsesToChat      = "responses→chat"
	svcDirWSResponses          = "websocket-responses"

	svcPathFallback = "fallback"
	svcPathSticky   = "sticky"
)

type svcMatrixCell struct {
	direction string
	path      string
	streaming bool
	tools     string
	errCase   string
	check     func(t *testing.T)
}

func svcCellKey(direction, path string, streaming bool) string {
	s := "non-streaming"
	if streaming {
		s = "streaming"
	}
	return direction + "|" + path + "|" + s
}

var serverCompatibilityMatrix = []svcMatrixCell{
	{
		direction: svcDirResponsesToAnthropic, path: svcPathFallback, streaming: false,
		tools:   "web_search server tool dropped; function tool kept; messages converted",
		errCase: "model required guard",
		check:   checkFallbackResponsesToAnthropicNonStreaming,
	},
	{
		direction: svcDirResponsesToAnthropic, path: svcPathFallback, streaming: true,
		tools:   "same as non-streaming, stream flag carried",
		errCase: "scanner/terminal via transformAnthropicStreamToResponses (existing suite)",
		check:   checkFallbackResponsesToAnthropicStreaming,
	},
	{
		direction: svcDirResponsesToChat, path: svcPathFallback, streaming: false,
		tools:   "responses input → chat messages; web_search tool dropped",
		errCase: "n/a — request conversion",
		check:   checkFallbackResponsesToChatNonStreaming,
	},
	{
		direction: svcDirResponsesToChat, path: svcPathFallback, streaming: true,
		tools:   "stream + stream_options include_usage set",
		errCase: "terminal event synthesized in stream transform (existing suite)",
		check:   checkFallbackResponsesToChatStreaming,
	},
	{
		direction: svcDirWSResponses, path: svcPathSticky, streaming: true,
		tools:   "n/a — transport; terminal event types drive relay/billing",
		errCase: "graceful drain: healthz 503 + connection close (openai_ws_drain_test.go)",
		check:   checkWSStickyTerminalDetection,
	},
}

var expectedServerMatrixCells = []string{
	svcCellKey(svcDirResponsesToAnthropic, svcPathFallback, false),
	svcCellKey(svcDirResponsesToAnthropic, svcPathFallback, true),
	svcCellKey(svcDirResponsesToChat, svcPathFallback, false),
	svcCellKey(svcDirResponsesToChat, svcPathFallback, true),
	svcCellKey(svcDirWSResponses, svcPathSticky, true),
}

func TestServerCompatibilityMatrix_Coverage(t *testing.T) {
	registered := map[string]svcMatrixCell{}
	for _, c := range serverCompatibilityMatrix {
		k := svcCellKey(c.direction, c.path, c.streaming)
		if _, dup := registered[k]; dup {
			t.Fatalf("duplicate matrix cell %s", k)
		}
		registered[k] = c
	}
	for _, want := range expectedServerMatrixCells {
		c, ok := registered[want]
		require.Truef(t, ok, "server matrix is missing expected coordinate %s", want)
		require.NotNilf(t, c.check, "server matrix cell %s has no check", want)
	}
	for k := range registered {
		found := slices.Contains(expectedServerMatrixCells, k)
		require.Truef(t, found, "server matrix cell %s is not in expectedServerMatrixCells", k)
	}
}

func TestServerCompatibilityMatrix_RunAllCells(t *testing.T) {
	for _, c := range serverCompatibilityMatrix {
		t.Run(svcCellKey(c.direction, c.path, c.streaming), c.check)
	}
}

// --- Server-side cell checks ------------------------------------------------

func checkFallbackResponsesToAnthropicNonStreaming(t *testing.T) {
	body := []byte(`{
		"model":"Kimi-K2.7-Code",
		"instructions":"Be terse.",
		"input":[
			{"role":"user","content":"hi"},
			{"type":"web_search_call","call_id":"ws_1","name":"web_search"},
			{"role":"assistant","content":"done"}
		],
		"tools":[
			{"type":"function","name":"exec_command","parameters":{"type":"object"}},
			{"type":"web_search","name":"web_search"}
		]
	}`)
	out, stream, err := responsesRequestToAnthropicBody(body)
	require.NoError(t, err)
	assert.False(t, stream)

	var payload struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content jsonx.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"tools"`
	}
	require.NoError(t, jsonx.Unmarshal(out, &payload))
	require.Len(t, payload.Messages, 2, "web_search_call history item must be dropped in fallback too")
	require.Len(t, payload.Tools, 1, "web_search tool must be dropped")
	assert.Equal(t, "exec_command", payload.Tools[0].Name)
	assert.Equal(t, "", payload.Tools[0].Type, "fallback strips anthropic server-tool type identifiers")
}

func checkFallbackResponsesToAnthropicStreaming(t *testing.T) {
	body := []byte(`{
		"model":"Kimi-K2.7-Code",
		"input":"hi",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)
	out, stream, err := responsesRequestToAnthropicBody(body)
	require.NoError(t, err)
	assert.True(t, stream, "stream flag must be carried into the anthropic fallback body")
	var payload struct {
		Stream bool `json:"stream"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	require.NoError(t, jsonx.Unmarshal(out, &payload))
	assert.True(t, payload.Stream)
	require.Len(t, payload.Tools, 1)
	assert.Equal(t, "exec_command", payload.Tools[0].Name)
}

func checkFallbackResponsesToChatNonStreaming(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"role":"user","content":"hi"},
			{"type":"web_search_call","call_id":"ws_1","name":"web_search"}
		],
		"tools":[{"type":"web_search","name":"web_search"}]
	}`)
	out, stream, err := responsesRequestToChatCompletionsBody(body)
	require.NoError(t, err)
	assert.False(t, stream)

	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	}
	require.NoError(t, jsonx.Unmarshal(out, &payload))
	require.Len(t, payload.Messages, 1, "web_search_call item must not map to a chat message")
	assert.Equal(t, "user", payload.Messages[0].Role)
	assert.Empty(t, payload.Tools, "server-side web_search tool has no chat equivalent")
}

func checkFallbackResponsesToChatStreaming(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"hi","stream":true}`)
	out, stream, err := responsesRequestToChatCompletionsBody(body)
	require.NoError(t, err)
	assert.True(t, stream)
	var payload struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	require.NoError(t, jsonx.Unmarshal(out, &payload))
	assert.True(t, payload.Stream)
	assert.True(t, payload.StreamOptions.IncludeUsage,
		"streaming chat fallback must request usage for the Responses terminal event")
}

func checkWSStickyTerminalDetection(t *testing.T) {
	// Terminal event contract drives relay turn completion + billing commit.
	for _, evt := range []string{"response.completed", "response.failed", "response.cancelled", "response.done"} {
		assert.Truef(t, isOpenAIWSTerminalEvent(evt), "%s must be a terminal event", evt)
	}
	for _, evt := range []string{"response.created", "response.output_item.added", "response.output_text.delta"} {
		assert.Falsef(t, isOpenAIWSTerminalEvent(evt), "%s must NOT be a terminal event", evt)
	}

	// A response.completed frame must surface as terminal through the relay
	// state observer (billing/quota commit trigger).
	st := newOpenAIWSRelayState()
	_, _, terminal := st.observeUpstreamFrame(
		[]byte(`{"type":"response.completed","response":{"id":"resp_ws_1"}}`),
		coderws.MessageText, time.Now(),
	)
	assert.True(t, terminal, "response.completed frame must be detected as terminal")

	// Non-text frames (binary pings) must never be treated as events.
	_, _, terminal = st.observeUpstreamFrame(nil, coderws.MessageBinary, time.Now())
	assert.False(t, terminal)
}
