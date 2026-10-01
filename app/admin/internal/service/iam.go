package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	a "micro-one-api/api/admin/v1"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/platform/iamdto"
)

type IAMAdminService struct {
	a.UnimplementedIAMAdminServiceServer
	uc *biz.IAMUsecase
}

func NewIAMAdminService(uc *biz.IAMUsecase) *IAMAdminService { return &IAMAdminService{uc: uc} }
func (s *IAMAdminService) execute(ctx context.Context, method string, p *v.IAMRequest) (*v.IAMReply, error) {
	credential := operatorCredential(ctx)
	if transport, ok := transport.FromServerContext(ctx); ok {
		if http, ok := transport.(*khttp.Transport); ok {
			header := http.Request().Header.Get("Authorization")
			if strings.HasPrefix(header, "Bearer ") {
				credential = strings.TrimPrefix(header, "Bearer ")
			}
		}
	}
	if credential == "" {
		return nil, status.Error(codes.Unauthenticated, "user session required")
	}
	if err := iamdto.ValidateRequest(p); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	out, err := s.uc.Execute(ctx, credential, method, iamdto.IAMRequestFrom(p))
	if err != nil {
		return nil, err
	}
	return iamdto.IAMReplyTo(out), nil
}

func (s *IAMAdminService) ListResources(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListResources", p)
}

func (s *IAMAdminService) ListPermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListPermissions", p)
}

func (s *IAMAdminService) GetPermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetPermission", p)
}

func (s *IAMAdminService) CreatePermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreatePermission", p)
}

func (s *IAMAdminService) UpdatePermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdatePermission", p)
}

func (s *IAMAdminService) SetPermissionStatus(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "SetPermissionStatus", p)
}

func (s *IAMAdminService) ArchivePermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ArchivePermission", p)
}

func (s *IAMAdminService) GetPermissionReferences(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetPermissionReferences", p)
}

func (s *IAMAdminService) ListRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListRoles", p)
}

func (s *IAMAdminService) GetRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetRole", p)
}

func (s *IAMAdminService) CreateRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateRole", p)
}

func (s *IAMAdminService) UpdateRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRole", p)
}

func (s *IAMAdminService) CopyRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CopyRole", p)
}

func (s *IAMAdminService) SetRoleStatus(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "SetRoleStatus", p)
}

func (s *IAMAdminService) ArchiveRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ArchiveRole", p)
}

func (s *IAMAdminService) GetRoleReferences(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetRoleReferences", p)
}

func (s *IAMAdminService) ListRoleMembers(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListRoleMembers", p)
}

func (s *IAMAdminService) GetRolePermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetRolePermissions", p)
}

func (s *IAMAdminService) UpdateRolePermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRolePermissions", p)
}

func (s *IAMAdminService) UpdateRoleInheritance(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRoleInheritance", p)
}

func (s *IAMAdminService) PreviewRoleChange(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "PreviewRoleChange", p)
}

func (s *IAMAdminService) GetUserRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetUserRoles", p)
}

func (s *IAMAdminService) AssignUserRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "AssignUserRole", p)
}

func (s *IAMAdminService) RevokeUserRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeUserRole", p)
}

func (s *IAMAdminService) BatchAssignUserRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "BatchAssignUserRoles", p)
}

func (s *IAMAdminService) PreviewUserRoleChange(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "PreviewUserRoleChange", p)
}

func (s *IAMAdminService) GetSessionAuthorization(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetSessionAuthorization", p)
}

func (s *IAMAdminService) GetSessionRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetSessionRoles", p)
}

func (s *IAMAdminService) ActivateSessionRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ActivateSessionRoles", p)
}

func (s *IAMAdminService) RevokeOwnSession(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeOwnSession", p)
}

func (s *IAMAdminService) RevokeUserSessions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeUserSessions", p)
}

func (s *IAMAdminService) ListDelegations(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListDelegations", p)
}

func (s *IAMAdminService) CreateDelegation(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateDelegation", p)
}

func (s *IAMAdminService) UpdateDelegation(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateDelegation", p)
}

func (s *IAMAdminService) RevokeDelegation(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeDelegation", p)
}

func (s *IAMAdminService) ListRoleConstraints(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListRoleConstraints", p)
}

func (s *IAMAdminService) CreateRoleConstraint(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateRoleConstraint", p)
}

func (s *IAMAdminService) UpdateRoleConstraint(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRoleConstraint", p)
}

func (s *IAMAdminService) DeleteRoleConstraint(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "DeleteRoleConstraint", p)
}

func (s *IAMAdminService) ListMenuItems(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListMenuItems", p)
}

func (s *IAMAdminService) CreateMenuItem(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateMenuItem", p)
}

func (s *IAMAdminService) UpdateMenuItem(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateMenuItem", p)
}

func (s *IAMAdminService) ArchiveMenuItem(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ArchiveMenuItem", p)
}

func (s *IAMAdminService) CheckAuthorization(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CheckAuthorization", p)
}

func (s *IAMAdminService) GetUserEffectivePermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetUserEffectivePermissions", p)
}

func (s *IAMAdminService) ExplainAuthorization(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ExplainAuthorization", p)
}

func (s *IAMAdminService) SimulateAuthorizationChange(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "SimulateAuthorizationChange", p)
}

func (s *IAMAdminService) ListAuthorizationAuditEvents(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListAuthorizationAuditEvents", p)
}

func (s *IAMAdminService) GetAuthorizationAuditEvent(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetAuthorizationAuditEvent", p)
}

func (s *IAMAdminService) ExportAuthorizationAuditEvents(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ExportAuthorizationAuditEvents", p)
}

func (s *IAMAdminService) ListUserSessions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListUserSessions", p)
}
