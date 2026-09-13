package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micro-one-api/pkg/jsonx"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionGroupCreatePreservesExplicitDisabledStatus(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	for _, tt := range []struct {
		body string
		want int32
	}{
		{`{"name":"quota-policy"}`, 1},
		{`{"name":"quota-policy","status":0}`, 0},
		{`{"name":"quota-policy","status":1}`, 1},
	} {
		t.Run(tt.body, func(t *testing.T) {
			srv := newPlanLifecycleTestServer()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscription-groups", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer admin-token")
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			srv.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var created struct {
				Success bool                 `json:"success"`
				Data    subscriptionGroupDTO `json:"data"`
			}
			require.NoError(t, jsonx.Unmarshal(recorder.Body.Bytes(), &created))
			require.True(t, created.Success, recorder.Body.String())
			require.Equal(t, tt.want, created.Data.Status)

			req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/subscription-groups/1", nil)
			req.Header.Set("Authorization", "Bearer admin-token")
			recorder = httptest.NewRecorder()
			srv.ServeHTTP(recorder, req)
			require.NoError(t, jsonx.Unmarshal(recorder.Body.Bytes(), &created))
			require.True(t, created.Success)
			require.Equal(t, tt.want, created.Data.Status, "persisted policy status must match the request")
		})
	}
}
