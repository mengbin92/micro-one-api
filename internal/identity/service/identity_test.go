package service

import (
	"context"
	"testing"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/identity/biz"
	identitydata "micro-one-api/internal/identity/data"

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
	_, sessionToken, err := uc.Login(context.Background(), "alice", "password123")
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
