package service

import (
	"context"
	"testing"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	identitydata "micro-one-api/app/identity/internal/data"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIdentityServiceValidateTokenAcceptsSessionJWT(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo, nil)
	user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	_, sessionToken, err := uc.Login(context.Background(), "alice", "password123", "")
	if err != nil {
		t.Fatal(err)
	}

	svc := NewIdentityService(uc)
	resp, err := svc.ValidateToken(context.Background(), &identityv1.ValidateTokenRequest{Token: sessionToken})
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if !resp.GetValid() || resp.GetUserId() != user.ID || resp.GetTokenId() != 0 {
		t.Fatalf("ValidateToken() response = %+v, want session user id %d and token id 0", resp, user.ID)
	}
}

func TestIdentityServiceValidateTokenRejectsAPIKey(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo, nil)
	user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	apiToken, err := uc.CreateAccessToken(context.Background(), user.ID, "work-token", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewIdentityService(uc)
	_, err = svc.ValidateToken(context.Background(), &identityv1.ValidateTokenRequest{Token: apiToken.Key})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ValidateToken() error code = %v, want %v (err=%v)", status.Code(err), codes.Unauthenticated, err)
	}
}

func TestIdentityServiceSetUserRoleBindsOperatorToSession(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo, nil)
	admin, err := uc.Register(context.Background(), "admin", "password123", "admin@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	admin.Role = biz.RoleAdminUser
	if err := repo.UpdateUser(context.Background(), admin); err != nil {
		t.Fatal(err)
	}
	target, err := uc.Register(context.Background(), "target", "password123", "target@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	_, sessionToken, err := uc.Login(context.Background(), "admin", "password123", "")
	if err != nil {
		t.Fatal(err)
	}

	svc := NewIdentityService(uc)
	ctx := ServiceAuthenticatedContext(context.Background())
	ctx = WithOperatorCredential(ctx, sessionToken, false)
	resp, err := svc.SetUserRole(ctx, &identityv1.SetUserRoleRequest{
		UserId: target.ID, Role: biz.RoleGuestUser, OperatorUserId: admin.ID,
	})
	if err != nil || !resp.GetSuccess() {
		t.Fatalf("SetUserRole() = %+v, %v", resp, err)
	}

	_, err = svc.SetUserRole(ctx, &identityv1.SetUserRoleRequest{
		UserId: target.ID, Role: biz.RoleCommonUser, OperatorUserId: target.ID,
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("spoofed operator error = %v, want unauthenticated", err)
	}
}

func TestIdentityServiceSetUserRoleRequiresOperatorCredential(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	svc := NewIdentityService(biz.NewIdentityUsecase(repo, nil))
	ctx := ServiceAuthenticatedContext(context.Background())
	_, err := svc.SetUserRole(ctx, &identityv1.SetUserRoleRequest{UserId: 1, Role: biz.RoleAdminUser})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("SetUserRole() error = %v, want unauthenticated", err)
	}
}

// TestGetAuthSnapshotErrorCodeMapping verifies that each identity biz error
// maps to the correct gRPC code via GetAuthSnapshot. This is the regression
// guard for the error-code mismatch that caused TOKEN_EXHAUSTED (and others)
// to be misreported as NotFound → HTTP 401 instead of their true semantics.
func TestGetAuthSnapshotErrorCodeMapping(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo, nil)
	svc := NewIdentityService(uc)

	user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}

	// Exhausted token: unlimited=false, remain=0.
	exhausted, err := uc.CreateAccessToken(context.Background(), user.ID, "exhausted", nil, 0,
		biz.CreateAccessTokenOptions{RemainQuota: 100, UnlimitedQuota: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.UpdateAccessTokenWithOptions(context.Background(), user.ID, exhausted.ID,
		biz.UpdateAccessTokenOptions{RemainQuota: 0, UnlimitedQuota: false, Status: 1}); err != nil {
		t.Fatal(err)
	}

	// Disabled token.
	disabled, err := uc.CreateAccessToken(context.Background(), user.ID, "disabled", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.UpdateAccessTokenWithOptions(context.Background(), user.ID, disabled.ID,
		biz.UpdateAccessTokenOptions{Name: "disabled", Status: biz.TokenStatusDisabled}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		token     string
		wantCode  codes.Code
	}{
		{"nonexistent token", "does-not-exist", codes.Unauthenticated},
		{"exhausted token", exhausted.Key, codes.ResourceExhausted},
		{"disabled token", disabled.Key, codes.PermissionDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.GetAuthSnapshot(context.Background(), &identityv1.GetAuthSnapshotRequest{Token: tt.token})
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("GetAuthSnapshot(%q) code = %v, want %v (err=%v)", tt.token, got, tt.wantCode, err)
			}
		})
	}
}
