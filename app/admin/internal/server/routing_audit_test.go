package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoutingAuditAuthorizationAndReadback(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-test")
	t.Setenv("SERVICE_TOKEN", "service-test")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/selection-events", r.URL.Path)
		require.Equal(t, "Bearer service-test", r.Header.Get("Authorization"))
		require.Equal(t, "42", r.URL.Query().Get("user_id"))
		require.Equal(t, "root-a", r.URL.Query().Get("root_request_id"))
		_, _ = w.Write([]byte(`{"items":[{"planned":false,"fallback_reason":"upstream_5xx"}],"retention":"30 days"}`))
	}))
	defer upstream.Close()
	t.Setenv("LOG_HTTP_ENDPOINT", upstream.URL)
	billing := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billing)
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/api/log/routing-audit?user_id=42&root_request_id=root-a", "", 401},
		{"/api/log/routing-audit?user_id=42", "admin-test", 400},
		{"/api/log/routing-audit?user_id=42&root_request_id=root-a&page=2&page_size=100", "admin-test", 200},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, rec.Body.String())
		if tc.status == 200 {
			require.Contains(t, rec.Body.String(), "upstream_5xx")
			require.Contains(t, rec.Body.String(), "reservation-2")
			require.EqualValues(t, 2, billing.attemptsLastReq.Page)
		}
	}
}

func TestLedgerListAndExportUseSameOrdering(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-test")
	billing := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billing)
	for _, path := range []string{"/api/log", "/api/log/export"} {
		req := httptest.NewRequest(http.MethodGet, path+"?format=csv&sort=amount&order=asc&page=2&page_size=10&user_id=42", nil)
		req.Header.Set("Authorization", "Bearer admin-test")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		require.Equal(t, "amount asc", billing.ledgerListLastReq.OrderBy)
		require.EqualValues(t, 2, billing.ledgerListLastReq.Page)
		require.Equal(t, "42", billing.ledgerListLastReq.UserId)
	}
}

func TestSafeCSVCellNeutralizesFormulaText(t *testing.T) {
	require.Equal(t, "'=SUM(A1:A2)", safeCSVCell("=SUM(A1:A2)"))
	require.Equal(t, "'-cmd", safeCSVCell("-cmd"))
	require.Equal(t, "-12.5", safeCSVCell("-12.5"))
}
