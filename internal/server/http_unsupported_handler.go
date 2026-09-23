package server

import (
	"fmt"
	"net/http"
)

func (s *HTTPServer) handleUnsupportedOpenAIRoute(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.writeNotImplemented(w, fmt.Sprintf("%s is not implemented", feature))
	}
}
func (s *HTTPServer) writeNotImplemented(w http.ResponseWriter, message string) {
	s.writeJSON(w, http.StatusNotImplemented, map[string]any{"error": errorIdentity(w, map[string]any{
		"message": message, "type": "one_api_not_implemented", "param": nil, "code": "not_implemented",
		"request_id": w.Header().Get("X-Request-ID"),
	})})
}
