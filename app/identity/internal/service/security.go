package service

import (
	"context"
	"encoding/base64"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/pkg/safecast"
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
	return &v.SecurityReply{PasswordAvailable: out.PasswordAvailable, Enabled: out.Enabled, Configured: out.Configured, RecoveryCodesRemaining: safecast.IntToInt32Saturating(out.RecoveryCodesRemaining), StepUpRequired: out.StepUpRequired, AdminMfaRequired: out.AdminMFARequired}, nil
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
	if p == nil || len(p.Password) > 72 || len(p.Code) > 32 || len(p.Challenge) > 4096 || len(p.Attestation) > 16384 || len(p.CredentialId) > 1024 || len(p.Username) > 128 {
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

// BeginPasskeyLogin is unauthenticated; it returns WebAuthn assertion options
// plus a one-shot ceremony handle for CompletePasskeyLogin.
func (s *IdentityService) BeginPasskeyLogin(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	if err := validateSecurityInput(p); err != nil {
		return nil, err
	}
	options, ceremony, err := s.uc.BeginPasskeyLogin(ctx, p.Username)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{Options: options, Ceremony: ceremony}, nil
}

// CompletePasskeyLogin mints a session on a valid assertion. Accounts with TOTP
// configured still owe the second factor, surfaced as mfa_required + challenge.
func (s *IdentityService) CompletePasskeyLogin(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
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
	userID, token, mfaChallenge, err := s.uc.CompletePasskeyLogin(ctx, p.Challenge, p.Attestation)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	if mfaChallenge != "" {
		return &v.SecurityReply{MfaRequired: true, MfaChallenge: mfaChallenge, UserId: userID}, nil
	}
	return &v.SecurityReply{Token: token, UserId: userID}, nil
}

// BeginPasskeyEnrollment starts registering an authenticator for the signed-in
// account. It demands a fresh step-up proof like other sensitive changes.
func (s *IdentityService) BeginPasskeyEnrollment(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateSecurityInput(p); err != nil {
		return nil, err
	}
	options, ceremony, err := s.uc.BeginPasskeyEnrollment(ctx, raw, p.Username)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{Options: options, Ceremony: ceremony}, nil
}

func (s *IdentityService) CompletePasskeyEnrollment(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateSecurityInput(p); err != nil {
		return nil, err
	}
	if err = s.uc.CompletePasskeyEnrollment(ctx, raw, p.Challenge, p.Username, p.Attestation); err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{}, nil
}

func (s *IdentityService) ListPasskeys(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	creds, enabled, err := s.uc.ListPasskeys(ctx, raw)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	out := &v.SecurityReply{PasskeyEnabled: enabled, Passkeys: make([]*v.PasskeyCredential, 0, len(creds))}
	for _, c := range creds {
		out.Passkeys = append(out.Passkeys, &v.PasskeyCredential{
			CredentialId: base64.RawURLEncoding.EncodeToString(c.ID),
			Name:         c.Name,
			CreatedAt:    c.CreatedAt,
			LastUsedAt:   c.LastUsedAt,
		})
	}
	return out, nil
}

func (s *IdentityService) DeletePasskey(ctx context.Context, p *v.SecurityRequest) (*v.SecurityReply, error) {
	raw, err := securityCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateSecurityInput(p); err != nil {
		return nil, err
	}
	if err = s.uc.DeletePasskey(ctx, raw, p.CredentialId); err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.SecurityReply{}, nil
}
