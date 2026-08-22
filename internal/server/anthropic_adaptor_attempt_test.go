package server

import (
	"io"
	"strings"
	"testing"
)

func TestPreflightAnthropicStream(t *testing.T) {
	t.Run("preserves first event and remainder", func(t *testing.T) {
		input := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		first, remainder, err := preflightAnthropicStream(strings.NewReader(input))
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		rest, err := io.ReadAll(remainder)
		if err != nil {
			t.Fatalf("read remainder: %v", err)
		}
		if string(first)+string(rest) != input {
			t.Fatalf("stream changed:\n%s%s", first, rest)
		}
	})

	for name, input := range map[string]string{
		"error event": "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\"}}\n\n",
		"empty":       "",
		"incomplete":  "event: message_start\ndata: {}",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := preflightAnthropicStream(strings.NewReader(input))
			if err == nil {
				t.Fatal("expected preflight error")
			}
		})
	}
}
