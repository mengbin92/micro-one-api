package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"micro-one-api/domain/upstream/evidence"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

// P0 deliberately pins the old fee input while proving that the same response
// exposes a different native verdict. P1 must explicitly update this baseline.
func TestP0NativeShadowPreservesCurrentSettlementBaseline(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	for _, tc := range []struct{ name, body, shadow string }{
		{"ambiguous overwritten", `{"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":5}},"choices":[]}`, "ambiguous"},
		{"zero overwritten", `{"usage":{"prompt_tokens":0,"completion_tokens":0},"choices":[]}`, "verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			var baseline relaybiz.UsageEnvelope
			for _, enabled := range []string{"false", "true"} {
				t.Setenv("RELAY_USAGE_SHADOW", enabled)
				var records []evidence.Record
				ctx := evidence.WithRecorder(context.Background(), func(record evidence.Record) { records = append(records, record) })
				uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, orchestratorChannelClient{baseURL: upstream.URL + "/v1"}, nil, nil)
				hooks := &recordingLifecycleHooks{}
				executor := NewRelayOrchestratorWithDependencies(uc, relayprovider.NewProviderFactory(time.Second), hooks, nil)
				result, err := executor.Execute(ctx, &RelayRequest{Token: "client-token", Model: "gpt-4o-mini", Endpoint: EndpointChatCompletions, RequestID: "shadow-root", Body: strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`)})
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(result.Response)
				_ = result.Response.Close()
				if string(body) != tc.body {
					t.Fatal("shadow changed the client response")
				}
				if len(records) != 1 || records[0].Observation.Status != tc.shadow {
					t.Fatalf("native verdict lost: %+v", records)
				}
				if hooks.committed.ParseStatus != relaybiz.UsageParseEstimated || hooks.committed.BillableTotal() <= 0 {
					t.Fatalf("P0 changed current fee input: %+v", hooks.committed)
				}
				if enabled == "false" {
					baseline = hooks.committed
				} else if hooks.committed.BillableTotal() != baseline.BillableTotal() {
					t.Fatal("shadow changed settlement input")
				}
			}
		})
	}
}

func TestGeminiNativeShadowRunsBeforeTypedProjection(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	t.Setenv("RELAY_USAGE_SHADOW", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"reply"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":5,"totalTokenCount":17}}`)
	}))
	defer upstream.Close()
	factory := relayprovider.NewProviderFactory(time.Second)
	_ = NewHTTPServer(nil, nil, nil, factory, nil)
	f := newRelayAdaptorForwarder(factory, nil, &http.Client{Timeout: time.Second}, nil, nil)
	var records []evidence.Record
	ctx := evidence.WithRecorder(context.Background(), func(record evidence.Record) { records = append(records, record) })
	resp, err := f.Forward(ctx, &relaybiz.RelayPlan{Channel: &relaybiz.Channel{ID: 9, Type: relayprovider.ChannelTypeGemini, BaseURL: upstream.URL, Key: "secret"}, ResolvedModel: "ExactGeminiID"}, relaybiz.ExecutorRequest{Endpoint: "chat/completions", RequestID: "gemini-attempt", Body: []byte(`{"model":"ExactGeminiID","messages":[{"role":"user","content":"hi"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resp.Body), "thoughtsTokenCount") {
		t.Fatal("fixture no longer exercises typed projection")
	}
	if len(records) != 1 || records[0].Observation.Status != "unsupported" || !strings.Contains(strings.Join(records[0].Observation.Fingerprint, ","), "thoughtsTokenCount") {
		t.Fatalf("native dimension hidden by conversion: %+v", records)
	}
	if resp.Usage == nil || resp.Usage.ParseStatus != relaybiz.UsageParseVerified {
		t.Fatal("P0 changed existing client-protocol parser")
	}
}

func TestNativeWSShadowSeparateTurnsAndDuplicateCompletion(t *testing.T) {
	client := newFakeFrameConn(8)
	upstream := newFakeFrameConn(8)
	first := []byte(`{"type":"response.completed","response":{"id":"resp1","usage":{"input_tokens":10,"output_tokens":2}}}`)
	second := []byte(`{"type":"response.completed","response":{"id":"resp2","usage":{"input_tokens":20,"output_tokens":3,"tool_units":1}}}`)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: first}
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: first}
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: second}
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: first}
	close(upstream.in)
	go func() {
		for range client.out {
		}
	}()
	go func() {
		for range upstream.out {
		}
	}()
	var records []evidence.Record
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = evidence.WithRecorder(ctx, func(record evidence.Record) { records = append(records, record) })
	_, _ = relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{writeTimeout: time.Second, idleTimeout: time.Second})
	if len(records) != 2 || records[0].Observation.Buckets == nil || records[0].Observation.Buckets[0] != 10 || records[1].Observation.Status != "unsupported" {
		t.Fatalf("turns mixed or re-counted: %+v", records)
	}
}
