package biz

import (
	"context"
	"slices"
	"time"

	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

// projectSessionAuthorization runs in the same primary snapshot as the actor,
// session and grants. The summary permits displaying an action; data owners
// still check scope, delegation and business invariants at each real entry.
func (uc *IdentityUsecase) projectSessionAuthorization(ctx context.Context, tx IAMTx, out *IAMAuthorizationSnapshot, state IAMConstraintState, now time.Time) error {
	for _, role := range state.Roles {
		if slices.Contains(out.AuthorizedRoleIDs, role.ID) {
			role.Grants = nil
			role.Inherits = nil
			out.Roles = append(out.Roles, role)
		}
	}
	if out.Policy.Mode != "iam" || out.Session.ActivationState != "active" {
		return nil
	}
	reader, ok := uc.iam.(interface {
		Permissions(context.Context, IAMTx) ([]m.Permission, error)
		Menus(context.Context, IAMTx) ([]m.Menu, error)
	})
	if !ok {
		return nil // No display capabilities when the catalogue is unavailable.
	}
	permissions, err := reader.Permissions(ctx, tx)
	if err != nil {
		return err
	}
	root := slices.ContainsFunc(out.Roles, func(r m.Role) bool {
		return r.Builtin && r.Code == "root" && slices.Contains(out.ActiveRoleIDs, r.ID)
	})
	for _, permission := range permissions {
		op, registered := authorization.Lookup(permission.Code)
		if !registered || !IAMExecutionBound(op.Code) || permission.Status != "enabled" {
			continue
		}
		q, err := authorization.QueryFromSources(out.Actor, op, out.Sources, now, out.Versions)
		if err != nil {
			return ErrIAMInvalidRelation
		}
		present := root
		deny := authorization.Scope{}
		for _, scope := range q.Deny {
			deny.Clauses = append(deny.Clauses, scope.Clauses...)
		}
		for _, allow := range q.Allow {
			if len(allow.Clauses) > 0 && !authorization.ScopeContained(allow, deny, out.Actor.UserID, out.Actor.UserID) {
				present = true
			}
		}
		// Global operations have an exact decision. For object-scoped actions
		// presence says nothing about a particular object's permitted actions.
		if !root && len(op.Scopes) == 1 && op.Scopes[0] == authorization.All {
			present = q.Matches(authorization.ObjectFacts{Context: out.Context}, false)
		}
		if present {
			out.PermittedOperations = append(out.PermittedOperations, op.Code)
		}
	}
	slices.Sort(out.PermittedOperations)
	menus, err := reader.Menus(ctx, tx)
	if err != nil {
		return err
	}
	for _, menu := range menus {
		if !menu.Enabled || len(out.PermittedOperations) == 0 {
			continue
		}
		all := true
		for _, code := range menu.RequiredAll {
			all = all && slices.Contains(out.PermittedOperations, code)
		}
		any := len(menu.RequiredAny) == 0
		for _, code := range menu.RequiredAny {
			any = any || slices.Contains(out.PermittedOperations, code)
		}
		if all && any {
			out.Menus = append(out.Menus, menu)
		}
	}
	return nil
}
