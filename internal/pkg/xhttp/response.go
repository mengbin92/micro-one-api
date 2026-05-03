package xhttp

import (
	"encoding/json"
	"net/http"
)

// Response is the standard API response envelope.
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ErrorDetail is returned on error responses.
type ErrorDetail struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// JSON writes a successful JSON response.
func JSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(Response{
		Code: http.StatusOK,
		Data: data,
	})
}

// Error writes an error JSON response.
func Error(w http.ResponseWriter, code int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(Response{
		Code: code,
		Data: ErrorDetail{
			Reason:  reason,
			Message: message,
		},
	})
}
