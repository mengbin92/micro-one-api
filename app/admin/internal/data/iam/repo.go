package iam

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/platform/iamdto"
)

type repo struct{ client v.IAMServiceClient }

func NewRepo(client v.IAMServiceClient) biz.IAMRepo { return &repo{client: client} }
func (r *repo) Execute(ctx context.Context, credential, method string, req m.Request) (m.Response, error) {
	if r.client == nil {
		return m.Response{}, status.Error(codes.Unavailable, "IAM unavailable")
	}
	// Overwrite, never append a spoofed operator from inbound metadata.
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-operator-authorization", "Bearer "+credential)
	ctx = metadata.NewOutgoingContext(ctx, md)
	calls := map[string]func(context.Context, *v.IAMRequest, ...grpc.CallOption) (*v.IAMReply, error){
		"ListUserSessions":               r.client.ListUserSessions,
		"ListResources":                  r.client.ListResources,
		"ListPermissions":                r.client.ListPermissions,
		"GetPermission":                  r.client.GetPermission,
		"CreatePermission":               r.client.CreatePermission,
		"UpdatePermission":               r.client.UpdatePermission,
		"SetPermissionStatus":            r.client.SetPermissionStatus,
		"ArchivePermission":              r.client.ArchivePermission,
		"GetPermissionReferences":        r.client.GetPermissionReferences,
		"ListRoles":                      r.client.ListRoles,
		"GetRole":                        r.client.GetRole,
		"CreateRole":                     r.client.CreateRole,
		"UpdateRole":                     r.client.UpdateRole,
		"CopyRole":                       r.client.CopyRole,
		"SetRoleStatus":                  r.client.SetRoleStatus,
		"ArchiveRole":                    r.client.ArchiveRole,
		"GetRoleReferences":              r.client.GetRoleReferences,
		"ListRoleMembers":                r.client.ListRoleMembers,
		"GetRolePermissions":             r.client.GetRolePermissions,
		"UpdateRolePermissions":          r.client.UpdateRolePermissions,
		"UpdateRoleInheritance":          r.client.UpdateRoleInheritance,
		"PreviewRoleChange":              r.client.PreviewRoleChange,
		"GetUserRoles":                   r.client.GetUserRoles,
		"AssignUserRole":                 r.client.AssignUserRole,
		"RevokeUserRole":                 r.client.RevokeUserRole,
		"BatchAssignUserRoles":           r.client.BatchAssignUserRoles,
		"PreviewUserRoleChange":          r.client.PreviewUserRoleChange,
		"GetSessionAuthorization":        r.client.GetSessionAuthorization,
		"GetSessionRoles":                r.client.GetSessionRoles,
		"ActivateSessionRoles":           r.client.ActivateSessionRoles,
		"RevokeOwnSession":               r.client.RevokeOwnSession,
		"RevokeUserSessions":             r.client.RevokeUserSessions,
		"ListDelegations":                r.client.ListDelegations,
		"CreateDelegation":               r.client.CreateDelegation,
		"UpdateDelegation":               r.client.UpdateDelegation,
		"RevokeDelegation":               r.client.RevokeDelegation,
		"ListRoleConstraints":            r.client.ListRoleConstraints,
		"CreateRoleConstraint":           r.client.CreateRoleConstraint,
		"UpdateRoleConstraint":           r.client.UpdateRoleConstraint,
		"DeleteRoleConstraint":           r.client.DeleteRoleConstraint,
		"ListMenuItems":                  r.client.ListMenuItems,
		"CreateMenuItem":                 r.client.CreateMenuItem,
		"UpdateMenuItem":                 r.client.UpdateMenuItem,
		"ArchiveMenuItem":                r.client.ArchiveMenuItem,
		"CheckAuthorization":             r.client.CheckAuthorization,
		"GetUserEffectivePermissions":    r.client.GetUserEffectivePermissions,
		"ExplainAuthorization":           r.client.ExplainAuthorization,
		"SimulateAuthorizationChange":    r.client.SimulateAuthorizationChange,
		"ListAuthorizationAuditEvents":   r.client.ListAuthorizationAuditEvents,
		"GetAuthorizationAuditEvent":     r.client.GetAuthorizationAuditEvent,
		"ExportAuthorizationAuditEvents": r.client.ExportAuthorizationAuditEvents,
	}
	call, ok := calls[method]
	if !ok {
		return m.Response{}, status.Error(codes.InvalidArgument, "unknown IAM method")
	}
	out, err := call(ctx, iamdto.IAMRequestTo(req))
	if err != nil {
		return m.Response{}, err
	}
	return iamdto.IAMReplyFrom(out), nil
}
