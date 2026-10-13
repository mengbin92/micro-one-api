package evidence

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPObservationPreservesBytesAndEmitsOnce(t *testing.T) {
	payload := []byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0},"choices":[{"message":{"content":"secret-conversation"}}]}`)
	var records []Record
	ctx := WithRecorder(context.Background(), func(record Record) { records = append(records, record) })
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.com/chat/completions", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Authorization", "Bearer secret-key")
	client := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"upstream-r1"}}, Body: io.NopCloser(bytes.NewReader(payload))}, nil
	})}
	resp, err := Do(client, req, "openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = resp.Body.Close()
	if !bytes.Equal(got, payload) || len(records) != 1 || !records[0].Complete || records[0].Observation.Status != "verified" || records[0].Observation.ProviderRequestID != "upstream-r1" {
		t.Fatalf("response/evidence changed: %s / %+v", got, records)
	}
	if bytes.Contains(records[0].Observation.Usage, []byte("secret")) {
		t.Fatal("content/credentials in usage evidence")
	}
}

func TestHTTPObserverDisabledHasNoBodyWrapper(t *testing.T) {
	raw := io.NopCloser(strings.NewReader("unaltered"))
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: raw}, nil
	})}
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	resp, err := Do(client, req, "other")
	if err != nil || resp.Body != raw {
		t.Fatal("disabled observer changed the body")
	}
}

func TestHTTPObservationFailureAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		transportErr bool
		partial      bool
		want         string
	}{
		{"transport", 0, true, false, "execution_unknown"},
		{"rejected", 429, false, false, "upstream_rejected"},
		{"closed early", 200, false, true, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records []Record
			ctx := WithRecorder(context.Background(), func(r Record) { records = append(records, r) })
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.com/responses", nil)
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				if tc.transportErr {
					return nil, errors.New("secret transport error")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"secret"}}`))}, nil
			})}
			resp, err := Do(client, req, "responses")
			if tc.transportErr {
				if err == nil {
					t.Fatal("transport error lost")
				}
			} else {
				if !tc.partial {
					_, _ = io.Copy(io.Discard, resp.Body)
				} else {
					buffer := make([]byte, 1)
					_, _ = resp.Body.Read(buffer)
				}
				_ = resp.Body.Close()
			}
			if len(records) != 1 || records[0].Execution != tc.want || records[0].Observation.Buckets != nil {
				t.Fatalf("failure laundered: %+v", records)
			}
		})
	}
}

func TestHTTPSSEObservationAndBounds(t *testing.T) {
	stream := "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
	for _, tc := range []struct{ name, body, contentType, status string }{
		{"sse", stream, "text/event-stream", "verified"},
		{"oversize", strings.Repeat("x", MaxFrameBytes+1), "application/json", "unobserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Record
			ctx := WithRecorder(context.Background(), func(r Record) { got = r })
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.com/chat/completions", nil)
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			resp, err := Do(client, req, "openai_chat")
			if err != nil {
				t.Fatal(err)
			}
			result, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if string(result) != tc.body || got.Observation.Status != tc.status {
				t.Fatalf("lost bytes/bound: %+v", got)
			}
		})
	}
}

func TestHTTPObservationConcurrentReadAndClose(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	records := make(chan Record, 2)
	ctx := WithRecorder(context.Background(), func(r Record) { records <- r })
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.com/chat/completions", nil)
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
	})}
	resp, err := Do(client, req, "openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	_, _ = io.WriteString(writer, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	_ = resp.Body.Close()
	<-done
	_ = resp.Body.Close()
	if got := <-records; got.Complete || got.Execution != "incomplete" {
		t.Fatalf("concurrent close claimed completion: %+v", got)
	}
	select {
	case <-records:
		t.Fatal("duplicate observation")
	default:
	}
}
