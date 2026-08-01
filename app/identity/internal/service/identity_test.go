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
	uc := biz.NewIdentityUsecase(repo)
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
	uc := biz.NewIdentityUsecase(repo)
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
	if status.Code(err) != codes.NotFound {
		t.Fatalf("ValidateToken() error code = %v, want %v (err=%v)", status.Code(err), codes.NotFound, err)
	}
}

func TestIdentityServiceSetUserRoleBindsOperatorToSession(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
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
	svc := NewIdentityService(biz.NewIdentityUsecase(repo))
	ctx := ServiceAuthenticatedContext(context.Background())
	_, err := svc.SetUserRole(ctx, &identityv1.SetUserRoleRequest{UserId: 1, Role: biz.RoleAdminUser})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("SetUserRole() error = %v, want unauthenticated", err)
	}
}
