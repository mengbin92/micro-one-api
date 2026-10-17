package evidence

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTracingWithoutEvidenceWaitsForStreamAndObservesReuse(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	finish := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		if r.URL.Path == "/blocked" {
			<-finish
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client := server.Client()
	req, err := http.NewRequest(http.MethodGet, server.URL+"/blocked", nil)
	require.NoError(t, err)
	resp, err := Do(client, req, "openai_chat")
	require.NoError(t, err)
	for _, span := range spans.Ended() {
		require.NotEqual(t, "upstream.http", span.Name(), "client.Do returning headers is not stream completion")
	}
	close(finish)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	request, err := http.NewRequest(http.MethodGet, server.URL+"/reuse", nil)
	require.NoError(t, err)
	response, err := Do(client, request, "openai_chat")
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	var upstream []sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		if span.Name() == "upstream.http" {
			upstream = append(upstream, span)
		}
	}
	require.Len(t, upstream, 2)
	var reused bool
	var firstByte, firstBody, firstContent bool
	for _, a := range upstream[1].Attributes() {
		if a.Key == attribute.Key("network.connection.reused") {
			reused = a.Value.AsBool()
		}
	}
	for _, event := range upstream[0].Events() {
		switch event.Name {
		case "upstream.first_byte":
			firstByte = true
		case "upstream.first_body":
			firstBody = true
		case "upstream.first_content":
			firstContent = true
		}
	}
	require.True(t, reused)
	require.True(t, firstByte)
	require.True(t, firstBody)
	require.True(t, firstContent)
}

func TestTracingWithoutEvidenceEndsWhenUnreadBodyIsCancelled(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	resp, err := Do(server.Client(), req, "openai_chat")
	require.NoError(t, err)
	cancel()
	require.Eventually(t, func() bool {
		for _, span := range spans.Ended() {
			if span.Name() == "upstream.http" {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	require.NoError(t, resp.Body.Close())
}
