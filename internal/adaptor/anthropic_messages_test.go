package adaptor

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func TestOpenAICompatibleAdaptor_AnthropicRoundTrip(t *testing.T) {
	adaptor := NewOpenAICompatibleAdaptor(nil, nil)
	context := &RelayContext{
		InboundFormat: FormatAnthropicMessages,
		ClientModel:   "client-model",
		ResolvedModel: "deepseek-chat",
		Channel: &relaybiz.Channel{
			Type: provider.ChannelTypeDeepSeek, BaseURL: "https://api.deepseek.com/v1", Key: "key",
		},
	}
	requestBody := []byte(`{
		"model":"client-model","max_tokens":32,"stop_sequences":["DONE"],
		"messages":[{"role":"user","content":"inspect"}],
		"tools":[{"name":"Read","input_schema":{"type":"object"}}]
	}`)
	format, converted, err := adaptor.ConvertRequest(context, FormatAnthropicMessages, requestBody)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	if format != FormatOpenAIChatCompletions {
		t.Fatalf("format = %q", format)
	}
	var chatRequest apicompat.ChatCompletionsRequest
	if err := jsonx.Unmarshal(converted, &chatRequest); err != nil {
		t.Fatalf("parse converted request: %v", err)
	}
	if chatRequest.Model != "deepseek-chat" || chatRequest.MaxTokens == nil || *chatRequest.MaxTokens != 32 {
		t.Fatalf("converted request model/max_tokens = %q/%v", chatRequest.Model, chatRequest.MaxTokens)
	}
	if len(chatRequest.Tools) != 1 || string(chatRequest.Stop) != `["DONE"]` {
		t.Fatalf("converted tools/stop = %#v/%s", chatRequest.Tools, chatRequest.Stop)
	}

	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
		"id":"chatcmpl-1","object":"chat.completion","model":"deepseek-chat",
		"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}
	}`))}
	outFormat, output, err := adaptor.ConvertResponse(context, format, response)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	if outFormat != FormatAnthropicMessages {
		t.Fatalf("out format = %q", outFormat)
	}
	var anthropicResponse apicompat.AnthropicResponse
	if err := jsonx.Unmarshal(output, &anthropicResponse); err != nil {
		t.Fatalf("parse anthropic response: %v", err)
	}
	if anthropicResponse.StopReason != "tool_use" || len(anthropicResponse.Content) != 1 {
		t.Fatalf("anthropic response = %#v", anthropicResponse)
	}
	tool := anthropicResponse.Content[0]
	if tool.Type != "tool_use" || tool.ID != "call_1" || tool.Name != "Read" || string(tool.Input) != `{"file_path":"README.md"}` {
		t.Fatalf("tool block = %#v", tool)
	}
}

func TestOpenAICompatibleAdaptor_UsesChannelDefaultBaseURL(t *testing.T) {
	adaptor := NewOpenAICompatibleAdaptor(nil, nil)
	context := &RelayContext{Channel: &relaybiz.Channel{Type: provider.ChannelTypeDeepSeek}}

	url, err := adaptor.GetUpstreamURL(context)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if url != "https://api.deepseek.com/v1/chat/completions" {
		t.Fatalf("url = %q", url)
	}
}

func TestGeminiAdaptor_AnthropicToolAndImageConversion(t *testing.T) {
	adaptor := NewGeminiAdaptor(nil, nil)
	context := &RelayContext{
		InboundFormat: FormatAnthropicMessages,
		ClientModel:   "client-gemini",
		ResolvedModel: "gemini-2.0-flash",
		Channel: &relaybiz.Channel{
			Type: provider.ChannelTypeGemini, BaseURL: "https://generativelanguage.googleapis.com", Key: "gem-key",
		},
	}
	requestBody := []byte(`{
		"model":"client-gemini","max_tokens":64,"stream":true,"system":"be concise",
		"messages":[{"role":"user","content":[{"type":"text","text":"inspect"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}]}],
		"tools":[{"name":"Read","description":"read file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
		"tool_choice":{"type":"tool","name":"Read"}
	}`)
	format, converted, err := adaptor.ConvertRequest(context, FormatAnthropicMessages, requestBody)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	if format != FormatGemini || !context.IsStream {
		t.Fatalf("format/stream = %q/%v", format, context.IsStream)
	}
	var request geminiRequest
	if err := jsonx.Unmarshal(converted, &request); err != nil {
		t.Fatalf("parse Gemini request: %v", err)
	}
	if request.SystemInstruction == nil || len(request.SystemInstruction.Parts) != 1 {
		t.Fatalf("system instruction = %#v", request.SystemInstruction)
	}
	if len(request.Contents) != 1 || len(request.Contents[0].Parts) != 2 || request.Contents[0].Parts[1].InlineData == nil {
		t.Fatalf("contents = %#v", request.Contents)
	}
	if len(request.Tools) != 1 || request.ToolConfig == nil || request.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("tools/config = %#v/%#v", request.Tools, request.ToolConfig)
	}
	url, err := adaptor.GetUpstreamURL(context)
	if err != nil {
		t.Fatalf("GetUpstreamURL: %v", err)
	}
	if !strings.Contains(url, ":streamGenerateContent?alt=sse") || strings.Contains(url, "gem-key") {
		t.Fatalf("stream URL = %q", url)
	}
}

func TestGeminiAdaptor_AnthropicToolResponseAndStream(t *testing.T) {
	adaptor := NewGeminiAdaptor(nil, nil)
	context := &RelayContext{InboundFormat: FormatAnthropicMessages, ClientModel: "client-gemini"}
	nonStream := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
		"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"Read","args":{"path":"README.md"}}}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"cachedContentTokenCount":4}
	}`))}
	format, output, err := adaptor.ConvertResponse(context, FormatGemini, nonStream)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	if format != FormatAnthropicMessages {
		t.Fatalf("format = %q", format)
	}
	var response apicompat.AnthropicResponse
	if err := jsonx.Unmarshal(output, &response); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if response.StopReason != "tool_use" || len(response.Content) != 1 || response.Content[0].Name != "Read" {
		t.Fatalf("response = %#v", response)
	}
	if response.Usage.InputTokens != 6 || response.Usage.CacheReadInputTokens != 4 || response.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %#v", response.Usage)
	}

	streamBody := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"Read","args":{"path":"README.md"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}

`
	streamResp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(streamBody))}
	streamContext := &RelayContext{InboundFormat: FormatAnthropicMessages, ClientModel: "client-gemini", IsStream: true}
	streamFormat, reader, err := adaptor.ConvertStreamResponse(streamContext, FormatGemini, streamResp)
	if err != nil {
		t.Fatalf("ConvertStreamResponse: %v", err)
	}
	if streamFormat != FormatAnthropicMessages {
		t.Fatalf("stream format = %q", streamFormat)
	}
	stream, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	for _, expected := range []string{`"type":"tool_use"`, `"name":"Read"`, `"partial_json":"{\"path\":\"README.md\"}"`, `"stop_reason":"tool_use"`, "event: message_stop"} {
		if !strings.Contains(string(stream), expected) {
			t.Fatalf("stream missing %q:\n%s", expected, stream)
		}
	}
}

func TestGeminiAdaptor_FailedGenerationIsNotReportedAsSuccess(t *testing.T) {
	adaptor := NewGeminiAdaptor(nil, nil)
	context := &RelayContext{InboundFormat: FormatAnthropicMessages, ClientModel: "client-gemini"}

	nonStream := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
		"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"MALFORMED_FUNCTION_CALL"}]
	}`))}
	if _, _, err := adaptor.ConvertResponse(context, FormatGemini, nonStream); err == nil {
		t.Fatal("malformed Gemini function call was reported as a successful response")
	}

	stream := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
		"data:{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"MALFORMED_FUNCTION_CALL\"}]}\n\n",
	))}
	format, reader, err := adaptor.ConvertStreamResponse(context, FormatGemini, stream)
	if err != nil {
		t.Fatalf("ConvertStreamResponse: %v", err)
	}
	if format != FormatAnthropicMessages {
		t.Fatalf("format = %q", format)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !strings.Contains(string(output), `"type":"error"`) || strings.Contains(string(output), "event: message_stop") {
		t.Fatalf("failed Gemini generation was converted to success:\n%s", output)
	}
}

func TestPumpChatToAnthropic_TruncatedStreamEmitsError(t *testing.T) {
	for name, input := range map[string]string{
		"empty":     "",
		"no finish": `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}` + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			reader, writer := io.Pipe()
			go pumpChatToAnthropic(strings.NewReader(input), writer, "client-model")
			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			stream := string(output)
			if !strings.Contains(stream, `"type":"error"`) || !strings.Contains(stream, `"message":"upstream stream interrupted"`) {
				t.Fatalf("missing Anthropic error event:\n%s", stream)
			}
			if strings.Contains(stream, "event: message_stop") {
				t.Fatalf("truncated stream must not report success:\n%s", stream)
			}
		})
	}
}

func TestPumpChatToAnthropic_DoneWithoutFinishReasonCompletes(t *testing.T) {
	input := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"complete"},"finish_reason":null}]}` + "\n\n" +
		"data:[DONE]\n\n"
	reader, writer := io.Pipe()
	go pumpChatToAnthropic(strings.NewReader(input), writer, "client-model")
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	stream := string(output)
	if strings.Contains(stream, `"type":"error"`) || !strings.Contains(stream, "event: message_stop") {
		t.Fatalf("explicit DONE must complete the stream:\n%s", stream)
	}
}

func TestChatBridgeErrorFinishRemainsAnError(t *testing.T) {
	input := `data: {"id":"chatcmpl-stream-error","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"upstream stream interrupted"},"finish_reason":"error"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	t.Run("anthropic", func(t *testing.T) {
		reader, writer := io.Pipe()
		go pumpChatToAnthropic(strings.NewReader(input), writer, "client-model")
		output, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read output: %v", err)
		}
		stream := string(output)
		if !strings.Contains(stream, `"type":"error"`) || strings.Contains(stream, "event: message_stop") {
			t.Fatalf("chat error was converted to Anthropic success:\n%s", stream)
		}
	})

	t.Run("responses", func(t *testing.T) {
		reader, writer := io.Pipe()
		go pumpChatToResponses(strings.NewReader(input), writer, "client-model")
		output, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read output: %v", err)
		}
		stream := string(output)
		if !strings.Contains(stream, `"type":"response.failed"`) || strings.Contains(stream, `"type":"response.completed"`) {
			t.Fatalf("chat error was converted to Responses success:\n%s", stream)
		}
	})
}
