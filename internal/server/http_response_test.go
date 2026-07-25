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
