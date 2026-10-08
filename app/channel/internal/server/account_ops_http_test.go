package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountOpsHTTPServerExposesOnlyHealthAndMetrics(t *testing.T) {
	srv := NewAccountOpsHTTPServer(":0")
	for _, path := range []string{"/healthz", "/metrics"} {
		response := httptest.NewRecorder()
		srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code)
	}
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/subscription/oauth/codex/exchange", nil))
	require.Equal(t, http.StatusNotFound, response.Code)
}
