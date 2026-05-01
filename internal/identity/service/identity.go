package service

import (
	"context"

	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/identity/biz"
)

// IdentityService is the transport layer entry for identity-service.
type IdentityService struct {
	identityv1.UnimplementedIdentityServiceServer
	uc *biz.IdentityUsecase
}

func NewIdentityService(uc *biz.IdentityUsecase) *IdentityService {
	return &IdentityService{uc: uc}
}

func (s *IdentityService) ValidateTokenModel(ctx context.Context, token string) (*biz.Token, error) {
	return s.uc.ValidateToken(ctx, token)
}

func (s *IdentityService) GetAuthSnapshotModel(ctx context.Context, token string) (*biz.AuthSnapshot, error) {
	return s.uc.GetAuthSnapshot(ctx, token)
}

func (s *IdentityService) GetUserModel(ctx context.Context, userID int64) (*biz.User, error) {
	return s.uc.GetUser(ctx, userID)
}

func (s *IdentityService) ValidateToken(ctx context.Context, req *identityv1.ValidateTokenRequest) (*identityv1.ValidateTokenReply, error) {
	token, err := s.uc.ValidateToken(ctx, req.Token)
	if err != nil {
		return &identityv1.ValidateTokenReply{
			Valid:   false,
			Message: err.Error(),
		}, nil
	}
	return &identityv1.ValidateTokenReply{
		Valid:   true,
		UserId:  token.UserID,
		TokenId: token.ID,
		Message: "ok",
	}, nil
}

func (s *IdentityService) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest) (*identityv1.GetAuthSnapshotReply, error) {
	snapshot, err := s.uc.GetAuthSnapshot(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	return &identityv1.GetAuthSnapshotReply{
		UserId:        snapshot.UserID,
		TokenId:       snapshot.TokenID,
		Group:         snapshot.Group,
		AllowedModels: snapshot.AllowedModels,
		UserEnabled:   snapshot.UserEnabled,
		TokenEnabled:  snapshot.TokenEnabled,
	}, nil
}

func (s *IdentityService) GetUser(ctx context.Context, req *identityv1.GetUserRequest) (*identityv1.GetUserReply, error) {
	user, err := s.uc.GetUser(ctx, req.UserId)
	if err != nil {
		return nil, err
	}
	return &identityv1.GetUserReply{
		UserId:      user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Group:       user.Group,
		Status:      user.Status,
	}, nil
}
