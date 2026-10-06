package biz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/pkg/jsonx"
)

var IAMMethods = map[string]string{
	"ListUserSessions": "iam.authorization.user.read",
	"ListResources":    "iam.permission.list", "ListPermissions": "iam.permission.list", "GetPermission": "iam.permission.read", "CreatePermission": "iam.permission.create", "UpdatePermission": "iam.permission.metadata.update", "SetPermissionStatus": "iam.permission.enable", "ArchivePermission": "iam.permission.archive", "GetPermissionReferences": "iam.permission.references.read",
	"ListRoles": "iam.role.list", "GetRole": "iam.role.read", "CreateRole": "iam.role.create", "UpdateRole": "iam.role.update", "CopyRole": "iam.role.copy", "SetRoleStatus": "iam.role.enable", "ArchiveRole": "iam.role.archive", "GetRoleReferences": "iam.role.read", "ListRoleMembers": "iam.role.members.read", "GetRolePermissions": "iam.role.permissions.read", "UpdateRolePermissions": "iam.role.permissions.update", "UpdateRoleInheritance": "iam.role.hierarchy.update",
	"GetUserRoles": "identity.user_role.read", "AssignUserRole": "identity.user_role.assign", "RevokeUserRole": "identity.user_role.revoke", "BatchAssignUserRoles": "identity.user_role.batch_assign",
	"ListDelegations": "iam.delegation.read", "CreateDelegation": "iam.delegation.create", "UpdateDelegation": "iam.delegation.update", "RevokeDelegation": "iam.delegation.revoke",
	"ListRoleConstraints": "iam.constraint.read", "CreateRoleConstraint": "iam.constraint.create", "UpdateRoleConstraint": "iam.constraint.update", "DeleteRoleConstraint": "iam.constraint.delete",
	"ListMenuItems": "iam.menu.list", "CreateMenuItem": "iam.menu.create", "UpdateMenuItem": "iam.menu.update", "ArchiveMenuItem": "iam.menu.archive",
	"GetUserEffectivePermissions": "iam.authorization.user.read", "ExplainAuthorization": "iam.authorization.explain", "CheckAuthorization": "iam.authorization.explain", "ListAuthorizationAuditEvents": "iam.audit.read", "GetAuthorizationAuditEvent": "iam.audit.read", "ExportAuthorizationAuditEvents": "iam.audit.export", "RevokeUserSessions": "identity.user.sessions.revoke",
}

func iamWriteMethod(method string) bool {
	return strings.HasPrefix(method, "Create") || strings.HasPrefix(method, "Update") || strings.HasPrefix(method, "Set") || strings.HasPrefix(method, "Archive") || strings.HasPrefix(method, "Assign") || strings.HasPrefix(method, "Revoke") || strings.HasPrefix(method, "Batch") || method == "DeleteRoleConstraint" || method == "CopyRole"
}
func (uc *IAMGovernanceUsecase) Execute(ctx context.Context, raw, method string, req m.Request) (m.Response, error) {
	if uc == nil || uc.repo == nil || uc.runner == nil || uc.identity == nil {
		return m.Response{}, ErrIAMDependencyUnavailable
	}
	if req.Context.RequirePlatform() != nil {
		return m.Response{}, ErrIAMContextInvalid
	}
	// Authenticate only from the verified credential. Actor IDs never occur in
	// the public request; lazy JTI initialization precedes the locked operation.
	snapshot, err := uc.identity.GetSessionAuthorization(ctx, raw, req.Context)
	if err != nil {
		return m.Response{}, err
	}
	actor := snapshot.Actor
	preview := method == "PreviewRoleChange" || method == "PreviewUserRoleChange" || method == "SimulateAuthorizationChange"
	targetMethod := method
	if preview {
		targetMethod = req.Operation
		if !iamWriteMethod(targetMethod) || targetMethod == "RevokeUserSessions" {
			return m.Response{}, ErrIAMInvalidRelation
		}
		if method == "PreviewRoleChange" && !slices.Contains([]string{"CreateRole", "CopyRole", "UpdateRole", "SetRoleStatus", "ArchiveRole", "UpdateRolePermissions", "UpdateRoleInheritance"}, targetMethod) {
			return m.Response{}, ErrIAMInvalidRelation
		}
		if method == "PreviewUserRoleChange" && !slices.Contains([]string{"AssignUserRole", "RevokeUserRole", "BatchAssignUserRoles"}, targetMethod) {
			return m.Response{}, ErrIAMInvalidRelation
		}
	}
	op, ok := IAMMethods[targetMethod]
	if !ok {
		return m.Response{}, ErrIAMInvalidRelation
	}
	if targetMethod == "SetRoleStatus" && req.Role != nil && req.Role.Status == "disabled" {
		op = "iam.role.disable"
	}
	if targetMethod == "SetPermissionStatus" && req.Permission != nil && req.Permission.Status == "disabled" {
		op = "iam.permission.disable"
	}
	write := iamWriteMethod(targetMethod)
	if write && (req.ExpectedPolicyRevision == 0 || strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.RequestID) == "") {
		return m.Response{}, ErrIAMInvalidRelation
	}
	if req.BasePolicyRevision != 0 && (req.BasePolicyRevision != req.ExpectedPolicyRevision || req.ContentDigest == "" || req.ContentDigest != IAMContentDigest(req)) {
		return m.Response{}, ErrIAMRevisionConflict
	}
	if req.EventID == "" {
		req.EventID = uc.identity.generateToken()
	}
	var out m.Response
	run := uc.runner.ReadIAMSnapshot
	if write && !preview {
		run = uc.runner.RunIAMWrite
	}
	var attempted authorization.PolicyState
	err = run(ctx, func(ctx context.Context, tx IAMTx) error {
		out = m.Response{}
		v, e := uc.view(ctx, tx, actor, req.Context)
		if e != nil {
			return e
		}
		attempted = v.policy
		if write && req.ExpectedPolicyRevision != v.policy.PolicyRevision {
			return ErrIAMRevisionConflict
		}
		if preview {
			if e = v.permit("iam.authorization.simulate", req.ID, req.UserID); e != nil {
				return e
			}
		}
		if !write {
			out, e = uc.read(ctx, tx, v, targetMethod, req, op)
			return e
		}
		proposed, result, e := uc.propose(ctx, tx, v, targetMethod, req, op)
		if e != nil {
			return e
		}
		conflicts, e := IAMCheckConstraints(v.now, proposed)
		if e != nil {
			return e
		}
		iamNormalizeProposedConflicts(v.state, conflicts)
		result.Impacts, e = v.permissionImpacts(v.state, proposed, result.AffectedUserIDs, true)
		if e != nil {
			return e
		}
		result.Conflicts = conflicts
		result.BasePolicyRevision = v.policy.PolicyRevision
		result.ContentDigest = IAMContentDigest(req)
		if preview {
			out = result
			return nil
		}
		if len(conflicts) > 0 {
			return &IAMConstraintViolation{Conflicts: conflicts}
		}
		out, e = uc.persist(ctx, tx, v, targetMethod, req, result)
		if e != nil {
			return e
		}
		// Re-read persisted IDs so successful audit sources refer to actual rows,
		// while a preview retains zero IDs for assignments not yet committed.
		committed, e := uc.repo.ConstraintState(ctx, tx, req.Context)
		if e != nil {
			return e
		}
		out.Impacts, e = v.permissionImpacts(v.state, committed, result.AffectedUserIDs, false)
		if e != nil {
			return e
		}
		latest, e := uc.repo.Policy(ctx, tx)
		if e != nil {
			return e
		}
		if e = uc.repo.AdvancePolicy(ctx, tx, latest.PolicyRevision, strings.Contains(targetMethod, "Permission") && !strings.Contains(targetMethod, "Role")); e != nil {
			return e
		}
		latest, e = uc.repo.Policy(ctx, tx)
		if e != nil {
			return e
		}
		out.BasePolicyRevision = latest.PolicyRevision
		before, _ := jsonx.Marshal(iamAuditBefore(v, targetMethod, req))
		after, _ := jsonx.Marshal(out)
		event := IAMAuditEvent{EventID: req.EventID, Actor: actor, Context: req.Context, TargetContext: req.Context, Action: op, Target: iamAuditTarget(targetMethod, req, out), Before: string(before), After: string(after), Diff: string(after), Result: "success", Versions: authorization.Versions{Policy: latest.PolicyRevision, Catalog: latest.CatalogRevision}, RequestID: req.RequestID, Reason: req.Reason, OccurredAt: v.now}
		return uc.repo.AppendAudit(ctx, tx, event)
	})
	if err != nil {
		if write && !preview {
			e := IAMAuditEvent{EventID: req.EventID, Actor: actor, Context: req.Context, TargetContext: req.Context, Action: op, Target: iamAuditTarget(targetMethod, req, m.Response{}), Before: "{}", After: "{}", Diff: "{}", Result: "failure", Versions: authorization.Versions{Policy: attempted.PolicyRevision, Catalog: attempted.CatalogRevision}, RequestID: req.RequestID, Reason: req.Reason, OccurredAt: uc.now().UTC()}
			if auditErr := uc.repo.AppendFailureAudit(ctx, e); auditErr != nil {
				err = errors.Join(err, auditErr)
			}
		}
		return m.Response{}, err
	}
	return out, nil
}
func iamAuditBefore(v iamManagementView, method string, req m.Request) any {
	switch method {
	case "AssignUserRole", "RevokeUserRole", "BatchAssignUserRoles":
		before := []IAMAssignment{}
		for _, a := range v.state.Assignments {
			if method != "BatchAssignUserRoles" && a.ID == req.ID && a.UserID == req.UserID {
				before = append(before, a)
			} else if method == "BatchAssignUserRoles" && slices.ContainsFunc(req.Assignments, func(input IAMAssignment) bool { return input.ID > 0 && input.ID == a.ID }) {
				before = append(before, a)
			}
		}
		return before
	case "UpdateRole", "SetRoleStatus", "ArchiveRole", "UpdateRolePermissions", "UpdateRoleInheritance", "CopyRole":
		id := req.ID
		if method == "CopyRole" {
			id = req.SourceID
		}
		for _, r := range v.state.Roles {
			if r.ID == id {
				return r
			}
		}
	case "UpdateDelegation", "RevokeDelegation":
		for _, d := range v.delegations {
			if d.ID == req.ID {
				return d
			}
		}
	case "UpdatePermission", "SetPermissionStatus", "ArchivePermission":
		for _, p := range v.permissions {
			if p.ID == req.ID {
				return p
			}
		}
	case "UpdateRoleConstraint", "DeleteRoleConstraint":
		for _, c := range v.state.Constraints {
			if c.ID == req.ID {
				return c
			}
		}
	case "UpdateMenuItem", "ArchiveMenuItem":
		for _, menu := range v.menus {
			if menu.ID == req.ID {
				return menu
			}
		}
	}
	return map[string]any{"policy_revision": v.policy.PolicyRevision}
}
func iamAuditTarget(method string, req m.Request, out m.Response) string {
	if method == "BatchAssignUserRoles" {
		return method + ":" + iamID(req.ID)
	}
	if len(out.Assignments) == 1 {
		return "role:" + iamID(out.Assignments[0].RoleID)
	}
	if len(out.Roles) == 1 {
		return "role:" + iamID(out.Roles[0].ID)
	}
	switch method {
	case "UpdateRole", "SetRoleStatus", "ArchiveRole", "UpdateRolePermissions", "UpdateRoleInheritance":
		return "role:" + iamID(req.ID)
	}
	return method + ":" + iamID(req.ID)
}
func (uc *IAMGovernanceUsecase) propose(ctx context.Context, tx IAMTx, v iamManagementView, method string, req m.Request, op string) (IAMConstraintState, m.Response, error) {
	state := v.state
	state.Roles = slices.Clone(state.Roles)
	state.Assignments = slices.Clone(state.Assignments)
	state.Constraints = slices.Clone(state.Constraints)
	out := m.Response{}
	fail := func(e error) (IAMConstraintState, m.Response, error) { return state, out, e }
	if method == "SetRoleStatus" && req.Role != nil && req.Role.Status == "disabled" {
		op = "iam.role.disable"
	}
	if method == "SetPermissionStatus" && req.Permission != nil && req.Permission.Status == "disabled" {
		op = "iam.permission.disable"
	}
	// DELETE selects the assignment by its authoritative ID and user path;
	// no client-supplied role or boundary is needed to revoke it.
	if method == "RevokeUserRole" && req.Assignment == nil {
		i := slices.IndexFunc(v.state.Assignments, func(a IAMAssignment) bool { return a.ID == req.ID && a.UserID == req.UserID })
		if i < 0 {
			return fail(ErrIAMNotFound)
		}
		a := v.state.Assignments[i]
		req.Assignment = &a
	}
	gateID := req.ID
	if (method == "AssignUserRole" || method == "RevokeUserRole") && req.Assignment != nil {
		gateID = req.Assignment.RoleID
	}
	if method == "RevokeUserSessions" {
		gateID = req.UserID
	}
	if method == "CopyRole" {
		gateID = req.SourceID
	}
	if method != "BatchAssignUserRoles" {
		if err := v.permit(op, gateID, req.UserID); err != nil {
			return fail(err)
		}
	}
	switch method {
	case "CreateRole", "CopyRole":
		if req.Role == nil || req.Role.ID != 0 || req.ExpectedRevision != 0 || req.Role.Builtin || req.Role.CreationDelegationID != 0 {
			return fail(ErrIAMInvalidRelation)
		}
		r := *req.Role
		r.Context = req.Context
		r.Status = "draft"
		r.Revision = 1
		r.Inherits = slices.Clone(r.Inherits)
		r.Grants = slices.Clone(r.Grants)
		if method == "CopyRole" {
			i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == req.SourceID })
			if i < 0 {
				return fail(ErrIAMNotFound)
			}
			source := state.Roles[i]
			if err := v.permit("iam.role.read", source.ID, 0); err != nil {
				return fail(err)
			}
			if !v.root {
				if err := v.manageRole(state, source.ID, "iam.role.copy", 0, nil, iamAllScope); err != nil {
					return fail(err)
				}
			}
			r.Grants = slices.Clone(source.Grants)
			r.Inherits = slices.Clone(source.Inherits)
		}
		if strings.TrimSpace(r.Code) == "" || len(r.Code) > 80 || strings.TrimSpace(r.Name) == "" || slices.Contains([]string{"root", "member", "guest", "platform_admin"}, r.Code) || slices.ContainsFunc(state.Roles, func(old IAMRole) bool { return old.Code == r.Code }) {
			return fail(ErrIAMInvalidRelation)
		}
		for _, old := range state.Roles {
			if old.ID >= r.ID {
				r.ID = old.ID + 1
			}
		}
		state.Roles = append(state.Roles, r)
		if err := iamValidateRole(r, state); err != nil {
			return fail(err)
		}
		if !v.root {
			matched := false
			for _, d := range v.delegations {
				if d.TargetKind == "role_creation" && d.Validity.Contains(v.now) && slices.Contains(v.active, d.ManagerRoleID) && slices.Contains(d.Actions, op) && iamCeilingCovers(d, state, r.ID, 0, v.actor.UserID, iamAllScope) {
					r.CreationDelegationID = d.ID
					matched = true
					break
				}
			}
			if !matched {
				return fail(ErrIAMProtected)
			}
		}
		state.Roles[len(state.Roles)-1] = r
		r.ID = 0
		out.Roles = []IAMRole{r}
	case "UpdateRole", "SetRoleStatus", "ArchiveRole", "UpdateRolePermissions", "UpdateRoleInheritance":
		i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == req.ID })
		if i < 0 {
			return fail(ErrIAMNotFound)
		}
		r := state.Roles[i]
		if req.ExpectedRevision == 0 || r.Revision != req.ExpectedRevision {
			return fail(ErrIAMRevisionConflict)
		}
		if r.Builtin || r.Status == "archived" {
			return fail(ErrIAMProtected)
		}
		switch method {
		case "UpdateRole":
			if req.Role == nil || len(req.UpdateMask) == 0 {
				return fail(ErrIAMInvalidRelation)
			}
			for _, field := range req.UpdateMask {
				switch field {
				case "name":
					r.Name = req.Role.Name
				case "description":
					r.Description = req.Role.Description
				case "max_members":
					r.MaxMembers = req.Role.MaxMembers
				default:
					return fail(ErrIAMInvalidRelation)
				}
			}
		case "SetRoleStatus":
			if req.Role == nil || !slices.Contains([]string{"enabled", "disabled"}, req.Role.Status) {
				return fail(ErrIAMInvalidRelation)
			}
			r.Status = req.Role.Status
		case "ArchiveRole":
			refs := iamRoleReferences(v, req.ID)
			if len(refs) > 0 {
				return fail(ErrIAMInvalidRelation)
			}
			r.Status = "archived"
		case "UpdateRolePermissions":
			r.Grants = slices.Clone(req.Grants)
		case "UpdateRoleInheritance":
			r.Inherits = slices.Clone(req.RoleIDs)
		}
		state.Roles[i] = r
		if err := iamValidateRole(r, state); err != nil {
			return fail(err)
		}
		affected, err := v.roleChange(v.state, state, r.ID, op)
		if err != nil {
			return fail(err)
		}
		out.AffectedUserIDs = affected
		out.Roles = []IAMRole{r}
	case "AssignUserRole", "RevokeUserRole", "BatchAssignUserRoles":
		inputs := req.Assignments
		if method != "BatchAssignUserRoles" {
			if req.Assignment == nil {
				return fail(ErrIAMInvalidRelation)
			}
			inputs = []IAMAssignment{*req.Assignment}
		}
		if len(inputs) == 0 || len(inputs) > 200 {
			return fail(ErrIAMInvalidRelation)
		}
		touched := map[string]bool{}
		for _, input := range inputs {
			a := input
			a.Context = req.Context
			a.AssignedBy = v.actor.UserID
			if method != "BatchAssignUserRoles" && (a.UserID != req.UserID || a.ID != req.ID) {
				return fail(ErrIAMInvalidRelation)
			}
			key := fmt.Sprint(a.UserID, ":", a.RoleID)
			if touched[key] {
				return fail(ErrIAMInvalidRelation)
			}
			touched[key] = true
			user, err := uc.repo.User(ctx, tx, a.UserID)
			if err != nil {
				return fail(err)
			}
			if user.Status != UserStatusEnabled {
				return fail(ErrIAMProtected)
			}
			targetRoles, err := iamFutureRoles(state, a.UserID, v.now)
			if err != nil {
				return fail(err)
			}
			if slices.ContainsFunc(state.Roles, func(r IAMRole) bool { return r.Builtin && r.Code == "root" && slices.Contains(targetRoles, r.ID) }) {
				return fail(ErrIAMProtected)
			}
			i := slices.IndexFunc(state.Assignments, func(old IAMAssignment) bool { return old.ID == a.ID && a.ID > 0 })
			expected := req.ExpectedRevision
			if method == "BatchAssignUserRoles" {
				expected = a.Revision
			}
			if a.ID == 0 {
				if expected != 0 || method == "RevokeUserRole" {
					return fail(ErrIAMRevisionConflict)
				}
				a.Origin = "explicit"
				a.MigrationBatchID = ""
				a.Revision = 1
			} else {
				if i < 0 {
					return fail(ErrIAMNotFound)
				}
				old := state.Assignments[i]
				if old.UserID != a.UserID || old.RoleID != a.RoleID {
					return fail(ErrIAMInvalidRelation)
				}
				if expected == 0 || old.Revision != expected {
					return fail(ErrIAMRevisionConflict)
				}
				a.Origin, a.MigrationBatchID, a.Revision = old.Origin, old.MigrationBatchID, old.Revision
				if method == "RevokeUserRole" {
					a = old
					a.Revoked = true
					a.AssignedBy = v.actor.UserID
				}
			}
			if a.Validity.StartsAt.IsZero() {
				a.Validity.StartsAt = v.now
			}
			if a.Validity.Validate() != nil || a.Boundary.Clauses == nil || a.Boundary.Validate([]authorization.ScopeKind{authorization.All, authorization.Self, authorization.Users, authorization.Resources, authorization.Groups}) != nil {
				return fail(ErrIAMInvalidRelation)
			}
			roleIndex := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == a.RoleID })
			if roleIndex < 0 || state.Roles[roleIndex].Status != "enabled" && !a.Revoked {
				return fail(ErrIAMInvalidRelation)
			}
			if err = v.permit(op, a.RoleID, a.UserID); err != nil {
				return fail(err)
			}
			interval := &a.Validity
			action := op
			if a.Revoked {
				interval = nil
				action = "identity.user_role.revoke"
				if err := v.permit(action, a.RoleID, a.UserID); err != nil {
					return fail(err)
				}
			}
			if err = v.manageRole(state, a.RoleID, action, a.UserID, interval, a.Boundary); err != nil {
				return fail(err)
			}
			if iamAssignmentRemovesDeny(v.state, a, v.now) {
				if err = v.expandedUserCeiling(state, a.RoleID, a.UserID, action); err != nil {
					return fail(err)
				}
			}
			out.Assignments = append(out.Assignments, a)
			if a.ID == 0 {
				candidate := a
				for _, old := range state.Assignments {
					if old.ID >= candidate.ID {
						candidate.ID = old.ID + 1
					}
				}
				state.Assignments = append(state.Assignments, candidate)
			} else {
				state.Assignments[i] = a
			}
			out.AffectedUserIDs = append(out.AffectedUserIDs, a.UserID)
		}
	case "CreateDelegation", "UpdateDelegation", "RevokeDelegation":
		if req.Delegation == nil && method != "RevokeDelegation" {
			return fail(ErrIAMInvalidRelation)
		}
		var d m.Delegation
		var old *m.Delegation
		if req.Delegation != nil {
			d = *req.Delegation
		}
		d.ID = req.ID
		d.Context = req.Context
		if method != "CreateDelegation" {
			i := slices.IndexFunc(v.delegations, func(d m.Delegation) bool { return d.ID == req.ID })
			if i < 0 {
				return fail(ErrIAMNotFound)
			}
			copy := v.delegations[i]
			old = &copy
			if req.ExpectedRevision == 0 || copy.Revision != req.ExpectedRevision {
				return fail(ErrIAMRevisionConflict)
			}
			if method == "RevokeDelegation" {
				d = copy
			} else {
				if len(req.UpdateMask) == 0 {
					return fail(ErrIAMInvalidRelation)
				}
				input := d
				d = copy
				for _, field := range req.UpdateMask {
					switch field {
					case "actions":
						d.Actions = input.Actions
					case "target_user_scope":
						d.TargetUserScope = input.TargetUserScope
					case "grant_ceiling":
						d.GrantCeiling = input.GrantCeiling
					case "can_redelegate":
						d.CanRedelegate = input.CanRedelegate
					case "validity":
						d.Validity = input.Validity
					case "validity.starts_at":
						d.Validity.StartsAt = input.Validity.StartsAt
					case "validity.expires_at":
						d.Validity.ExpiresAt = input.Validity.ExpiresAt
					default:
						return fail(ErrIAMInvalidRelation)
					}
				}
			}
		} else {
			if req.ID != 0 || req.ExpectedRevision != 0 {
				return fail(ErrIAMRevisionConflict)
			}
		}
		if d.Validity.StartsAt.IsZero() {
			d.Validity.StartsAt = v.now
		}
		if err := v.governDelegation(d, op, old); err != nil {
			return fail(err)
		}
		if method == "RevokeDelegation" {
			d.Actions = []string{}
			d.CanRedelegate = false
		}
		out.Delegations = []m.Delegation{d}
	case "CreateRoleConstraint", "UpdateRoleConstraint", "DeleteRoleConstraint":
		if !v.root {
			return fail(ErrIAMProtected)
		} // Global constraints need full impact visibility.
		ch := IAMConstraintChange{ExpectedRevision: req.ExpectedRevision}
		if method == "DeleteRoleConstraint" {
			id := req.ID
			ch.DeleteConstraint = &id
		} else {
			if req.Constraint == nil {
				return fail(ErrIAMInvalidRelation)
			}
			c := *req.Constraint
			if method == "UpdateRoleConstraint" {
				i := slices.IndexFunc(v.state.Constraints, func(old IAMRoleConstraint) bool { return old.ID == req.ID })
				if i < 0 {
					return fail(ErrIAMNotFound)
				}
				if len(req.UpdateMask) == 0 {
					return fail(ErrIAMInvalidRelation)
				}
				c = v.state.Constraints[i]
				for _, field := range req.UpdateMask {
					switch field {
					case "name":
						c.Name = req.Constraint.Name
					case "role_ids":
						c.RoleIDs = slices.Clone(req.Constraint.RoleIDs)
					case "max_count":
						c.MaxCount = req.Constraint.MaxCount
					case "enabled":
						c.Enabled = req.Constraint.Enabled
					default:
						return fail(ErrIAMInvalidRelation)
					}
				}
			}
			c.ID = req.ID
			c.Context = req.Context
			ch.Constraint = &c
			out.Constraints = []IAMRoleConstraint{c}
		}
		proposed, _, err := iamProposeChanges(state, []IAMConstraintChange{ch}, v.now)
		if err != nil {
			return fail(err)
		}
		state = proposed
	case "CreatePermission", "UpdatePermission", "SetPermissionStatus", "ArchivePermission":
		if method != "UpdatePermission" && !v.root {
			return fail(ErrIAMProtected)
		}
		if req.Permission == nil && method != "ArchivePermission" {
			return fail(ErrIAMInvalidRelation)
		}
		var p m.Permission
		if req.Permission != nil {
			p = *req.Permission
		}
		p.ID = req.ID
		if method == "CreatePermission" {
			if req.ID != 0 {
				return fail(ErrIAMInvalidRelation)
			}
			if req.ExpectedRevision != 0 {
				return fail(ErrIAMRevisionConflict)
			}
			p.Status = "draft"
			p.Binding = "unbound"
			// Unknown codes may exist only as unbound metadata drafts. They never
			// become executable through lifecycle APIs or role grants.
			resources, err := uc.repo.Resources(ctx, tx)
			if err != nil {
				return fail(err)
			}
			if !slices.ContainsFunc(resources, func(r m.Resource) bool {
				return r.ID == p.ResourceID && strings.HasPrefix(p.Code, r.Code+".") && len(strings.TrimPrefix(p.Code, r.Code+".")) > 0
			}) {
				return fail(ErrIAMInvalidRelation)
			}
		} else {
			i := slices.IndexFunc(v.permissions, func(p m.Permission) bool { return p.ID == req.ID })
			if i < 0 {
				return fail(ErrIAMNotFound)
			}
			old := v.permissions[i]
			if req.ExpectedRevision == 0 || old.Revision != req.ExpectedRevision {
				return fail(ErrIAMRevisionConflict)
			}
			if old.Status == "archived" {
				return fail(ErrIAMProtected)
			}
			p = old
			switch method {
			case "UpdatePermission":
				if len(req.UpdateMask) == 0 {
					return fail(ErrIAMInvalidRelation)
				}
				for _, field := range req.UpdateMask {
					switch field {
					case "name":
						p.Name = req.Permission.Name
					case "category":
						p.Category = req.Permission.Category
					case "description":
						p.Description = req.Permission.Description
					case "sort":
						p.Sort = req.Permission.Sort
					case "risk_level":
						p.RiskLevel = req.Permission.RiskLevel
					default:
						return fail(ErrIAMInvalidRelation)
					}
				}
			case "SetPermissionStatus":
				if !slices.Contains([]string{"enabled", "disabled"}, req.Permission.Status) {
					return fail(ErrIAMInvalidRelation)
				}
				p.Status = req.Permission.Status
			case "ArchivePermission":
				p.Status = "archived"
			}
		}
		if strings.TrimSpace(p.Name) == "" || len(p.Name) > 255 || len(p.Code) > 160 || len(p.Description) > 8192 || len(p.Category) > 64 || len(p.RiskLevel) > 16 {
			return fail(ErrIAMInvalidRelation)
		}
		if p.RiskLevel == "" {
			p.RiskLevel = "normal"
		}
		operation, ok := authorization.Lookup(p.Code)
		if p.Status == "enabled" && (!ok || !IAMExecutionBound(p.Code)) {
			return fail(ErrIAMInvalidRelation)
		}
		if err := v.permit(op, p.ID, 0); err != nil {
			return fail(err)
		}
		if (p.Status == "disabled" || p.Status == "archived") && operation.Protected {
			return fail(ErrIAMProtected)
		}
		if p.Status == "archived" && len(iamPermissionReferences(v, p.Code)) > 0 {
			return fail(ErrIAMInvalidRelation)
		}
		out.Permissions = []m.Permission{p}
	case "CreateMenuItem", "UpdateMenuItem", "ArchiveMenuItem":
		if !v.root {
			return fail(ErrIAMProtected)
		}
		menus, err := uc.repo.Menus(ctx, tx)
		if err != nil {
			return fail(err)
		}
		var menu m.Menu
		if method == "CreateMenuItem" {
			if req.Menu == nil || req.ID != 0 || req.ExpectedRevision != 0 {
				return fail(ErrIAMInvalidRelation)
			}
			menu = *req.Menu
			menu.ID = 0
		} else {
			i := slices.IndexFunc(menus, func(x m.Menu) bool { return x.ID == req.ID })
			if i < 0 {
				return fail(ErrIAMNotFound)
			}
			menu = menus[i]
			if req.ExpectedRevision == 0 || menu.Revision != req.ExpectedRevision {
				return fail(ErrIAMRevisionConflict)
			}
			if method == "ArchiveMenuItem" {
				menu.Enabled = false
			} else {
				if req.Menu == nil || len(req.UpdateMask) == 0 {
					return fail(ErrIAMInvalidRelation)
				}
				for _, field := range req.UpdateMask {
					switch field {
					case "name":
						menu.Name = req.Menu.Name
					case "route_key":
						menu.RouteKey = req.Menu.RouteKey
					case "icon_key":
						menu.IconKey = req.Menu.IconKey
					case "parent_id":
						menu.ParentID = req.Menu.ParentID
					case "sort":
						menu.Sort = req.Menu.Sort
					case "enabled":
						menu.Enabled = req.Menu.Enabled
					case "required_all":
						menu.RequiredAll = req.Menu.RequiredAll
					case "required_any":
						menu.RequiredAny = req.Menu.RequiredAny
					default:
						return fail(ErrIAMInvalidRelation)
					}
				}
			}
		}
		if err = iamValidateMenu(menu, menus); err != nil {
			return fail(err)
		}
		out.Menus = []m.Menu{menu}
	case "RevokeUserSessions":
		if req.UserID <= 0 || req.ExpectedRevision == 0 {
			return fail(ErrIAMInvalidRelation)
		}
		rev, err := uc.repo.UserRevision(ctx, tx, req.UserID)
		if err != nil {
			return fail(err)
		}
		if rev != req.ExpectedRevision {
			return fail(ErrIAMRevisionConflict)
		}
		if err = v.credentialScope(req.UserID, op); err != nil {
			return fail(err)
		}
	default:
		return fail(ErrIAMInvalidRelation)
	}
	return state, out, nil
}
func iamValidateRole(r IAMRole, state IAMConstraintState) error {
	if r.Context != state.Context || r.Builtin || r.Code == "root" || strings.TrimSpace(r.Name) == "" || len(r.Name) > 255 || len(r.Description) > 8192 {
		return ErrIAMProtected
	}
	if _, err := iamStructuralRoles(state, r.ID); err != nil {
		return err
	}
	for _, id := range r.Inherits {
		for _, child := range state.Roles {
			if child.ID == id && (child.Code == "root" || child.Status == "archived") {
				return ErrIAMProtected
			}
		}
	}
	touched := map[string]bool{}
	for _, g := range r.Grants {
		op, ok := authorization.Lookup(g.Operation)
		key := g.Operation + ":" + string(g.Effect)
		if !ok || !slices.Contains(op.ContextTypes, r.Context.Type) || (g.Effect != authorization.Allow && g.Effect != authorization.Deny) || g.Scope.Clauses == nil || g.Scope.Validate(op.Scopes) != nil || touched[key] {
			return ErrIAMScopeInvalid
		}
		touched[key] = true
	}
	return nil
}
func (v iamManagementView) credentialScope(uid int64, op string) error {
	if uid == v.actor.UserID {
		return ErrIAMProtected
	}
	future, err := iamFutureRoles(v.state, uid, v.now)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(v.state.Roles, func(r IAMRole) bool { return r.Code == "root" && r.Builtin && slices.Contains(future, r.ID) }) {
		return ErrIAMProtected
	}
	if v.root {
		return nil
	}
	for _, d := range v.delegations {
		if slices.Contains(v.active, d.ManagerRoleID) && slices.Contains(d.Actions, op) && IAMCheckCredentialTakeover(v.now, v.state, v.delegations, v.actor.UserID, uid, d) == nil {
			return nil
		}
	}
	return ErrIAMProtected
}
func (uc *IAMGovernanceUsecase) persist(ctx context.Context, tx IAMTx, v iamManagementView, method string, req m.Request, out m.Response) (m.Response, error) {
	var err error
	if len(out.Roles) > 0 {
		out.Roles[0], err = uc.repo.SaveManagedRole(ctx, tx, out.Roles[0], req.ExpectedRevision)
	}
	for i := range out.Assignments {
		expected := req.ExpectedRevision
		if method == "BatchAssignUserRoles" {
			expected = out.Assignments[i].Revision
			if out.Assignments[i].ID == 0 {
				expected = 0
			}
		}
		out.Assignments[i], err = uc.repo.SaveAssignment(ctx, tx, out.Assignments[i], expected)
		if err != nil {
			return m.Response{}, err
		}
	}
	if len(out.Delegations) > 0 {
		out.Delegations[0], err = uc.repo.SaveDelegation(ctx, tx, out.Delegations[0], req.ExpectedRevision)
	}
	if len(out.Permissions) > 0 {
		out.Permissions[0], err = uc.repo.SavePermission(ctx, tx, out.Permissions[0], req.ExpectedRevision)
	}
	if len(out.Menus) > 0 {
		out.Menus[0], err = uc.repo.SaveMenu(ctx, tx, out.Menus[0], req.ExpectedRevision)
	}
	if len(out.Constraints) > 0 {
		err = uc.repo.SaveConstraint(ctx, tx, out.Constraints[0], req.ExpectedRevision)
		if err == nil {
			state, e := uc.repo.ConstraintState(ctx, tx, req.Context)
			if e != nil {
				return m.Response{}, e
			}
			out.Constraints = state.Constraints
		}
	}
	if method == "DeleteRoleConstraint" {
		err = uc.repo.DeleteConstraint(ctx, tx, req.Context, req.ID, req.ExpectedRevision)
	}
	if method == "RevokeUserSessions" {
		err = uc.repo.RevokeUserSessions(ctx, tx, req.UserID, v.now)
		if err == nil {
			err = uc.repo.AdvanceUser(ctx, tx, req.UserID, req.ExpectedRevision)
		}
	}
	if err != nil {
		return m.Response{}, err
	}
	return out, nil
}
func iamRoleReferences(v iamManagementView, id int64) []m.Reference {
	out := []m.Reference{}
	for _, r := range v.state.Roles {
		if slices.Contains(r.Inherits, id) {
			out = append(out, m.Reference{Kind: "inheritance", ID: r.ID})
		}
	}
	for _, a := range v.state.Assignments {
		if a.RoleID == id && !a.Revoked && (a.Validity.ExpiresAt == nil || a.Validity.ExpiresAt.After(v.now)) {
			out = append(out, m.Reference{Kind: "assignment", ID: a.ID, UserID: a.UserID})
		}
	}
	for _, d := range v.delegations {
		if (d.ManagerRoleID == id || d.TargetRoleID == id) && len(d.Actions) > 0 && (d.Validity.ExpiresAt == nil || d.Validity.ExpiresAt.After(v.now)) {
			out = append(out, m.Reference{Kind: "delegation", ID: d.ID})
		}
	}
	for _, c := range v.state.Constraints {
		if slices.Contains(c.RoleIDs, id) {
			out = append(out, m.Reference{Kind: "constraint", ID: c.ID})
		}
	}
	return out
}
func iamPermissionReferences(v iamManagementView, code string) []m.Reference {
	out := []m.Reference{}
	for _, r := range v.state.Roles {
		if slices.ContainsFunc(r.Grants, func(g IAMGrant) bool { return g.Operation == code }) {
			out = append(out, m.Reference{Kind: "role", ID: r.ID})
		}
	}
	for _, d := range v.delegations {
		if slices.ContainsFunc(d.GrantCeiling, func(c m.Ceiling) bool { return c.Operation == code }) {
			out = append(out, m.Reference{Kind: "delegation", ID: d.ID})
		}
	}
	for _, menu := range v.menus {
		if slices.Contains(menu.RequiredAll, code) || slices.Contains(menu.RequiredAny, code) {
			out = append(out, m.Reference{Kind: "menu", ID: menu.ID})
		}
	}
	return out
}
func iamValidateMenu(menu m.Menu, menus []m.Menu) error {
	if !slices.Contains([]string{"overview", "users", "channels", "models", "routing-groups", "pricing", "payments", "subscriptions", "logs", "settings", "iam"}, menu.RouteKey) || !slices.Contains([]string{"home", "users", "server", "box", "route", "coins", "credit-card", "calendar", "file-text", "settings", "shield"}, menu.IconKey) || strings.TrimSpace(menu.Name) == "" {
		return ErrIAMInvalidRelation
	}
	for _, code := range append(slices.Clone(menu.RequiredAll), menu.RequiredAny...) {
		if _, ok := authorization.Lookup(code); !ok {
			return ErrIAMInvalidRelation
		}
	}
	seen := map[int64]bool{menu.ID: true}
	parent := menu.ParentID
	for parent != 0 {
		if seen[parent] {
			return ErrIAMInvalidRelation
		}
		seen[parent] = true
		i := slices.IndexFunc(menus, func(x m.Menu) bool { return x.ID == parent })
		if i < 0 {
			return ErrIAMNotFound
		}
		parent = menus[i].ParentID
	}
	return nil
}
