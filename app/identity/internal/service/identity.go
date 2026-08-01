package service

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/pkg/errors"
	applogger "micro-one-api/platform/logging"
)

// IdentityService is the transport layer entry for identity-service.
type IdentityService struct {
	identityv1.UnimplementedIdentityServiceServer
	uc *biz.IdentityUsecase
}

type operatorCredentialKey struct{}

// WithOperatorCredential carries the caller's end-user session credential
// across the identity transport boundary. It is populated only by the
// authenticated admin-api path and is never accepted from a public request
// field.
func WithOperatorCredential(ctx context.Context, credential string, system bool) context.Context {
	return context.WithValue(context.WithValue(ctx, operatorCredentialKey{}, credential), operatorSystemKey{}, system)
}

type operatorSystemKey struct{}

func operatorCredential(ctx context.Context) (string, bool) {
	credential, _ := ctx.Value(operatorCredentialKey{}).(string)
	system, _ := ctx.Value(operatorSystemKey{}).(bool)
	return credential, system
}

func NewIdentityService(uc *biz.IdentityUsecase) *IdentityService {
	return &IdentityService{uc: uc}
}

func (s *IdentityService) ValidateTokenModel(ctx context.Context, token, clientIP string) (*biz.Token, error) {
	return s.uc.ValidateToken(ctx, token, clientIP)
}

func (s *IdentityService) GetAuthSnapshotModel(ctx context.Context, token, clientIP string) (*biz.AuthSnapshot, error) {
	return s.uc.GetAuthSnapshot(ctx, token, clientIP)
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
	snapshot, err := s.uc.GetAuthSnapshot(ctx, req.Token, req.ClientIp)
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
	user, token, err := s.uc.Login(ctx, req.Username, req.Password, "")
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
		applogger.Log.Warn("CreateUser failed", zap.Error(err))
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
		applogger.Log.Warn("UpdateUser failed", zap.Error(err))
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
		applogger.Log.Warn("DeleteUser failed", zap.Error(err))
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

// serviceCallerKey marks a context as originating from a caller that
// presented a valid SERVICE_TOKEN (validated by the gRPC interceptor in
// internal/server/grpc.go). Its presence means the request is a trusted
// in-cluster service call, not an unauthenticated network request. Sensitive
// handlers additionally validate their end-user/operator credential.
type serviceCallerKey struct{}

// ServiceAuthenticatedContext stamps the service-caller marker into the
// context. It is called by the gRPC service-token interceptor after
// successful token validation so downstream handlers know the caller is a
// trusted service.
func ServiceAuthenticatedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, serviceCallerKey{}, true)
}

// isServiceAuthenticated reports whether the context carries the
// service-caller marker (i.e. the request passed the service-token
// interceptor). When false the caller did not present a valid SERVICE_TOKEN.
func isServiceAuthenticated(ctx context.Context) bool {
	v, _ := ctx.Value(serviceCallerKey{}).(bool)
	return v
}

// ErrUnauthenticatedService is returned by handlers that require a valid
// service token or operator credential is absent or invalid.
var ErrUnauthenticatedService = status.Error(codes.Unauthenticated, "service token required")

func (s *IdentityService) SetUserRole(ctx context.Context, req *identityv1.SetUserRoleRequest) (*identityv1.SetUserRoleResponse, error) {
	// Require both service authentication and an independently validated
	// operator credential. The request field is only an identifier and never
	// establishes the caller's identity.
	if !isServiceAuthenticated(ctx) {
		return nil, ErrUnauthenticatedService
	}

	credential, system := operatorCredential(ctx)
	if credential == "" {
		return nil, ErrUnauthenticatedService
	}
	var operator *biz.User
	if system {
		if req.OperatorUserId != 0 {
			return nil, ErrUnauthenticatedService
		}
	} else if req.OperatorUserId > 0 {
		claimsUser, err := s.uc.ValidateSessionToken(ctx, credential)
		if err != nil || claimsUser.ID != req.OperatorUserId {
			return nil, ErrUnauthenticatedService
		}
		op, err := s.uc.GetUser(ctx, req.OperatorUserId)
		if err != nil {
			applogger.Log.Warn("SetUserRole operator lookup failed", zap.Error(err))
			return &identityv1.SetUserRoleResponse{
				Success: false,
				Message: "operator not found",
			}, nil
		}
		operator = op
	} else {
		return nil, ErrUnauthenticatedService
	}
	// operator == nil is only reachable here when the caller is
	// service-authenticated, the ADMIN_TOKEN was independently validated, and
	// OperatorUserId == 0. This represents a legitimate system-level
	// call; SetRole applies its root-protection checks but skips the
	// operator-vs-target rank comparison.
	user, err := s.uc.SetRole(ctx, operator, req.UserId, req.Role)
	if err != nil {
		applogger.Log.Warn("SetUserRole failed", zap.Error(err))
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

// ConsumeTokenQuota deducts the given amount from a token's remaining quota
// and marks it exhausted when it reaches zero. Called by relay-gateway after
// billing settles so per-key quota limits are enforced (review High #5).
func (s *IdentityService) ConsumeTokenQuota(ctx context.Context, req *identityv1.ConsumeTokenQuotaRequest) (*identityv1.ConsumeTokenQuotaReply, error) {
	if !isServiceAuthenticated(ctx) {
		return nil, ErrUnauthenticatedService
	}
	if req.TokenId <= 0 || req.Amount <= 0 {
		return &identityv1.ConsumeTokenQuotaReply{
			Success: false,
			Message: "invalid token_id or amount",
		}, nil
	}
	remaining, err := s.uc.ConsumeTokenQuota(ctx, req.UserId, req.TokenId, req.Amount)
	if err != nil {
		applogger.Log.Warn("ConsumeTokenQuota failed", zap.Error(err))
		return &identityv1.ConsumeTokenQuotaReply{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return &identityv1.ConsumeTokenQuotaReply{
		Success:   true,
		Remaining: remaining,
		Message:   "ok",
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
		applogger.Log.Warn("identity error", zap.String("reason", string(structuredErr.Reason)), zap.Error(err))
		return status.Error(code, message)
	}

	applogger.Log.Warn("unexpected identity error", zap.Error(err))
	return status.Error(codes.Internal, "internal error")
}
