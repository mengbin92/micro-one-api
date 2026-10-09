package server

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompletedIAMBusinessRouteShapes(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		ready        bool
	}{
		{http.MethodGet, "/api/admin/request-logs/export", true},
		{http.MethodPost, "/api/admin/request-logs/purge", true},
		{http.MethodPut, "/api/admin/notification-rules/2", true},
		{http.MethodPost, "/api/admin/notifications/2/acknowledge", true},
		{http.MethodGet, "/api/v1/admin/reports/cost:export", true},
		{http.MethodPost, "/api/v1/admin/accounts/2/balance:reset", true},
		{http.MethodPost, "/api/reconciliation", true},
		{http.MethodGet, "/api/log/attempts", true},
		{http.MethodPatch, "/api/v1/admin/routing-groups/2/billing", true},
		{http.MethodPut, "/api/v1/admin/routing-groups/2/user-price/3", true},
		{http.MethodGet, "/api/v1/admin/routing-access/3/available", true},
		{http.MethodPost, "/api/v1/admin/subscriptions/2/revoke", true},
		{http.MethodDelete, "/api/v1/admin/subscription-plans/2", true},
		{http.MethodGet, "/api/admin/models/2/channels", true},
		{http.MethodGet, "/api/channel/disable/9", true},
		{http.MethodPost, "/api/channel/disable/9", true},
		{http.MethodPost, "/api/channel/enable/9", true},
		{http.MethodPut, "/api/channel/disable/9", false},
		{http.MethodPost, "/api/channel/disable/9/extra", false},
		{http.MethodPost, "/api/admin/models/2/usage", false},
		{http.MethodPost, "/api/admin/request-logs/export", false},
		{http.MethodPost, "/api/admin/request-logs/2/delete-everything", false},
		{http.MethodPut, "/api/admin/notification-rules/2/anything", false},
		{http.MethodPost, "/api/v1/admin/accounts/2/balance:reset/extra", false},
		{http.MethodGet, "/api/admin/configs/ns/key/extra", false},
		{http.MethodPost, "/api/v1/admin/subscriptions/2/unknown", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			require.Equal(t, tc.ready, iamUserRouteReady(httptest.NewRequest(tc.method, tc.path, nil)))
		})
	}
}
