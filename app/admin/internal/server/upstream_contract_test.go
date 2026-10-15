package server

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestP2OperatorRoutesDoNotExposeSystemProbeAPIs(t *testing.T) {
	for _, route := range []string{"/api/admin/upstream-contracts", "/api/admin/vendor-reports"} {
		require.True(t, iamOwnerRouteReady(route, http.MethodGet))
		require.True(t, iamOwnerRouteReady(route, http.MethodPost))
		require.False(t, iamOwnerRouteReady(route, http.MethodDelete))
	}
	require.True(t, iamOwnerRouteReady("/api/admin/upstream-contracts/transition", http.MethodPost))
	require.False(t, iamOwnerRouteReady("/api/admin/upstream-contracts/probe", http.MethodPost))
	require.False(t, iamOwnerRouteReady("/api/admin/vendor-reports/refund", http.MethodPost))
}
