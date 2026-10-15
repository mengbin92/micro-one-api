package evidence

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"micro-one-api/pkg/jsonx"
)

func TestNativeFixtures(t *testing.T) {
	body, err := os.ReadFile("testdata/native-usage.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Protocol, Status, Reason string
		Body                           jsonx.RawMessage
		Buckets                        *[5]int64
	}
	if err := jsonx.Unmarshal(body, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := Parse(tc.Body, tc.Protocol)
			if got.Status != tc.Status || got.Reason != tc.Reason {
				t.Fatalf("status/reason = %s/%s, want %s/%s", got.Status, got.Reason, tc.Status, tc.Reason)
			}
			if tc.Buckets != nil && (got.Buckets == nil || *got.Buckets != *tc.Buckets) {
				t.Fatalf("buckets = %v, want %v", got.Buckets, *tc.Buckets)
			}
			if got.Status != "verified" && got.Buckets != nil {
				t.Fatal("untrusted candidate fabricated canonical usage")
			}
			encoded, err := jsonx.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("private conversation")) {
				t.Fatal("content leaked into evidence")
			}
		})
	}
}

func TestNativeStrictInput(t *testing.T) {
	for _, body := range []string{
		`{"usage":{"prompt_tokens":1,"prompt_tokens":2,"completion_tokens":0}}`,
		`{"usage":{"prompt_tokens_details":{"cached_tokens":0,"cached_tokens":1},"prompt_tokens":1,"completion_tokens":0}}`,
	} {
		if got := Parse([]byte(body), "openai_chat"); got.Status != "invalid" || got.Reason != "duplicate_key" {
			t.Fatalf("duplicate accepted: %+v", got)
		}
	}
	for _, body := range []string{`{`, `{"usage":{}} {}`, `[]`} {
		if got := Parse([]byte(body), "openai_chat"); got.Status != "invalid" {
			t.Fatalf("malformed accepted: %+v", got)
		}
	}
	if got := Parse(bytes.Repeat([]byte("x"), MaxFrameBytes+1), "openai_chat"); got.Status != "unobserved" {
		t.Fatal(got)
	}
}

func TestNativeFingerprintPresenceAndRedaction(t *testing.T) {
	zero := Parse([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0}}`), "openai_chat")
	missing := Parse([]byte(`{"usage":{"completion_tokens":0}}`), "openai_chat")
	null := Parse([]byte(`{"usage":{"prompt_tokens":null,"completion_tokens":0}}`), "openai_chat")
	if strings.Join(zero.Fingerprint, ",") == strings.Join(missing.Fingerprint, ",") || strings.Join(zero.Fingerprint, ",") == strings.Join(null.Fingerprint, ",") {
		t.Fatal("presence/types conflated")
	}
	changedValue := Parse([]byte(`{"usage":{"prompt_tokens":123,"completion_tokens":0}}`), "openai_chat")
	if strings.Join(zero.Fingerprint, ",") != strings.Join(changedValue.Fingerprint, ",") {
		t.Fatal("fingerprint included numeric values")
	}
	redacted := Parse([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"unknown":"secret-token"},"messages":[{"content":"secret-conversation"}]}`), "openai_chat")
	encoded, _ := jsonx.Marshal(redacted)
	if bytes.Contains(encoded, []byte("secret-")) || redacted.UsageDigest == "" {
		t.Fatalf("leaked value: %s", encoded)
	}
}

func TestNativeStreamCumulativeReplayAndExplicitZero(t *testing.T) {
	tracker := NewTracker("responses")
	frame := `{"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":10,"output_tokens":2}}}`
	tracker.ObserveFrame([]byte(frame))
	tracker.ObserveFrame([]byte(frame))
	got, completed := tracker.Finish()
	if !completed || got.Buckets == nil || *got.Buckets != [5]int64{10, 0, 0, 0, 2} {
		t.Fatalf("duplicate added usage: %+v, %v", got, completed)
	}
	tracker = NewTracker("anthropic_messages")
	tracker.ObserveFrame([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":400,"output_tokens":0,"cache_read_input_tokens":600}}}`))
	tracker.ObserveFrame([]byte(`{"type":"message_delta","usage":{"output_tokens":2}}`))
	tracker.ObserveFrame([]byte(`{"type":"message_delta","usage":{"output_tokens":5}}`))
	tracker.ObserveFrame([]byte(`{"type":"message_delta","usage":{"output_tokens":0}}`))
	tracker.ObserveFrame([]byte(`{"type":"message_stop"}`))
	got, completed = tracker.Finish()
	if !completed || got.Buckets == nil || *got.Buckets != [5]int64{400, 600, 0, 0, 0} {
		t.Fatalf("snapshot/zero ignored: %+v, %v", got, completed)
	}
}

func TestNativeSSEBoundaries(t *testing.T) {
	complete := "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\r\n\r\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, step := range []int{1, 7, len(complete)} {
		tracker := NewTracker("anthropic_messages")
		for start := 0; start < len(complete); start += step {
			_, _ = tracker.Write([]byte(complete[start:min(start+step, len(complete))]))
		}
		got, finished := tracker.Finish()
		if !finished || got.Status != "verified" || got.Buckets == nil || got.Buckets[0] != 2 {
			t.Fatalf("step %d: %+v / %v", step, got, finished)
		}
	}
	for _, stream := range []string{
		"data: {\"type\":\"message_stop\"}",
		"event: message_stop\n\n",
		"data: {\"type\":\"message_stop\"}\n",
		"data: {\"type\":\"error\"}\n\ndata: {\"type\":\"message_stop\"}\n\n",
	} {
		tracker := NewTracker("anthropic_messages")
		_, _ = tracker.Write([]byte(stream))
		if _, complete := tracker.Finish(); complete {
			t.Fatalf("invalid termination accepted: %q", stream)
		}
	}
}

func TestNativeStreamFaultsStayVisibleAndBounded(t *testing.T) {
	tracker := NewTracker("openai_chat")
	tracker.ObserveFrame([]byte(`{"usage":{"prompt_tokens":-1,"completion_tokens":2}}`))
	for range MaxEvents + 10 {
		tracker.ObserveFrame([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}
	got, complete := tracker.Finish()
	if got.Status != "invalid" || complete || len(tracker.Events) != MaxEvents || tracker.DroppedEvents != 11 {
		t.Fatalf("fault/bound lost: %+v, %d", got, tracker.DroppedEvents)
	}
	tracker = NewTracker("openai_chat")
	_, _ = tracker.Write(bytes.Repeat([]byte("a"), MaxFrameBytes+100))
	if len(tracker.pending) != 0 {
		t.Fatal("unbounded line buffer")
	}
	got, _ = tracker.Finish()
	if got.Status != "unobserved" {
		t.Fatal(got)
	}
	tracker = NewTracker("responses")
	tracker.ObserveFrame([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"new_dimension":4}}}`))
	got, _ = tracker.Finish()
	if got.Status != "unsupported" || got.Buckets != nil {
		t.Fatal("unknown dimension became verified")
	}
}

func TestNativeChatStreamNullUsageAndTermination(t *testing.T) {
	tracker := NewTracker("openai_chat")
	for _, frame := range []string{
		`{"id":"chat-r1","object":"chat.completion.chunk","choices":[{"delta":{"content":"OK"},"finish_reason":null}],"usage":null}`,
		`{"object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}],"usage":null}`,
		`{"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
	} {
		_, _ = tracker.Write([]byte("data: " + frame + "\n\n"))
	}
	got, complete := tracker.Finish()
	if complete {
		t.Fatal("finish_reason accepted without the SSE terminal marker")
	}
	_, _ = tracker.Write([]byte("data: [DONE]\n\n"))
	got, complete = tracker.Finish()
	if !complete || got.Status != "verified" || got.Buckets == nil || *got.Buckets != [5]int64{10, 0, 0, 0, 2} || got.ProviderRequestID != "chat-r1" {
		t.Fatalf("standard stream lost usage or identity: %+v complete=%v", got, complete)
	}
	if got := Parse([]byte(`{"usage":null}`), "openai_chat"); got.Status != "invalid" {
		t.Fatal("non-stream null usage accepted")
	}
}

func TestNativeTokenDetailsAndAmbiguousCandidatesCannotOverflow(t *testing.T) {
	for _, body := range []string{
		`{"usage":{"prompt_tokens":1,"completion_tokens":2,"completion_tokens_details":{"reasoning_tokens":3}}}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":9223372036854775807,"prompt_tokens_details":{"cached_tokens":5}}}`,
	} {
		got := Parse([]byte(body), "openai_chat")
		if got.Status != "invalid" || got.Buckets != nil {
			t.Fatalf("contradictory or overflowing evidence accepted: %+v", got)
		}
	}
	got := Parse([]byte(`{"usage":{"input_tokens":1,"output_tokens":2,"output_tokens_details":{"reasoning_tokens":3}}}`), "responses")
	if got.Status != "invalid" || got.Buckets != nil {
		t.Fatalf("responses reasoning subset exceeds output: %+v", got)
	}
}
