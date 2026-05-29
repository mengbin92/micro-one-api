package service

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/identity/biz"
	"micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
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
	user, err := s.uc.ValidateSessionToken(ctx, req.Token)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &identityv1.ValidateTokenReply{
		Valid:   true,
		UserId:  user.ID,
		TokenId: 0,
		Message: "ok",
	}, nil
}

func (s *IdentityService) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest) (*identityv1.GetAuthSnapshotReply, error) {
	snapshot, err := s.uc.GetAuthSnapshot(ctx, req.Token)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &identityv1.GetAuthSnapshotReply{
		UserId:        snapshot.UserID,
		TokenId:       snapshot.TokenID,
		TokenName:     snapshot.TokenName,
		Group:         snapshot.Group,
		AllowedModels: snapshot.AllowedModels,
		UserEnabled:   snapshot.UserEnabled,
		TokenEnabled:  snapshot.TokenEnabled,
	}, nil
}

func (s *IdentityService) GetUser(ctx context.Context, req *identityv1.GetUserRequest) (*identityv1.GetUserReply, error) {
	user, err := s.uc.GetUser(ctx, req.UserId)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &identityv1.GetUserReply{
		User: &commonv1.UserInfo{
			Id:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			Email:       user.Email,
			Group:       user.Group,
			Status:      user.Status,
			Role:        user.Role,
		},
	}, nil
}

func (s *IdentityService) Login(ctx context.Context, req *identityv1.LoginRequest) (*identityv1.LoginResponse, error) {
	user, token, err := s.uc.Login(ctx, req.Username, req.Password)
	if err != nil {
		return &identityv1.LoginResponse{
			Success: false,
			Message: "invalid credentials",
		}, nil
	}
	return &identityv1.LoginResponse{
		Success: true,
		Message: "ok",
		Token:   token,
		UserId:  user.ID,
	}, nil
}

func (s *IdentityService) Register(ctx context.Context, req *identityv1.RegisterRequest) (*identityv1.RegisterResponse, error) {
	user, err := s.uc.Register(ctx, req.Username, req.Password, req.Email, req.Group)
	if err != nil {
		return &identityv1.RegisterResponse{
			Success: false,
			Message: "registration failed",
		}, nil
	}
	return &identityv1.RegisterResponse{
		Success: true,
		Message: "ok",
		UserId:  user.ID,
	}, nil
}

func (s *IdentityService) CreateAccessToken(ctx context.Context, req *identityv1.CreateAccessTokenRequest) (*identityv1.CreateAccessTokenResponse, error) {
	token, err := s.uc.CreateAccessToken(ctx, req.UserId, req.Name, req.Models, req.ExpireAt)
	if err != nil {
		return &identityv1.CreateAccessTokenResponse{
			Success: false,
			Message: "failed to create access token",
		}, nil
	}
	return &identityv1.CreateAccessTokenResponse{
		Success: true,
		Message: "ok",
		Token:   token.Key,
		TokenId: token.ID,
	}, nil
}

func (s *IdentityService) ListUsers(ctx context.Context, req *identityv1.ListUsersRequest) (*identityv1.ListUsersResponse, error) {
	users, total, err := s.uc.ListUsers(ctx, req.Page, req.PageSize, req.Keyword, req.Group, req.Status)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	result := make([]*commonv1.UserInfo, len(users))
	for i, u := range users {
		result[i] = &commonv1.UserInfo{
			Id:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Email:       u.Email,
			Group:       u.Group,
			Status:      u.Status,
			Role:        u.Role,
		}
	}
	return &identityv1.ListUsersResponse{
		Users: result,
		Total: total,
	}, nil
}

func (s *IdentityService) CreateUser(ctx context.Context, req *identityv1.CreateUserRequest) (*identityv1.CreateUserResponse, error) {
	user, err := s.uc.CreateUser(ctx, req.Username, req.DisplayName, req.Email, req.Password, req.Group, 0)
	if err != nil {
		if applogger.Log != nil {
			applogger.Log.Warn("CreateUser failed", zap.Error(err))
		}
		return &identityv1.CreateUserResponse{
			Success: false,
			Message: "user creation failed",
		}, nil
	}
	return &identityv1.CreateUserResponse{
		Success: true,
		Message: "ok",
		UserId:  user.ID,
	}, nil
}

func (s *IdentityService) UpdateUser(ctx context.Context, req *identityv1.UpdateUserRequest) (*identityv1.UpdateUserResponse, error) {
	err := s.uc.UpdateUser(ctx, req.UserId, req.DisplayName, req.Email, req.Group, req.Status)
	if err != nil {
		if applogger.Log != nil {
			applogger.Log.Warn("UpdateUser failed", zap.Error(err))
		}
		return &identityv1.UpdateUserResponse{
			Success: false,
			Message: "user update failed",
		}, nil
	}
	return &identityv1.UpdateUserResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *IdentityService) DeleteUser(ctx context.Context, req *identityv1.DeleteUserRequest) (*identityv1.DeleteUserResponse, error) {
	err := s.uc.DeleteUser(ctx, req.UserId)
	if err != nil {
		if applogger.Log != nil {
			applogger.Log.Warn("DeleteUser failed", zap.Error(err))
		}
		return &identityv1.DeleteUserResponse{
			Success: false,
			Message: "user deletion failed",
		}, nil
	}
	return &identityv1.DeleteUserResponse{
		Success: true,
		Message: "ok",
	}, nil
}

func (s *IdentityService) SetUserRole(ctx context.Context, req *identityv1.SetUserRoleRequest) (*identityv1.SetUserRoleResponse, error) {
	var operator *biz.User
	if req.OperatorUserId > 0 {
		op, err := s.uc.GetUser(ctx, req.OperatorUserId)
		if err != nil {
			if applogger.Log != nil {
				applogger.Log.Warn("SetUserRole operator lookup failed", zap.Error(err))
			}
			return &identityv1.SetUserRoleResponse{
				Success: false,
				Message: "operator not found",
			}, nil
		}
		operator = op
	}
	user, err := s.uc.SetRole(ctx, operator, req.UserId, req.Role)
	if err != nil {
		if applogger.Log != nil {
			applogger.Log.Warn("SetUserRole failed", zap.Error(err))
		}
		return &identityv1.SetUserRoleResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &identityv1.SetUserRoleResponse{
		Success: true,
		Message: "ok",
		Role:    user.Role,
	}, nil
}

func mapIdentityErrorToGRPC(err error) error {
	if err == nil {
		return nil
	}

	mappedErr := errors.MapIdentityError(err)
	if structuredErr, ok := mappedErr.(*errors.Error); ok {
		var code codes.Code
		var message string
		switch structuredErr.Reason {
		case errors.ReasonUnauthorized,
			errors.ReasonTokenDisabled,
			errors.ReasonTokenExpired,
			errors.ReasonTokenExhausted,
			errors.ReasonTokenNotFound,
			errors.ReasonUserNotFound:
			code = codes.NotFound
			message = "resource not found"
		case errors.ReasonUserDisabled,
			errors.ReasonModelForbidden:
			code = codes.PermissionDenied
			message = "permission denied"
		case errors.ReasonQuotaNotEnough:
			code = codes.ResourceExhausted
			message = "quota exhausted"
		default:
			code = codes.Internal
			message = "internal error"
		}
		if applogger.Log != nil {
			applogger.Log.Warn("identity error", zap.String("reason", string(structuredErr.Reason)), zap.Error(err))
		}
		return status.Error(code, message)
	}

	if applogger.Log != nil {
		applogger.Log.Warn("unexpected identity error", zap.Error(err))
	}
	return status.Error(codes.Internal, "internal error")
}
