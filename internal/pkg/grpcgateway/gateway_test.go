package grpcgateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	logv1 "micro-one-api/api/log/v1"
)

func TestNewServeMuxForwardsAuthorizationHeader(t *testing.T) {
	mdKey, ok := incomingHeaderMatcher("Authorization")
	if !ok || mdKey != "authorization" {
		t.Fatalf("authorization header matcher = %q, %v; want authorization, true", mdKey, ok)
	}
}

func TestNewServeMuxUsesProtoJSONNames(t *testing.T) {
	mux := NewServeMux()
	req, err := http.NewRequest(http.MethodGet, "/v1/logs/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, marshaler := runtime.MarshalerForRequest(mux, req)
	payload, err := marshaler.Marshal(&logv1.GetLogResponse{
		RequestId:        "req-1",
		PromptTokens:     10,
		CompletionTokens: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	for _, want := range []string{`"request_id"`, `"prompt_tokens"`, `"completion_tokens"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("gateway json missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "requestId") || strings.Contains(body, "promptTokens") {
		t.Fatalf("gateway json used camelCase field names: %s", body)
	}
}
