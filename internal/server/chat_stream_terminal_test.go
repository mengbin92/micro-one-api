package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
)

type chatTerminalProvider struct {
	relayprovider.Provider
	chunks []relayprovider.StreamChunk
}

func (p chatTerminalProvider) ChatCompletionsStream(context.Context, *relayprovider.ChatCompletionsRequest) (<-chan relayprovider.StreamChunk, error) {
	chunks := make(chan relayprovider.StreamChunk, len(p.chunks))
	for _, chunk := range p.chunks {
		chunks <- chunk
	}
	close(chunks)
	return chunks, nil
}

func TestChatStreamTerminalFailuresAreExplicitAndNeverReplay(t *testing.T) {
	for _, tc := range []struct {
		name          string
		chunks        []relayprovider.StreamChunk
		commitMessage string
		commits       int
		releases      int
	}{
		{name: "commit rejected", chunks: []relayprovider.StreamChunk{{Complete: true}}, commitMessage: "private billing failure", commits: 1},
		{name: "stream error", chunks: []relayprovider.StreamChunk{{StreamError: errors.New("private upstream failure")}}, releases: 1},
		{name: "error takes precedence over complete", chunks: []relayprovider.StreamChunk{{Complete: true, StreamError: io.ErrUnexpectedEOF}}, releases: 1},
		{name: "invalid payload before done", chunks: []relayprovider.StreamChunk{{InvalidPayload: true}, {Complete: true}}, releases: 1},
		{name: "missing terminal", releases: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billing := &rawBillingClient{commitMessage: tc.commitMessage}
			s := &HTTPServer{billingClient: billing}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			err := s.handleStreamingResponse(rec, req, chatTerminalProvider{chunks: tc.chunks}, &relayprovider.ChatCompletionsRequest{Stream: true}, &billingv1.ReserveQuotaResponse{ReservationId: "terminal-test"}, usageLogInput{})
			if !errors.Is(err, errRelayStreamInterrupted) || !relaybiz.IsPostForwardError(err) {
				t.Errorf("terminal failure must stop retry/outer JSON, err=%v", err)
			}
			body := rec.Body.String()
			if strings.Count(body, "data: {\"error\":") != 1 || strings.Contains(body, "[DONE]") || strings.Contains(body, "private") {
				t.Errorf("terminal response must contain one sanitized SSE error and no success sentinel: %s", body)
			}
			if billing.commits != tc.commits || billing.releases != tc.releases {
				t.Errorf("commits=%d releases=%d, want %d/%d", billing.commits, billing.releases, tc.commits, tc.releases)
			}
		})
	}
}

func TestChatStreamProviderFailuresStopAtOneAttempt(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct {
		name, tail, commitMessage string
		truncated                 bool
		commits, releases         int
	}{
		{name: "settlement failure", tail: "data: [DONE]\n\n", commitMessage: "private billing failure", commits: 1},
		{name: "malformed payload before done", tail: "data: {invalid}\n\ndata: [DONE]\n\n", releases: 1},
		{name: "scanner failure after content", truncated: true, releases: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.truncated {
					w.Header().Set("Content-Length", "10000")
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"+tc.tail)
			}))
			defer upstream.Close()
			identity, channel := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL, key: "fixture"}
			billing := &rawBillingClient{commitMessage: tc.commitMessage}
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
			s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`))
			req.Header.Set("Authorization", "Bearer user-token")
			s.handleChatCompletions(rec, req)
			body := rec.Body.String()
			if strings.Count(body, "data: {\"error\":") != 1 || strings.Contains(body, "[DONE]") || strings.Contains(body, "private") || strings.Contains(body, "upstream service error") {
				t.Errorf("expected one sanitized terminal SSE error: %s", body)
			}
			if calls != 1 || billing.commits != tc.commits || billing.releases != tc.releases {
				t.Errorf("calls=%d commits=%d releases=%d, want 1/%d/%d", calls, billing.commits, billing.releases, tc.commits, tc.releases)
			}
		})
	}
}

type chatDoneWriter struct {
	*httptest.ResponseRecorder
	onDone func()
	err    error
}

func (w chatDoneWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "[DONE]") {
		if w.onDone != nil {
			w.onDone()
		}
		if w.err != nil {
			return 0, w.err
		}
	}
	return w.ResponseRecorder.Write(p)
}

func TestChatStreamDoneWriteFailureKeepsCommittedSettlement(t *testing.T) {
	billing := &rawBillingClient{}
	logs := &rawLogClient{}
	s := &HTTPServer{billingClient: billing, logClient: logs}
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	err := s.handleStreamingResponse(chatDoneWriter{ResponseRecorder: httptest.NewRecorder(), err: io.ErrClosedPipe}, req, chatTerminalProvider{chunks: []relayprovider.StreamChunk{{Complete: true}}}, &relayprovider.ChatCompletionsRequest{Stream: true}, &billingv1.ReserveQuotaResponse{ReservationId: "done-failure"}, usageLogInput{})
	if !errors.Is(err, errRelayStreamInterrupted) || billing.commits != 1 || billing.releases != 0 || len(logs.entries) != 1 {
		t.Fatalf("terminal delivery failure must keep settlement and usage log: err=%v commits=%d releases=%d logs=%d", err, billing.commits, billing.releases, len(logs.entries))
	}
}

func TestChatStreamDoneFollowsCommitAndPrecedesUsageLog(t *testing.T) {
	billing, logs := &rawBillingClient{}, &rawLogClient{}
	s := &HTTPServer{billingClient: billing, logClient: logs}
	rec := httptest.NewRecorder()
	w := chatDoneWriter{ResponseRecorder: rec, onDone: func() {
		if billing.commits != 1 || len(logs.entries) != 0 {
			t.Errorf("DONE must follow settlement without waiting for log RPC: commits=%d logs=%d", billing.commits, len(logs.entries))
		}
	}}
	chunks := []relayprovider.StreamChunk{{Usage: relayprovider.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}}, {Complete: true}}
	err := s.handleStreamingResponse(w, httptest.NewRequest("POST", "/v1/chat/completions", nil), chatTerminalProvider{chunks: chunks}, &relayprovider.ChatCompletionsRequest{Stream: true}, &billingv1.ReserveQuotaResponse{ReservationId: "success"}, usageLogInput{})
	if err != nil || billing.commits != 1 || billing.releases != 0 || len(logs.entries) != 1 || strings.Count(rec.Body.String(), "[DONE]") != 1 || strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("success stream changed: err=%v commits=%d releases=%d logs=%d body=%s", err, billing.commits, billing.releases, len(logs.entries), rec.Body.String())
	}
	if billing.commitRequests[0].ActualTokens != 5 {
		t.Fatalf("actual tokens=%d, want 5", billing.commitRequests[0].ActualTokens)
	}
}

func TestChatStreamCanceledWriterOnlyReleases(t *testing.T) {
	billing := &rawBillingClient{failOnCanceledContext: true}
	s := &HTTPServer{billingClient: billing}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := httptest.NewRecorder()
	w := &tierCancellingWriter{ResponseRecorder: rec, cancel: cancel}
	chunks := []relayprovider.StreamChunk{{Usage: relayprovider.Usage{TotalTokens: 5}}, {Complete: true}}
	err := s.handleStreamingResponse(w, httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx), chatTerminalProvider{chunks: chunks}, &relayprovider.ChatCompletionsRequest{Stream: true}, &billingv1.ReserveQuotaResponse{ReservationId: "canceled"}, usageLogInput{})
	if !errors.Is(err, context.Canceled) || billing.commits != 0 || billing.releases != 1 || rec.Body.Len() != 0 {
		t.Fatalf("canceled writer must release without terminal writes: err=%v commits=%d releases=%d body=%s", err, billing.commits, billing.releases, rec.Body.String())
	}
}
