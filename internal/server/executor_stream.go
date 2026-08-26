package server

import (
	"context"
	"io"
	"strings"
	"sync"

	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

type streamUsageObserver interface {
	ObserveBytes([]byte)
	Usage() rawUsage
	ResponseID() string
}

type finalizingRelayStream struct {
	stream      relaybiz.RelayStream
	usage       streamUsageObserver
	finalize    func(relaybiz.CanonicalUsage, string, bool) error
	mu          sync.Mutex
	completed   bool
	interrupted bool
	terminal    streamTerminalTracker
	closeOnce   sync.Once
	closeError  error
}

func newFinalizingRelayStream(endpoint APIEndpoint, stream relaybiz.RelayStream, estimated relaybiz.CanonicalUsage, finalize func(relaybiz.CanonicalUsage, string, bool) error) relaybiz.RelayStream {
	fallback := rawUsageFromCanonical(estimated)
	var observer streamUsageObserver = newRawStreamUsageTracker(fallback)
	if endpoint == EndpointResponses {
		observer = newResponsesStreamUsageTracker(fallback)
	}
	return &finalizingRelayStream{stream: stream, usage: observer, finalize: finalize, terminal: streamTerminalTracker{endpoint: endpoint}}
}

func (s *finalizingRelayStream) Read(p []byte) (int, error) {
	n, err := s.stream.Read(p)
	if n > 0 {
		s.usage.ObserveBytes(p[:n])
		s.terminal.ObserveBytes(p[:n])
	}
	if err == io.EOF {
		s.mu.Lock()
		s.completed = true
		s.mu.Unlock()
	}
	return n, err
}

func (s *finalizingRelayStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		completed := s.completed && !s.interrupted && s.terminal.Success()
		s.mu.Unlock()

		closeErr := s.stream.Close()
		usage := canonicalUsageFromRaw(s.usage.Usage())
		if s.finalize != nil {
			if err := s.finalize(usage, s.usage.ResponseID(), completed); closeErr == nil {
				closeErr = err
			}
		}
		s.closeError = closeErr
	})
	return s.closeError
}

func (s *finalizingRelayStream) markInterrupted() {
	s.mu.Lock()
	s.interrupted = true
	s.mu.Unlock()
}

type streamTerminalTracker struct {
	endpoint APIEndpoint
	pending  string
	success  bool
	failed   bool
}

func (t *streamTerminalTracker) ObserveBytes(p []byte) {
	t.pending += string(p)
	for {
		line, rest, ok := strings.Cut(t.pending, "\n")
		if !ok {
			return
		}
		t.pending = rest
		t.observeLine(strings.TrimSpace(line))
	}
}

func (t *streamTerminalTracker) observeLine(line string) {
	if line == "" {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(line))
	if event, ok := strings.CutPrefix(lower, "event:"); ok {
		t.observeEvent(strings.TrimSpace(event))
		return
	}
	payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
	if !ok {
		return
	}
	payload = strings.TrimSpace(payload)
	if strings.EqualFold(payload, "[DONE]") {
		if t.endpoint == EndpointChatCompletions {
			t.success = true
		}
		return
	}
	var envelope struct {
		Type     string `json:"type"`
		Status   string `json:"status"`
		Response *struct {
			Status string `json:"status"`
		} `json:"response"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := jsonx.Unmarshal([]byte(payload), &envelope); err != nil {
		return
	}
	eventType := strings.ToLower(strings.TrimSpace(envelope.Type))
	status := strings.ToLower(strings.TrimSpace(envelope.Status))
	if envelope.Response != nil && strings.TrimSpace(envelope.Response.Status) != "" {
		status = strings.ToLower(strings.TrimSpace(envelope.Response.Status))
	}
	switch t.endpoint {
	case EndpointResponses:
		if eventType == "response.failed" || eventType == "response.incomplete" || status == "failed" || status == "incomplete" {
			t.failed = true
		}
		if eventType == "response.completed" || status == "completed" {
			t.success = true
		}
	case EndpointAnthropicMessages:
		if eventType == "error" {
			t.failed = true
		}
		if eventType == "message_stop" {
			t.success = true
		}
	default:
		if eventType == "error" {
			t.failed = true
		}
		for _, choice := range envelope.Choices {
			finishReason := strings.ToLower(strings.TrimSpace(choice.FinishReason))
			if finishReason == "error" {
				t.failed = true
			} else if finishReason != "" {
				t.success = true
			}
		}
	}
}

func (t *streamTerminalTracker) observeEvent(event string) {
	switch t.endpoint {
	case EndpointResponses:
		if event == "response.failed" || event == "response.incomplete" {
			t.failed = true
		}
		if event == "response.completed" {
			t.success = true
		}
	case EndpointAnthropicMessages:
		if event == "error" {
			t.failed = true
		}
		if event == "message_stop" {
			t.success = true
		}
	}
}

func (t *streamTerminalTracker) Success() bool {
	if strings.TrimSpace(t.pending) != "" {
		t.observeLine(strings.TrimSpace(t.pending))
		t.pending = ""
	}
	return t.success && !t.failed
}

func rawUsageFromCanonical(usage relaybiz.CanonicalUsage) rawUsage {
	return rawUsage{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		TotalTokens:           usage.TotalTokens,
	}
}

func canonicalUsageFromRaw(usage rawUsage) relaybiz.CanonicalUsage {
	return relaybiz.CanonicalUsage{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		TotalTokens:           usage.TotalTokens,
	}
}

func settlementContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
