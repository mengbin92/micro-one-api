package service

import (
	"context"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/identity/v1"
	"strings"
)

func securityCredential(ctx context.Context) (string, error) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		if ht, ok := tr.(*khttp.Transport); ok {
			ht.ReplyHeader().Set("Cache-Control", "no-store")
			raw := ht.Request().Header.Get("Authorization")
			if !strings.HasPrefix(raw, "Bearer ") {
				return "", status.Error(codes.Unauthenticated, "user session required")
			}
			return strings.TrimPrefix(raw, "Bearer "), nil
		}
	}
	if !isServiceAuthenticated(ctx) {
		return "", ErrUnauthenticatedService
	}
	raw, system := operatorCredential(ctx)
	if raw == "" || system {
		return "", status.Error(codes.Unauthenticated, "user session required")
	}
	return raw, nil
}

func (s *IdentityService) GetSecurityStatus(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetSecurityStatus(ctx, raw)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{PasswordAvailable: out.PasswordAvailable, Enabled: out.Enabled, Configured: out.Configured, RecoveryCodesRemaining: int32(out.RecoveryCodesRemaining), StepUpRequired: out.StepUpRequired, AdminMfaRequired: out.AdminMFARequired}, nil
}

func (s *IdentityService) VerifyStepUp(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateSecurityInput(p); err != nil {
		return nil, err
	}
	if err = s.uc.VerifyStepUp(ctx, raw, p.Password, p.Code); err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{}, nil
}

func validateSecurityInput(p *v.SecurityRequest) error {
	if p == nil || len(p.Password) > 72 || len(p.Code) > 32 || len(p.Challenge) > 4096 {
		return status.Error(codes.InvalidArgument, "invalid verification input")
	}
	return nil
}

func (s *IdentityService) manageMFA(ctx context.Context, p *v.SecurityRequest, action string) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateSecurityInput(p); err != nil {
		return nil, err
	}
	secret, uri, codes, err := s.uc.ManageMFA(ctx, raw, action, p.Code)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{Secret: secret, OtpauthUri: uri, RecoveryCodes: codes}, nil
}
func (s *IdentityService) SetupMFA(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	return s.manageMFA(ctx, p, "setup")
}
func (s *IdentityService) ConfirmMFA(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	return s.manageMFA(ctx, p, "confirm")
}
func (s *IdentityService) DisableMFA(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	return s.manageMFA(ctx, p, "disable")
}
func (s *IdentityService) RotateRecoveryCodes(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	return s.manageMFA(ctx, p, "recovery")
}

func (s *IdentityService) CompleteMFALogin(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	if err := validateSecurityInput(p); err != nil {
		return nil, err
	}
	if tr, ok := transport.FromServerContext(ctx); ok {
		if ht, ok := tr.(*khttp.Transport); ok {
			ht.ReplyHeader().Set("Cache-Control", "no-store")
		}
	} else if !isServiceAuthenticated(ctx) {
		return nil, ErrUnauthenticatedService
	}
	user, token, err := s.uc.CompleteMFALogin(ctx, p.Challenge, p.Code)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{Token: token, UserId: user.ID}, nil
}
