package server

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

// TestResponsesRequestToAnthropicBodyMapsSimple verifies the Responses→Anthropic
// request conversion path used by the Anthropic fallback for type=2 channels.
func TestResponsesRequestToAnthropicBodyMapsSimple(t *testing.T) {
	body, stream, err := responsesRequestToAnthropicBody([]byte(`{"model":"Kimi-K2.7-Code","input":"hi","max_output_tokens":64,"stream":true}`))
	if err != nil {
		t.Fatalf("responsesRequestToAnthropicBody error: %v", err)
	}
	if !stream {
		t.Fatal("stream = false, want true")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode anthropic body: %v, body=%s", err, string(body))
	}
	if got := payload["model"]; got != "Kimi-K2.7-Code" {
		t.Fatalf("model = %#v, want Kimi-K2.7-Code; body=%s", got, string(body))
	}
	if got := payload["max_tokens"]; got != float64(64) {
		t.Fatalf("max_tokens = %#v, want 64; body=%s", got, string(body))
	}
	msgs, ok := payload["messages"].([]interface{})
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages mismatch: %#v body=%s", payload["messages"], string(body))
	}
	msg := msgs[0].(map[string]interface{})
	if msg["role"] != "user" {
		t.Fatalf("role = %#v, want user; body=%s", msg["role"], string(body))
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("anthropic body should not include max_output_tokens: %s", string(body))
	}
}

func TestResponsesRequestToAnthropicBodyKeepsInstructionsAndStripsReasoningExtensions(t *testing.T) {
	body, _, err := responsesRequestToAnthropicBody([]byte(`{
		"model":"Kimi-K2.7-Code",
		"instructions":"Follow the repository instructions.",
		"input":"hi",
		"reasoning":{"effort":"high"},
		"stream":true
	}`))
	if err != nil {
		t.Fatalf("responsesRequestToAnthropicBody error: %v", err)
	}
	var payload struct {
		System       string          `json:"system"`
		Thinking     json.RawMessage `json:"thinking"`
		OutputConfig json.RawMessage `json:"output_config"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode anthropic body: %v, body=%s", err, string(body))
	}
	if payload.System != "Follow the repository instructions." {
		t.Fatalf("system = %q, want Codex instructions; body=%s", payload.System, string(body))
	}
	if len(payload.Thinking) != 0 {
		t.Fatalf("thinking should be omitted for compatible API-key upstreams; body=%s", string(body))
	}
	if len(payload.OutputConfig) != 0 {
		t.Fatalf("output_config should be omitted for compatible API-key upstreams; body=%s", string(body))
	}
}

func TestResponsesRequestToAnthropicBodyNormalizesCodexTools(t *testing.T) {
	body, _, err := responsesRequestToAnthropicBody([]byte(`{
		"model":"Kimi-K2.7-Code",
		"input":"hi",
		"tools":[
			{"type":"function","name":"exec_command","parameters":{"type":"object"}},
			{"type":"web_search","name":"web_search"},
			{"type":"multi_agent_v1","name":"multi_agent_v1"}
		]
	}`))
	if err != nil {
		t.Fatalf("responsesRequestToAnthropicBody error: %v", err)
	}
	var payload struct {
		Tools []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode anthropic body: %v, body=%s", err, string(body))
	}
	if len(payload.Tools) != 3 {
		t.Fatalf("tools = %d, want 3; body=%s", len(payload.Tools), string(body))
	}
	for _, tool := range payload.Tools {
		if tool.Type != "" {
			t.Fatalf("tool %q has unsupported type %q; body=%s", tool.Name, tool.Type, string(body))
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("tool %q input_schema is not an object: %s", tool.Name, string(tool.InputSchema))
		}
		if schema["type"] != "object" {
			t.Fatalf("tool %q input_schema.type = %#v; body=%s", tool.Name, schema["type"], string(body))
		}
	}
}

// TestAnthropicResponseToResponsesConvertsText verifies the non-streaming
// Anthropic→Responses response conversion.
func TestAnthropicResponseToResponsesConvertsText(t *testing.T) {
	anthropicBody := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"Kimi-K2.7-Code","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":7}}`)
	out, usage, err := anthropicResponseToResponses(anthropicBody)
	if err != nil {
		t.Fatalf("anthropicResponseToResponses error: %v", err)
	}
	if usage.PromptTokens != 5 || usage.CompletionTokens != 7 || usage.TotalTokens != 12 {
		t.Fatalf("usage = %#v, want {5,7,12}", usage)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode responses body: %v, body=%s", err, string(out))
	}
	if resp["object"] != "response" {
		t.Fatalf("object = %#v, want response; body=%s", resp["object"], string(out))
	}
	output, _ := resp["output"].([]interface{})
	if len(output) == 0 {
		t.Fatalf("output empty; body=%s", string(out))
	}
	first := output[0].(map[string]interface{})
	if first["type"] != "message" {
		t.Fatalf("first output type = %#v, want message; body=%s", first["type"], string(out))
	}
}

// TestSSEAnthropicDataExtraction verifies the SSE data-line parser used by the
// Anthropic stream→Responses stream bridge.
func TestSSEAnthropicDataExtraction(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{"event: message_start", "", false},
		{"data: {\"type\":\"message_start\"}", `{"type":"message_start"}`, true},
		{"data:{\"type\":\"message_start\"}", `{"type":"message_start"}`, true},
		{"data:\t{\"type\":\"message_start\"}", `{"type":"message_start"}`, true},
		{"data: [DONE]", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := sseAnthropicData(c.line)
		if ok != c.ok {
			t.Errorf("sseAnthropicData(%q) ok = %v, want %v", c.line, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("sseAnthropicData(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

// TestIsAnthropicAPIKeyChannel reports whether a channel is type=2.
func TestIsAnthropicAPIKeyChannel(t *testing.T) {
	if !isAnthropicAPIKeyChannel(&relaybiz.Channel{Type: relayprovider.ChannelTypeAnthropic}) {
		t.Fatal("type=2 should be anthropic api-key channel")
	}
	if isAnthropicAPIKeyChannel(&relaybiz.Channel{Type: relayprovider.ChannelTypeOpenAI}) {
		t.Fatal("type=1 should not be anthropic api-key channel")
	}
	if isAnthropicAPIKeyChannel(nil) {
		t.Fatal("nil channel should not be anthropic api-key channel")
	}
}

// TestTransformAnthropicStreamToResponsesBridgesSSE verifies the streaming
// Anthropic→Responses bridge emits response.created + output_text deltas.
func TestTransformAnthropicStreamToResponsesBridgesSSE(t *testing.T) {
	anthropicSSE := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"Kimi-K2.7-Code","usage":{"input_tokens":2,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"input_tokens":0}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	src := &relayprovider.RawStreamResponse{
		StatusCode: 200,
		Header:     map[string][]string{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(anthropicSSE)),
	}
	transformed := transformAnthropicStreamToResponses(src)
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := transformed.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	out := string(buf)
	if !strings.Contains(out, "response.created") {
		t.Fatalf("missing response.created in stream: %s", out)
	}
	if !strings.Contains(out, `"delta":"hi"`) {
		t.Fatalf("missing text delta 'hi' in stream: %s", out)
	}
	if !strings.Contains(out, "response.content_part.added") || !strings.Contains(out, "response.content_part.done") {
		t.Fatalf("missing output_text content-part lifecycle: %s", out)
	}
	if !strings.Contains(out, `"text":"hi"`) {
		t.Fatalf("completed text was not accumulated: %s", out)
	}
	if !strings.Contains(out, "response.completed") {
		t.Fatalf("missing response.completed in stream: %s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing [DONE] sentinel in stream: %s", out)
	}
}

// TestTransformAnthropicStreamPrematureCloseBeforeMessageStart (CR 2026-08-05)
// reproduces "stream closed before response.completed": when the upstream
// Anthropic stream ends before the first message_start event arrives,
// FinalizeAnthropicResponsesStream returns nil (CreatedSent=false), so the
// goroutine closes the pipe WITHOUT emitting any terminal event. The codex
// client then reports "stream disconnected before completion". The fix must
// guarantee a terminal event (response.failed) + [DONE] even in this case.
func TestTransformAnthropicStreamPrematureCloseBeforeMessageStart(t *testing.T) {
	// Upstream sends nothing then closes — simulates kimi engine overload
	// dropping the SSE connection before any event frame.
	src := &relayprovider.RawStreamResponse{
		StatusCode: 200,
		Header:     map[string][]string{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	transformed := transformAnthropicStreamToResponses(src)
	out := drainStreamResponse(transformed)
	if !strings.Contains(out, "response.failed") && !strings.Contains(out, "response.completed") {
		t.Fatalf("upstream closed before message_start: expected a terminal event (response.failed or response.completed), got stream:\n%s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("upstream closed before message_start: expected [DONE] sentinel, got stream:\n%s", out)
	}
}

// TestTransformAnthropicStreamScannerErrorEmitsTerminalAndDone (CR 2026-08-05)
// reproduces the missing-[DONE] bug in the scanner.Err() branch: when the
// upstream returns a read error mid-stream, the goroutine sent response.failed
// but returned without the [DONE] sentinel, leaving the client waiting.
func TestTransformAnthropicStreamScannerErrorEmitsTerminalAndDone(t *testing.T) {
	// A reader that returns a read error after partial data simulates a
	// mid-stream TCP reset (kimi overload dropping the connection).
	src := &relayprovider.RawStreamResponse{
		StatusCode: 200,
		Header:     map[string][]string{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(&errorAfterReader{data: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"k3\",\"usage\":{\"input_tokens\":10}}}\n\n"), err: io.ErrUnexpectedEOF}),
	}
	transformed := transformAnthropicStreamToResponses(src)
	out := drainStreamResponse(transformed)
	if !strings.Contains(out, "response.failed") {
		t.Fatalf("scanner error: expected response.failed, got stream:\n%s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("scanner error: expected [DONE] sentinel after response.failed, got stream:\n%s", out)
	}
}

// TestTransformAnthropicStreamCompletedThenScannerError (CR 2026-08-05) guards
// the boundary where the upstream reaches a normal terminal state
// (message_start + message_stop => response.completed) and only THEN the
// connection errors. The scanner.Err() branch must NOT emit a second terminal
// event (response.failed after response.completed) — a completed-then-failed
// pair is contradictory for the client.
func TestTransformAnthropicStreamCompletedThenScannerError(t *testing.T) {
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"k3\",\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	src := &relayprovider.RawStreamResponse{
		StatusCode: 200,
		Header:     map[string][]string{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(&errorAfterReader{data: []byte(body), err: io.ErrUnexpectedEOF}),
	}
	transformed := transformAnthropicStreamToResponses(src)
	out := drainStreamResponse(transformed)
	if !strings.Contains(out, "response.completed") {
		t.Fatalf("expected response.completed from message_stop, got stream:\n%s", out)
	}
	if strings.Contains(out, "response.failed") {
		t.Fatalf("DOUBLE TERMINAL: response.failed after response.completed, got stream:\n%s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("expected [DONE] sentinel, got stream:\n%s", out)
	}
}

// errorAfterReader returns its data then err on the next Read, simulating a
// truncated/mid-stream connection drop.
type errorAfterReader struct {
	data []byte
	err  error
	pos  int
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// drainStreamResponse reads a RawStreamResponse body to completion and returns
// the accumulated string.
func drainStreamResponse(resp *relayprovider.RawStreamResponse) string {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}
