package evidence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPObservationPreservesBytesAndEmitsOnce(t *testing.T) {
	t.Setenv("RELAY_UPSTREAM_EXECUTION_TIME_V1", "true")
	beforeSend := time.Now().UnixNano()
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
	require.GreaterOrEqual(t, records[0].StartedAtUnixNano, beforeSend)
	require.LessOrEqual(t, records[0].StartedAtUnixNano, time.Now().UnixNano())
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

func TestHTTPObservationIncompleteResponseIsNotCompletedAtEOF(t *testing.T) {
	var record Record
	ctx := WithRecorder(context.Background(), func(r Record) { record = r })
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.com/responses", nil)
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"incomplete","usage":{"input_tokens":10,"output_tokens":2}}`))}, nil
	})}
	resp, err := Do(client, req, "responses")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.False(t, record.Complete)
	require.Equal(t, "incomplete", record.Execution)
	// The partial valid metering remains procurement evidence.
	require.Equal(t, "verified", record.Observation.Status)
}

func TestDurableHTTPIntentDoesNotFollowRedirects(t *testing.T) {
	calls, admissions := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.URL.Path == "/first" {
			w.Header().Set("Location", "/second")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	}))
	defer server.Close()
	ctx := WithDurableRecorder(context.Background(), func() error { admissions++; return nil }, func(Record) error { return nil })
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/first", strings.NewReader(`{}`))
	client := &http.Client{}
	response, err := Do(client, req, "openai_chat")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 1, admissions)
	require.Equal(t, 1, calls)
	require.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
	require.Nil(t, client.CheckRedirect, "request policy must not mutate the shared client")
}

func TestDurableHTTPHeaderFailureRetainsIncompleteTerminal(t *testing.T) {
	failure := fmt.Errorf("header write failed")
	var stored []Record
	ctx := WithDurableRecorder(context.Background(), nil, func(r Record) error {
		if r.Execution == "checkpoint" {
			return failure
		}
		stored = append(stored, r)
		return nil
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://fixture.invalid/chat/completions", nil)
	client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"supplier-send"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	response, err := Do(client, req, "openai_chat")
	require.ErrorIs(t, err, failure)
	require.Nil(t, response)
	require.Len(t, stored, 1)
	require.Equal(t, "incomplete", stored[0].Execution)
	require.False(t, stored[0].Complete)
	require.Equal(t, "header_checkpoint_failed", stored[0].Observation.Reason)
	require.Equal(t, "supplier-send", stored[0].Observation.ProviderRequestID)
}

func TestHTTPExecutionTimingGateAndSSECheckpoints(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("RELAY_UPSTREAM_EXECUTION_TIME_V1", enabled)
			var records []Record
			ctx := WithDurableRecorder(context.Background(), nil, func(r Record) error { records = append(records, r); return nil })
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.com/chat/completions", nil)
			client := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n"))}, nil
			})}
			resp, err := Do(client, req, "openai_chat")
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.GreaterOrEqual(t, len(records), 2)
			for _, r := range records {
				require.Equal(t, records[0].StartedAtUnixNano, r.StartedAtUnixNano)
				if enabled == "true" {
					require.Positive(t, r.StartedAtUnixNano)
				} else {
					require.Zero(t, r.StartedAtUnixNano)
					encoded, err := jsonx.Marshal(r)
					require.NoError(t, err)
					require.NotContains(t, string(encoded), "started_at_unix_nano")
				}
			}
		})
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

func TestDurableEvidenceFailurePreventsForwardAndFinalSuccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`))
	}))
	defer server.Close()
	failure := fmt.Errorf("disk full")
	ctx := WithDurableRecorder(context.Background(), func() error { return failure }, func(Record) error { return nil })
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	_, err := Do(server.Client(), request, "openai_chat")
	require.ErrorIs(t, err, failure)
	require.Zero(t, calls)
	ctx = WithDurableRecorder(context.Background(), func() error { return nil }, func(r Record) error {
		if r.Execution == "checkpoint" {
			return nil
		}
		return failure
	})
	request, _ = http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	resp, err := Do(server.Client(), request, "openai_chat")
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, resp.Body.Close(), failure)
	require.Equal(t, 1, calls)
}

func TestP2ContractPinsSnapshotAndRejectsBeforeSupplierIO(t *testing.T) {
	d := contract.Document{Scope: contract.Scope{Kind: "channel", ID: 1, Model: "exact", Protocol: "openai_chat"}, Provider: "fixture", AdapterBuild: contract.BuildID, ParserRevision: ParserRevision, UsageRevision: ContractRevision, Inbound: []string{"openai_chat"}, FixtureEvidence: "independent", BasisRevision: "schema", Sets: []contract.RuleSet{{Level: "model", Rules: []contract.Rule{{ID: "rename", Kind: "rename", Path: "old_option", Target: "new_option", Evidence: "equivalence"}}}}}
	snap, err := contract.Compile(d)
	require.NoError(t, err)
	snap.BindingRevision = 4
	snap.IncidentRevision = 2
	calls := 0
	var record Record
	ctx := contract.WithSelection(context.Background(), contract.Selection{Snapshots: []contract.Snapshot{snap}, Inbound: "openai_chat"})
	ctx = WithRecorder(ctx, func(r Record) { record = r })
	client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "new_option")
		require.NotContains(t, string(body), "old_option")
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":0,"completion_tokens":0}}`))}, nil
	})}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://fixture.invalid/chat/completions", strings.NewReader(`{"old_option":0,"unknown":null}`))
	resp, err := Do(client, req, "openai_chat")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, calls)
	require.Equal(t, snap.Revision, record.ParameterContract.Revision)
	require.EqualValues(t, 4, record.ParameterContract.BindingRevision)
	require.Positive(t, record.StartedAtUnixNano)
	bad, _ := http.NewRequestWithContext(ctx, "POST", "https://fixture.invalid/chat/completions", strings.NewReader(`{"old_option":0,"new_option":1}`))
	_, err = Do(client, bad, "openai_chat")
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
