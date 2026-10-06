package service

import (
	"context"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/platform/iamdto"
	"micro-one-api/platform/security/serviceidentity"
)

type IAMService struct {
	v.UnimplementedIAMServiceServer
	uc       *biz.IAMGovernanceUsecase
	identity *biz.IdentityUsecase
}

func NewIAMService(uc *biz.IAMGovernanceUsecase, identity *biz.IdentityUsecase) *IAMService {
	return &IAMService{uc: uc, identity: identity}
}
func (s *IAMService) execute(ctx context.Context, method string, p *v.IAMRequest) (*v.IAMReply, error) {
	credential, system := operatorCredential(ctx)
	self := method == "GetSessionAuthorization" || method == "GetSessionRoles" || method == "ActivateSessionRoles" || method == "RevokeOwnSession"
	httpSelf := false
	if tr, ok := transport.FromServerContext(ctx); ok && self {
		if ht, ok := tr.(*khttp.Transport); ok {
			httpSelf = true
			credential = ""
			system = false
			header := ht.Request().Header.Get("Authorization")
			if strings.HasPrefix(header, "Bearer ") {
				credential = strings.TrimPrefix(header, "Bearer ")
			}
		}
	}
	if !httpSelf && !isServiceAuthenticated(ctx) {
		return nil, status.Error(codes.Unauthenticated, "service authentication required")
	}
	if !httpSelf {
		mode, err := s.identity.AuthorizationMode(ctx)
		if err != nil {
			return nil, mapIdentityErrorToGRPC(err)
		}
		fullMethod := "/api.identity.v1.IAMService/" + method
		if mode == "iam" && (serviceidentity.RPCMethod(ctx) != fullMethod || !serviceidentity.FromContext(ctx).CanCall(fullMethod)) {
			return nil, status.Error(codes.PermissionDenied, "dedicated service caller capability required")
		}
	}
	if credential == "" || system {
		return nil, status.Error(codes.Unauthenticated, "user session required")
	}
	if err := iamdto.ValidateRequest(p); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if self {
		if p.Id != 0 || p.UserId != 0 || p.SourceId != 0 {
			return nil, status.Error(codes.InvalidArgument, "self session cannot select another identity")
		}
		req := iamdto.IAMRequestFrom(p)
		var snapshot *biz.IAMAuthorizationSnapshot
		var err error
		switch method {
		case "GetSessionAuthorization", "GetSessionRoles":
			snapshot, err = s.identity.GetSessionAuthorization(ctx, credential, req.Context)
		case "ActivateSessionRoles":
			snapshot, err = s.identity.ActivateSessionRoles(ctx, credential, req.Context, req.RoleIDs, req.ExpectedRevision, req.Reason)
		case "RevokeOwnSession":
			err = s.identity.RevokeOwnSession(ctx, credential, req.Context, true, req.ExpectedRevision, req.Reason)
		}
		if err != nil {
			return nil, mapIdentityErrorToGRPC(err)
		}
		if snapshot == nil {
			return &v.IAMReply{}, nil
		}
		out := m.Response{AuthorizationMode: snapshot.Policy.Mode, LegacyAdmin: snapshot.IsLegacyAdmin(), PermittedOperations: snapshot.PermittedOperations, Roles: snapshot.Roles, Menus: snapshot.Menus, Versions: snapshot.Versions, Session: &snapshot.Session, Sources: snapshot.Sources, AuthorizedRoleIDs: snapshot.AuthorizedRoleIDs, ActiveRoleIDs: snapshot.ActiveRoleIDs, ValidUntil: &snapshot.ValidUntil, BasePolicyRevision: snapshot.Policy.PolicyRevision}
		return iamdto.IAMReplyTo(out), nil
	}
	out, err := s.uc.Execute(ctx, credential, method, iamdto.IAMRequestFrom(p))
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return iamdto.IAMReplyTo(out), nil
}
func (s *IAMService) RescueRootCredential(ctx context.Context, p *v.IAMRescueRequest) (*v.IAMReply, error) {
	if !isRescueAuthenticated(ctx) || p == nil {
		return nil, status.Error(codes.Unauthenticated, "dedicated rescue service required")
	}
	credential, _ := operatorCredential(ctx)
	if err := s.identity.RescueRootCredential(ctx, credential, p.UserId, p.Password, p.Reason, p.ExpectedRevision, p.ExpectedPolicyRevision); err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &v.IAMReply{}, nil
}

type rescueCallerKey struct{}

func RescueAuthenticatedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, rescueCallerKey{}, true)
}
func isRescueAuthenticated(ctx context.Context) bool {
	valid, _ := ctx.Value(rescueCallerKey{}).(bool)
	return valid
}

func (s *IAMService) ListResources(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListResources", p)
}

func (s *IAMService) ListPermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListPermissions", p)
}

func (s *IAMService) GetPermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetPermission", p)
}

func (s *IAMService) CreatePermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreatePermission", p)
}

func (s *IAMService) UpdatePermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdatePermission", p)
}

func (s *IAMService) SetPermissionStatus(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "SetPermissionStatus", p)
}

func (s *IAMService) ArchivePermission(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ArchivePermission", p)
}

func (s *IAMService) GetPermissionReferences(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetPermissionReferences", p)
}

func (s *IAMService) ListRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListRoles", p)
}

func (s *IAMService) GetRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetRole", p)
}

func (s *IAMService) CreateRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateRole", p)
}

func (s *IAMService) UpdateRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRole", p)
}

func (s *IAMService) CopyRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CopyRole", p)
}

func (s *IAMService) SetRoleStatus(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "SetRoleStatus", p)
}

func (s *IAMService) ArchiveRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ArchiveRole", p)
}

func (s *IAMService) GetRoleReferences(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetRoleReferences", p)
}

func (s *IAMService) ListRoleMembers(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListRoleMembers", p)
}

func (s *IAMService) GetRolePermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetRolePermissions", p)
}

func (s *IAMService) UpdateRolePermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRolePermissions", p)
}

func (s *IAMService) UpdateRoleInheritance(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRoleInheritance", p)
}

func (s *IAMService) PreviewRoleChange(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "PreviewRoleChange", p)
}

func (s *IAMService) GetUserRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetUserRoles", p)
}

func (s *IAMService) AssignUserRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "AssignUserRole", p)
}

func (s *IAMService) RevokeUserRole(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeUserRole", p)
}

func (s *IAMService) BatchAssignUserRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "BatchAssignUserRoles", p)
}

func (s *IAMService) PreviewUserRoleChange(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "PreviewUserRoleChange", p)
}

func (s *IAMService) GetSessionAuthorization(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetSessionAuthorization", p)
}

func (s *IAMService) GetSessionRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetSessionRoles", p)
}

func (s *IAMService) ActivateSessionRoles(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ActivateSessionRoles", p)
}

func (s *IAMService) RevokeOwnSession(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeOwnSession", p)
}

func (s *IAMService) RevokeUserSessions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeUserSessions", p)
}

func (s *IAMService) ListDelegations(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListDelegations", p)
}

func (s *IAMService) CreateDelegation(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateDelegation", p)
}

func (s *IAMService) UpdateDelegation(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateDelegation", p)
}

func (s *IAMService) RevokeDelegation(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "RevokeDelegation", p)
}

func (s *IAMService) ListRoleConstraints(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListRoleConstraints", p)
}

func (s *IAMService) CreateRoleConstraint(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateRoleConstraint", p)
}

func (s *IAMService) UpdateRoleConstraint(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateRoleConstraint", p)
}

func (s *IAMService) DeleteRoleConstraint(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "DeleteRoleConstraint", p)
}

func (s *IAMService) ListMenuItems(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListMenuItems", p)
}

func (s *IAMService) CreateMenuItem(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CreateMenuItem", p)
}

func (s *IAMService) UpdateMenuItem(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "UpdateMenuItem", p)
}

func (s *IAMService) ArchiveMenuItem(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ArchiveMenuItem", p)
}

func (s *IAMService) CheckAuthorization(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "CheckAuthorization", p)
}

func (s *IAMService) GetUserEffectivePermissions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetUserEffectivePermissions", p)
}

func (s *IAMService) ExplainAuthorization(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ExplainAuthorization", p)
}

func (s *IAMService) SimulateAuthorizationChange(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "SimulateAuthorizationChange", p)
}

func (s *IAMService) ListAuthorizationAuditEvents(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListAuthorizationAuditEvents", p)
}

func (s *IAMService) GetAuthorizationAuditEvent(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "GetAuthorizationAuditEvent", p)
}

func (s *IAMService) ExportAuthorizationAuditEvents(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ExportAuthorizationAuditEvents", p)
}

func (s *IAMService) ListUserSessions(ctx context.Context, p *v.IAMRequest) (*v.IAMReply, error) {
	return s.execute(ctx, "ListUserSessions", p)
}
