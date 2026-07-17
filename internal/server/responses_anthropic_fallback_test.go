package server

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	relaybiz "micro-one-api/internal/biz"
	relayprovider "micro-one-api/domain/upstream/provider"
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
	if !strings.Contains(out, "response.completed") {
		t.Fatalf("missing response.completed in stream: %s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing [DONE] sentinel in stream: %s", out)
	}
}
