package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/platform/audit"

	"google.golang.org/grpc"
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

// sessionTokenIdentityClient is a minimal identity gRPC client mock for the
// session-token admin guard path. ValidateToken returns a valid admin user;
// GetUser returns the user with a configurable role.
type sessionTokenIdentityClient struct {
	identityv1.IdentityServiceClient
	userID int64
	role   int32
}

func (c *sessionTokenIdentityClient) ValidateToken(ctx context.Context, req *identityv1.ValidateTokenRequest, opts ...grpc.CallOption) (*identityv1.ValidateTokenReply, error) {
	return &identityv1.ValidateTokenReply{Valid: true, UserId: c.userID}, nil
}

func (c *sessionTokenIdentityClient) GetUser(ctx context.Context, req *identityv1.GetUserRequest, opts ...grpc.CallOption) (*identityv1.GetUserReply, error) {
	return &identityv1.GetUserReply{
		User: &commonv1.UserInfo{Id: req.UserId, Role: c.role, Status: 1},
	}, nil
}

// TestAdminGuard_StampsAuditActorSessionToken verifies the admin guard writes
// the audit actor with the real numeric user ID and role name for the
// session-token authentication path (as opposed to the shared ADMIN_TOKEN
// sentinel tested above).
func TestAdminGuard_StampsAuditActorSessionToken(t *testing.T) {
	const (
		adminUserID int64 = 42
		adminRole   int32 = 10 // service.RoleAdmin
	)
	// ADMIN_TOKEN unset so the shared-token path does not short-circuit.
	idClient := &sessionTokenIdentityClient{userID: adminUserID, role: adminRole}
	svc := service.NewAdminService(nil, idClient, nil, nil)
	guard := newAdminGuard(svc)

	captured := make(chan audit.ActorInfo, 1)
	handler := guard(func(w http.ResponseWriter, r *http.Request) {
		captured <- audit.ActorFrom(r.Context())
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/x", nil)
	req.Header.Set("Authorization", "Bearer session-token-for-user-42")
	handler(httptest.NewRecorder(), req)

	actor := <-captured
	if actor.UserID != adminUserID {
		t.Fatalf("session-token actor UserID = %d, want %d", actor.UserID, adminUserID)
	}
	if actor.Role != "admin" {
		t.Fatalf("session-token actor Role = %q, want %q", actor.Role, "admin")
	}
	if actor.ServiceName != "" {
		t.Fatalf("session-token actor ServiceName = %q, want empty (real user, not system)", actor.ServiceName)
	}
}

// TestAdminRoleName covers the role-name mapping including the "unknown"
// fallback for unexpected role values.
func TestAdminRoleName(t *testing.T) {
	tests := []struct {
		role int32
		want string
	}{
		{service.RoleRoot, "root"},
		{service.RoleAdmin, "admin"},
		{0, "unknown"},
		{999, "unknown"},
	}
	for _, tt := range tests {
		if got := adminRoleName(tt.role); got != tt.want {
			t.Errorf("adminRoleName(%d) = %q, want %q", tt.role, got, tt.want)
		}
	}
}
