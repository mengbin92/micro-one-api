package passthrough

import (
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   Kind
	}{
		{name: "429 retryable passthrough", status: http.StatusTooManyRequests, kind: KindRetryablePassthrough},
		{name: "401 passthrough", status: http.StatusUnauthorized, kind: KindPassthrough},
		{name: "403 passthrough", status: http.StatusForbidden, kind: KindPassthrough},
		{name: "cyber policy", status: http.StatusBadRequest, body: `{"error":"cyber_policy"}`, kind: KindCyberBlocked},
		{name: "5xx retryable", status: http.StatusBadGateway, kind: KindRetryable},
		{name: "529 overloaded", status: StatusOverloaded, kind: KindOverloaded},
		{name: "409 same account", status: http.StatusConflict, kind: KindRetryableOnSameAccount},
		{name: "423 same account", status: http.StatusLocked, kind: KindRetryableOnSameAccount},
		{name: "other 4xx non retryable", status: http.StatusBadRequest, kind: KindNonRetryable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.status, []byte(tt.body))
			if got.Kind != tt.kind {
				t.Fatalf("kind = %s, want %s", got.Kind, tt.kind)
			}
		})
	}
}

// TestRetryableAndPassthroughSemantics pins the dual nature of a 429: it must
// fail over across accounts AND, once exhausted, be passed through to the
// client with its Retry-After intact.
func TestRetryableAndPassthroughSemantics(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		retryable   bool
		passthrough bool
	}{
		{name: "429", status: http.StatusTooManyRequests, retryable: true, passthrough: true},
		{name: "529 overloaded", status: StatusOverloaded, retryable: true, passthrough: true},
		{name: "401", status: http.StatusUnauthorized, retryable: false, passthrough: true},
		{name: "403", status: http.StatusForbidden, retryable: false, passthrough: true},
		{name: "5xx", status: http.StatusBadGateway, retryable: true, passthrough: false},
		{name: "409 same account", status: http.StatusConflict, retryable: false, passthrough: false},
		{name: "cyber", status: http.StatusBadRequest, body: `{"error":"cyber_policy"}`, retryable: false, passthrough: true},
		{name: "other 4xx", status: http.StatusBadRequest, retryable: false, passthrough: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.status, []byte(tt.body))
			if got.RetryableAcrossAccounts() != tt.retryable {
				t.Fatalf("RetryableAcrossAccounts = %v, want %v", got.RetryableAcrossAccounts(), tt.retryable)
			}
			if got.ShouldPassthrough() != tt.passthrough {
				t.Fatalf("ShouldPassthrough = %v, want %v", got.ShouldPassthrough(), tt.passthrough)
			}
		})
	}
}
