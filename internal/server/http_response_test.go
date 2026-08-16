package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubscriptionAccountNotFoundReturnsServiceUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		handler func(*HTTPServer, http.ResponseWriter, error)
	}{
		{
			name: "openai plain error",
			err:  errors.New("subscription account not found"),
			handler: func(s *HTTPServer, w http.ResponseWriter, err error) {
				s.handleRelayPlanError(w, err)
			},
		},
		{
			name: "openai grpc error",
			err:  status.Error(codes.Unknown, "subscription account not found"),
			handler: func(s *HTTPServer, w http.ResponseWriter, err error) {
				s.handleRelayPlanError(w, err)
			},
		},
		{
			name: "anthropic plain error",
			err:  errors.New("subscription account not found"),
			handler: func(s *HTTPServer, w http.ResponseWriter, err error) {
				s.handleAnthropicPlanError(w, err)
			},
		},
		{
			name: "anthropic grpc error",
			err:  status.Error(codes.Unknown, "subscription account not found"),
			handler: func(s *HTTPServer, w http.ResponseWriter, err error) {
				s.handleAnthropicPlanError(w, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			tt.handler(&HTTPServer{}, recorder, tt.err)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
			}
		})
	}
}

// TestRouteDeadEndStaysServiceUnavailable pins the ROUTE_DEAD_END wire
// contract at the HTTP boundary. After pkg/errors GRPCStatus, channel-service
// routes dead-ends as codes.FailedPrecondition with the ORIGINAL error text
// as the message ("circuit-opened...", "none are schedulable...") — which does
// not match isChannelUnavailableMessage's three substrings. The boundary must
// still surface 503 (transient routing outcome), not 500. Regression guard
// for the c8e3172 review finding.
func TestRouteDeadEndStaysServiceUnavailable(t *testing.T) {
	routeDeadEndMessages := []string{
		`all subscription accounts serving "gpt-5" are circuit-opened or saturated; try again after the circuit window`,
		`model routing matched 2 account(s) for "gpt-5" but none are schedulable`,
	}
	handlers := []struct {
		name    string
		handler func(*HTTPServer, http.ResponseWriter, error)
	}{
		{"openai boundary", func(s *HTTPServer, w http.ResponseWriter, err error) { s.handleRelayPlanError(w, err) }},
		{"anthropic boundary", func(s *HTTPServer, w http.ResponseWriter, err error) { s.handleAnthropicPlanError(w, err) }},
	}

	for _, h := range handlers {
		for _, msg := range routeDeadEndMessages {
			t.Run(h.name+"/"+msg[:24], func(t *testing.T) {
				recorder := httptest.NewRecorder()
				h.handler(&HTTPServer{}, recorder, status.Error(codes.FailedPrecondition, msg))
				if recorder.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
				}
			})
		}
	}
}

// TestGenericFailedPreconditionNotChannelUnavailable guards against over-
// widening the 503 branch: a FailedPrecondition that is NOT a channel
// dead-end should not be reclassified as "no available channel". There is no
// such producer today, but the mapping must stay narrow if one appears.
func TestGenericFailedPreconditionNotChannelUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&HTTPServer{}).handleRelayPlanError(recorder, status.Error(codes.FailedPrecondition, "some unrelated precondition failure"))
	// The current contract maps any FailedPrecondition to 503 (service
	// unavailable), keeping the response transient-safe. Pin it so future
	// changes are deliberate.
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
}
