package adaptor

import (
	"io"
	"micro-one-api/internal/apicompat"
	"net/http"
	"strings"
	"testing"

	"micro-one-api/pkg/jsonx"
)

func TestAnthropicAdaptorConvertsResponsesRequest(t *testing.T) {
	adaptor := NewAnthropicAdaptor(nil, nil)
	format, body, err := adaptor.ConvertRequest(&RelayContext{InboundFormat: FormatOpenAIResponses}, FormatOpenAIResponses, []byte(`{"model":"m","input":"ping"}`))
	if err != nil || format != FormatAnthropicMessages || !strings.Contains(string(body), `"messages"`) {
		t.Fatalf("protocol conversion: %s %s %v", format, body, err)
	}
}

func TestAnthropicAdaptorConvertsResponsesResponse(t *testing.T) {
	adaptor := NewAnthropicAdaptor(nil, nil)
	context := &RelayContext{InboundFormat: FormatOpenAIResponses}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{
			"id":"msg_step_1",
			"type":"message",
			"role":"assistant",
			"model":"step-explore",
			"content":[{"type":"text","text":"done"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":2}
		}`)),
	}

	format, convertedBody, err := adaptor.ConvertResponse(context, FormatAnthropicMessages, response)
	if err != nil {
		t.Fatalf("ConvertResponse() error = %v", err)
	}
	if format != FormatOpenAIResponses {
		t.Fatalf("format = %q, want %q", format, FormatOpenAIResponses)
	}

	var converted apicompat.ResponsesResponse
	if err := jsonx.Unmarshal(convertedBody, &converted); err != nil {
		t.Fatalf("unmarshal converted response: %v", err)
	}
	if converted.ID != "msg_step_1" || converted.Status != "completed" || converted.Model != "step-explore" {
		t.Fatalf("converted response = %#v", converted)
	}
	if converted.Usage == nil || converted.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", converted.Usage)
	}
	if len(converted.Output) != 1 || len(converted.Output[0].Content) != 1 || converted.Output[0].Content[0].Text != "done" {
		t.Fatalf("output = %#v", converted.Output)
	}
}

func TestAnthropicAdaptorConvertsResponsesStream(t *testing.T) {
	adaptor := NewAnthropicAdaptor(nil, nil)
	context := &RelayContext{InboundFormat: FormatOpenAIResponses}
	response := &http.Response{Body: io.NopCloser(strings.NewReader(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_step_stream","model":"step-explore","usage":{"input_tokens":5,"output_tokens":0}}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	))}

	format, reader, err := adaptor.ConvertStreamResponse(context, FormatAnthropicMessages, response)
	if err != nil {
		t.Fatalf("ConvertStreamResponse() error = %v", err)
	}
	if format != FormatOpenAIResponses {
		t.Fatalf("format = %q, want %q", format, FormatOpenAIResponses)
	}
	converted, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read converted stream: %v", err)
	}
	stream := string(converted)
	for _, expected := range []string{"response.created", "response.output_text.delta", "response.completed", "[DONE]"} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("stream missing %q:\n%s", expected, stream)
		}
	}
}
