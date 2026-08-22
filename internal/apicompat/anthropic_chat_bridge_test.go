package apicompat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicToChatCompletionsRequestPreservesChatOnlyFields(t *testing.T) {
	req := &AnthropicRequest{
		Model:     "deepseek-v4-pro-0813",
		MaxTokens: 64,
		Stream:    true,
		StopSeqs:  []string{"END", "STOP"},
		Messages: []AnthropicMessage{{
			Role:    "user",
			Content: []byte(`"hello"`),
		}},
	}

	chat, err := AnthropicToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.NotNil(t, chat.MaxTokens)
	assert.Equal(t, 64, *chat.MaxTokens)
	assert.Nil(t, chat.MaxCompletionTokens)
	assert.JSONEq(t, `["END","STOP"]`, string(chat.Stop))
	assert.Empty(t, chat.ReasoningEffort)
	require.NotNil(t, chat.StreamOptions)
	assert.True(t, chat.StreamOptions.IncludeUsage)
}

func TestChatCompletionsResponseToAnthropicPreservesCacheCreation(t *testing.T) {
	resp := &ChatCompletionsResponse{
		ID:    "chatcmpl_1",
		Model: "deepseek",
		Choices: []ChatChoice{{
			Message:      ChatMessage{Role: "assistant", Content: []byte(`"ok"`)},
			FinishReason: "stop",
		}},
		Usage: &ChatUsage{
			PromptTokens:     20,
			CompletionTokens: 3,
			TotalTokens:      23,
			PromptTokensDetails: &ChatTokenDetails{
				CachedTokens:          5,
				CacheCreation5mTokens: 4,
				CacheCreation1hTokens: 2,
			},
		},
	}

	got, err := ChatCompletionsResponseToAnthropic(resp, "client-model")
	require.NoError(t, err)
	assert.Equal(t, 9, got.Usage.InputTokens)
	assert.Equal(t, 5, got.Usage.CacheReadInputTokens)
	assert.Equal(t, 6, got.Usage.CacheCreationInputTokens)
	assert.Equal(t, 3, got.Usage.OutputTokens)
}

func TestBufferedResponsesToAnthropicParallelToolsAreSequential(t *testing.T) {
	chatState := NewChatCompletionsToResponsesStreamState("client-model")
	anthropicState := NewBufferedResponsesEventToAnthropicState()
	anthropicState.Model = "client-model"

	zero, one := 0, 1
	chunks := []ChatCompletionsChunk{
		{
			ID: "chatcmpl_parallel",
			Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{
				{Index: &zero, ID: "call_a", Type: "function", Function: ChatFunctionCall{Name: "first", Arguments: `{"a":`}},
				{Index: &one, ID: "call_b", Type: "function", Function: ChatFunctionCall{Name: "second", Arguments: `{"b":`}},
			}}}},
		},
		{
			Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{
				{Index: &one, Function: ChatFunctionCall{Arguments: `2}`}},
				{Index: &zero, Function: ChatFunctionCall{Arguments: `1}`}},
			}}}},
		},
		{
			Choices: []ChatChunkChoice{{FinishReason: stringPointer("tool_calls")}},
			Usage:   &ChatUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		},
	}

	var got []AnthropicStreamEvent
	emit := func(event ResponsesStreamEvent) {
		got = append(got, ResponsesEventToAnthropicEvents(&event, anthropicState)...)
	}
	for i := range chunks {
		for _, event := range ChatCompletionsChunkToResponsesEvents(&chunks[i], chatState) {
			emit(event)
		}
	}
	for _, event := range FinalizeChatCompletionsResponsesStream(chatState) {
		emit(event)
	}

	var starts, deltas, stops []AnthropicStreamEvent
	for _, event := range got {
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				starts = append(starts, event)
			}
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Type == "input_json_delta" {
				deltas = append(deltas, event)
			}
		case "content_block_stop":
			stops = append(stops, event)
		}
	}
	require.Len(t, starts, 2)
	require.Len(t, deltas, 2)
	require.Len(t, stops, 2)
	assert.Equal(t, 0, *starts[0].Index)
	assert.Equal(t, 1, *starts[1].Index)
	assert.Equal(t, "call_a", starts[0].ContentBlock.ID)
	assert.Equal(t, "call_b", starts[1].ContentBlock.ID)
	assert.JSONEq(t, `{"a":1}`, deltas[0].Delta.PartialJSON)
	assert.JSONEq(t, `{"b":2}`, deltas[1].Delta.PartialJSON)
	assert.Equal(t, 0, *stops[0].Index)
	assert.Equal(t, 1, *stops[1].Index)
	assert.Equal(t, "message_delta", got[len(got)-2].Type)
	assert.Equal(t, "tool_use", got[len(got)-2].Delta.StopReason)
	assert.Equal(t, "message_stop", got[len(got)-1].Type)
}

func TestBufferedResponsesToAnthropicRejectsInvalidToolJSON(t *testing.T) {
	state := NewBufferedResponsesEventToAnthropicState()
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:     "response.created",
		Response: &ResponsesResponse{ID: "resp_invalid", Model: "deepseek"},
	}, state)
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "function_call", CallID: "call_bad", Name: "exec"},
	}, state)
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 0,
		Delta:       `{"cmd":`,
	}, state)

	events := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
			Usage:  &ResponsesUsage{InputTokens: 1, OutputTokens: 1},
		},
	}, state)

	require.Len(t, events, 1)
	assert.Equal(t, "error", events[0].Type)
	require.NotNil(t, events[0].Error)
	assert.Contains(t, events[0].Error.Message, "invalid JSON object")
	assert.Nil(t, FinalizeResponsesAnthropicStream(state))
}

func stringPointer(value string) *string { return &value }
