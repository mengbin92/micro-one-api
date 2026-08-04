package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"micro-one-api/platform/audit"
)

// TestAdminGuard_StampsAuditActor verifies the admin guard writes the audit
// actor via the platform/audit standard key for the shared ADMIN_TOKEN path
// (system operator sentinel). The session-token path uses the same audit.
// WithActor call with the authorized numeric user id; it is covered by the
// identical call site in newAdminGuard.
func TestAdminGuard_StampsAuditActor(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "shared-secret")
	guard := newAdminGuard(nil) // svc nil is legal: shared-token path runs first
	captured := make(chan audit.ActorInfo, 1)
	handler := guard(func(w http.ResponseWriter, r *http.Request) {
		captured <- audit.ActorFrom(r.Context())
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/x", nil)
	req.Header.Set("Authorization", "Bearer shared-secret")
	handler(httptest.NewRecorder(), req)
	actor := <-captured
	if actor.ServiceName != "admin" || actor.Username != "admin-token" {
		t.Fatalf("shared-token actor = %+v, want system/admin-token sentinel", actor)
	}
}
