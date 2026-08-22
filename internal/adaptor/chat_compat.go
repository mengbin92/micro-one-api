package adaptor

import (
	"fmt"
	"io"
	"net/http"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
)

func convertRequestToChat(rc *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	var request *apicompat.ChatCompletionsRequest
	switch inbound {
	case FormatOpenAIChatCompletions:
		return FormatOpenAIChatCompletions, body, nil
	case FormatOpenAIResponses:
		var responsesRequest apicompat.ResponsesRequest
		if err := jsonx.Unmarshal(body, &responsesRequest); err != nil {
			return "", nil, fmt.Errorf("parse responses request: %w", err)
		}
		var err error
		request, err = apicompat.ResponsesToChatCompletionsRequest(&responsesRequest)
		if err != nil {
			return "", nil, fmt.Errorf("responses to chat: %w", err)
		}
	case FormatAnthropicMessages:
		var anthropicRequest apicompat.AnthropicRequest
		if err := jsonx.Unmarshal(body, &anthropicRequest); err != nil {
			return "", nil, fmt.Errorf("parse anthropic request: %w", err)
		}
		if rc != nil && rc.ResolvedModel != "" {
			anthropicRequest.Model = rc.ResolvedModel
		}
		var err error
		request, err = apicompat.AnthropicToChatCompletionsRequest(&anthropicRequest)
		if err != nil {
			return "", nil, fmt.Errorf("anthropic to chat: %w", err)
		}
	default:
		return "", nil, fmt.Errorf("inbound format %q is not supported by chat upstream", inbound)
	}

	if rc != nil && rc.ResolvedModel != "" {
		request.Model = rc.ResolvedModel
	}
	if request.Stream {
		request.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	}
	out, err := jsonx.Marshal(request)
	if err != nil {
		return "", nil, fmt.Errorf("marshal chat request: %w", err)
	}
	return FormatOpenAIChatCompletions, out, nil
}

func convertChatResponse(rc *RelayContext, resp *http.Response) (Format, []byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBody))
	if err != nil {
		return "", nil, fmt.Errorf("read chat response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &provider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	if rc == nil || rc.InboundFormat == FormatOpenAIChatCompletions {
		return FormatOpenAIChatCompletions, body, nil
	}

	var chatResponse apicompat.ChatCompletionsResponse
	if err := jsonx.Unmarshal(body, &chatResponse); err != nil {
		return "", nil, fmt.Errorf("parse chat response: %w", err)
	}
	model := rc.ClientModel
	switch rc.InboundFormat {
	case FormatOpenAIResponses:
		out, err := jsonx.Marshal(apicompat.ChatCompletionsResponseToResponses(&chatResponse, model))
		if err != nil {
			return "", nil, fmt.Errorf("marshal responses response: %w", err)
		}
		return FormatOpenAIResponses, out, nil
	case FormatAnthropicMessages:
		anthropicResponse, err := apicompat.ChatCompletionsResponseToAnthropic(&chatResponse, model)
		if err != nil {
			return "", nil, fmt.Errorf("chat to anthropic: %w", err)
		}
		out, err := jsonx.Marshal(anthropicResponse)
		if err != nil {
			return "", nil, fmt.Errorf("marshal anthropic response: %w", err)
		}
		return FormatAnthropicMessages, out, nil
	default:
		return "", nil, fmt.Errorf("inbound format %q is not supported by chat response bridge", rc.InboundFormat)
	}
}

func convertChatStream(rc *RelayContext, resp *http.Response) (Format, io.Reader, error) {
	if rc == nil || rc.InboundFormat == FormatOpenAIChatCompletions {
		return FormatOpenAIChatCompletions, resp.Body, nil
	}
	model := rc.ClientModel
	switch rc.InboundFormat {
	case FormatOpenAIResponses:
		reader, writer := io.Pipe()
		go pumpChatToResponses(resp.Body, writer, model)
		return FormatOpenAIResponses, reader, nil
	case FormatAnthropicMessages:
		reader, writer := io.Pipe()
		go pumpChatToAnthropic(resp.Body, writer, model)
		return FormatAnthropicMessages, reader, nil
	default:
		return "", nil, fmt.Errorf("inbound format %q is not supported by chat stream bridge", rc.InboundFormat)
	}
}
