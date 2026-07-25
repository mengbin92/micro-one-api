package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicChatCompletionsParsesUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_123",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"pong"}],
			"model":"glm-5.2",
			"stop_reason":"end_turn",
			"usage":{"input_tokens":100,"output_tokens":25,"cache_read_input_tokens":60}
		}`))
	}))
	defer upstream.Close()

	p := NewAnthropicProvider(upstream.URL, "sk-test", 5*time.Second)
	resp, err := p.ChatCompletions(context.Background(), &ChatCompletionsRequest{Model: "glm-5.2"})
	if err != nil {
		t.Fatalf("ChatCompletions: %v", err)
	}

	if resp.Usage.PromptTokens != 100 || resp.Usage.CompletionTokens != 25 || resp.Usage.TotalTokens != 125 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if got := resp.Usage.PromptTokensDetails.CacheReadTokens; got != 60 {
		t.Fatalf("cache_read_tokens = %d, want 60", got)
	}
}

func TestAnthropicChatCompletionsStreamCollectsUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		write := func(event, data string) {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			flusher.Flush()
		}
		write("message_start", `{"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":60}}}`)
		write("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"po"}}`)
		write("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ng"}}`)
		write("message_delta", `{"type":"message_delta","usage":{"output_tokens":25}}`)
		write("message_stop", `{"type":"message_stop"}`)
	}))
	defer upstream.Close()

	stream := true
	p := NewAnthropicProvider(upstream.URL, "sk-test", 5*time.Second)
	chunks, err := p.ChatCompletionsStream(context.Background(), &ChatCompletionsRequest{Model: "glm-5.2", Stream: stream})
	if err != nil {
		t.Fatalf("ChatCompletionsStream: %v", err)
	}

	var text string
	var usage *Usage
	var finishReasons []string
	for chunk := range chunks {
		for _, choice := range chunk.Choices {
			text += choice.Delta.Content
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			}
		}
		if chunk.Usage.TotalTokens > 0 {
			u := chunk.Usage
			usage = &u
		}
	}

	if text != "pong" {
		t.Fatalf("text = %q, want %q", text, "pong")
	}
	if usage == nil {
		t.Fatal("no usage chunk received")
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 25 || usage.TotalTokens != 125 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.PromptTokensDetails.CacheReadTokens != 60 {
		t.Fatalf("cache_read_tokens = %d, want 60", usage.PromptTokensDetails.CacheReadTokens)
	}
	if len(finishReasons) != 1 || finishReasons[0] != "stop" {
		t.Fatalf("finish_reasons = %v, want [stop]", finishReasons)
	}
}

func TestAnthropicChatCompletionsStreamLargeEvent(t *testing.T) {
	// Regression: bufio.Scanner's default 64KB line limit used to truncate large
	// SSE events; the stream must survive events well beyond that.
	// Build a 256KB payload from the base64 alphabet (A-Za-z0-9+/=), which
	// mirrors real Anthropic image-content events. base64 is JSON-safe so it
	// needs no escaping; the point is to exceed bufio's 64KB default scanner
	// limit and confirm the raised 4MB buffer survives the full event.
	const bigSize = 256 * 1024
	base64Alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	bigText := strings.Repeat(base64Alphabet, bigSize/len(base64Alphabet)+1)[:bigSize]

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		write := func(event, data string) {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			flusher.Flush()
		}
		write("message_start", `{"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10,"output_tokens":1}}}`)
		write("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"%s"}}`, bigText))
		write("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"tail"}}`)
		write("message_delta", `{"type":"message_delta","usage":{"output_tokens":999}}`)
		write("message_stop", `{"type":"message_stop"}`)
	}))
	defer upstream.Close()

	p := NewAnthropicProvider(upstream.URL, "sk-test", 5*time.Second)
	chunks, err := p.ChatCompletionsStream(context.Background(), &ChatCompletionsRequest{Model: "glm-5.2", Stream: true})
	if err != nil {
		t.Fatalf("ChatCompletionsStream: %v", err)
	}

	var text string
	var usage *Usage
	for chunk := range chunks {
		for _, choice := range chunk.Choices {
			text += choice.Delta.Content
		}
		if chunk.Usage.TotalTokens > 0 {
			u := chunk.Usage
			usage = &u
		}
	}

	if len(text) != len(bigText)+len("tail") {
		t.Fatalf("text length = %d, want %d", len(text), len(bigText)+len("tail"))
	}
	if usage == nil || usage.CompletionTokens != 999 {
		t.Fatalf("usage = %+v", usage)
	}
}
