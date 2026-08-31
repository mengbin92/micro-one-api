package server

import (
	"fmt"
	"net/http"
)

func (s *HTTPServer) handleUnsupportedOpenAIRoute(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("%s is not implemented", feature),
				"type":    "one_api_not_implemented",
				"param":   nil,
				"code":    "not_implemented",
			},
		})
	}
}
