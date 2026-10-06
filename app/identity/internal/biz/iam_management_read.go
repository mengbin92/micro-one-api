package biz

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/pkg/filtering"
	"micro-one-api/pkg/ordering"
	"micro-one-api/pkg/pagination"
)

func (uc *IAMGovernanceUsecase) read(ctx context.Context, tx IAMTx, v iamManagementView, method string, req m.Request, op string) (m.Response, error) {
	out := m.Response{BasePolicyRevision: v.policy.PolicyRevision, Versions: authorization.Versions{Policy: v.policy.PolicyRevision, Catalog: v.policy.CatalogRevision, User: v.userRevision, Session: v.session.SessionRevision, SessionContext: v.session.Revision}}
	if err := v.readAdmission(method, op, req); err != nil {
		return out, err
	}
	visibleRole := v.visibleRole
	visibleUser := func(a IAMAssignment, action string) bool {
		return v.visibleAssignment(v.state, a, action)
	}
	switch method {
	case "ListResources":
		resources, err := uc.repo.Resources(ctx, tx)
		if err != nil {
			return out, err
		}
		for _, r := range resources {
			if slices.ContainsFunc(v.permissions, func(p m.Permission) bool { return p.ResourceID == r.ID && v.permit(op, p.ID, 0) == nil }) {
				out.Resources = append(out.Resources, r)
			}
		}
	case "ListPermissions", "GetPermission", "GetPermissionReferences":
		for _, p := range v.permissions {
			if (req.ID == 0 || p.ID == req.ID) && v.permit(op, p.ID, 0) == nil {
				out.Permissions = append(out.Permissions, p)
			}
		}
		if method != "ListPermissions" {
			if len(out.Permissions) != 1 {
				return out, ErrIAMNotFound
			}
			if method == "GetPermissionReferences" {
				for _, ref := range iamPermissionReferences(v, out.Permissions[0].Code) {
					if ref.Kind == "role" {
						i := slices.IndexFunc(v.state.Roles, func(r IAMRole) bool { return r.ID == ref.ID })
						if i >= 0 && visibleRole(v.state.Roles[i], "iam.role.read") {
							out.References = append(out.References, ref)
						}
					} else if v.root {
						out.References = append(out.References, ref)
					}
				}
			}
		}
	case "ListRoles", "GetRole", "GetRolePermissions", "GetRoleReferences", "ListRoleMembers":
		for _, r := range v.state.Roles {
			if (req.ID == 0 || r.ID == req.ID) && visibleRole(r, op) {
				out.Roles = append(out.Roles, r)
			}
		}
		if method != "ListRoles" {
			if len(out.Roles) != 1 {
				return out, ErrIAMNotFound
			}
			if method == "GetRoleReferences" {
				for _, ref := range iamRoleReferences(v, req.ID) {
					if v.root {
						out.References = append(out.References, ref)
					} else if ref.Kind == "assignment" {
						for _, a := range v.state.Assignments {
							if a.ID == ref.ID && visibleUser(a, "identity.user_role.read") {
								out.References = append(out.References, ref)
							}
						}
					}
				}
			}
			if method == "ListRoleMembers" {
				for _, a := range v.state.Assignments {
					if a.RoleID == req.ID && visibleUser(a, op) {
						out.Assignments = append(out.Assignments, a)
					}
				}
			}
		}
		// Separate grant/graph read from metadata; no role.list backdoor.
		{
			for i := range out.Roles {
				if !visibleRole(out.Roles[i], "iam.role.permissions.read") {
					out.Roles[i].Grants = nil
				}
				out.Roles[i].Inherits = slices.DeleteFunc(slices.Clone(out.Roles[i].Inherits), func(id int64) bool {
					i := slices.IndexFunc(v.state.Roles, func(r IAMRole) bool { return r.ID == id })
					return i < 0 || !visibleRole(v.state.Roles[i], "iam.role.read")
				})
			}
		}
		if method == "GetRolePermissions" && out.Roles[0].Status == "enabled" {
			sources, err := IAMSources(req.Context, func() map[int64]IAMRole {
				roles := map[int64]IAMRole{}
				for _, role := range v.state.Roles {
					roles[role.ID] = role
				}
				return roles
			}(), []IAMAssignment{{ID: 1, UserID: v.actor.UserID, RoleID: req.ID, Context: req.Context, Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Validity: authorization.Interval{StartsAt: v.now}}}, []int64{req.ID})
			if err != nil {
				return out, err
			}
			for _, source := range sources {
				if v.visibleGrantPath(v.state, source) {
					// This is a role projection, not a real user assignment.
					source.AssignmentID = 0
					out.Sources = append(out.Sources, source)
				}
			}
		}

	case "GetUserRoles":
		if req.UserID <= 0 {
			return out, ErrIAMInvalidRelation
		}
		for _, a := range v.state.Assignments {
			if a.UserID == req.UserID && visibleUser(a, op) {
				out.Assignments = append(out.Assignments, a)
			}
		}
	case "ListDelegations":
		for _, d := range v.delegations {
			if v.permit(op, d.ID, 0) == nil && (v.root || slices.Contains(v.active, d.ManagerRoleID)) {
				out.Delegations = append(out.Delegations, d)
			}
		}
	case "ListRoleConstraints":
		for _, c := range v.state.Constraints {
			if v.permit(op, c.ID, 0) != nil {
				continue
			}
			if v.root || !slices.ContainsFunc(c.RoleIDs, func(id int64) bool {
				i := slices.IndexFunc(v.state.Roles, func(r IAMRole) bool { return r.ID == id })
				return i < 0 || !visibleRole(v.state.Roles[i], "iam.role.read")
			}) {
				out.Constraints = append(out.Constraints, c)
			}
		}
	case "ListMenuItems":
		menus, err := uc.repo.Menus(ctx, tx)
		if err != nil {
			return out, err
		}
		for _, menu := range menus {
			if v.permit(op, menu.ID, 0) == nil {
				out.Menus = append(out.Menus, menu)
			}
		}
	case "ListAuthorizationAuditEvents", "GetAuthorizationAuditEvent", "ExportAuthorizationAuditEvents":
		ids := []int64{}
		for _, r := range v.state.Roles {
			if visibleRole(r, "iam.role.read") && v.permit(op, r.ID, 0) == nil {
				ids = append(ids, r.ID)
			}
		}
		events, err := uc.repo.ManagedAuditEvents(ctx, tx, ids, v.root)
		if err != nil {
			return out, err
		}
		for _, e := range events {
			if method == "GetAuthorizationAuditEvent" && e.EventID != req.EventID {
				continue
			}
			if !v.root {
				// Before/after/diff may name users outside the role's managed scope. Only
				// root receives raw policy payloads; scoped audits expose a safe summary.
				e.Before, e.After, e.Diff = "{}", "{}", "{}"
				e.Actor.SessionID = ""
				e.Actor.ServiceID = ""
				e.RequestID = ""
				e.Reason = ""
			}
			out.Audits = append(out.Audits, m.Audit(e))
		}
		if method == "GetAuthorizationAuditEvent" && len(out.Audits) != 1 {
			return out, ErrIAMNotFound
		}
	case "CheckAuthorization":
		// The selector names an actual IAM endpoint, not a client permission string.
		// Its operation, targets and invariants come from the same owner handlers.
		point := req.Operation
		registered, ok := IAMMethods[point]
		if !ok || point == "CheckAuthorization" || point == "ExplainAuthorization" || !IAMExecutionBound(registered) {
			return out, ErrIAMInvalidRelation
		}
		var checkErr error
		if iamWriteMethod(point) {
			var proposed IAMConstraintState
			proposed, _, checkErr = uc.propose(ctx, tx, v, point, req, registered)
			if checkErr == nil {
				conflicts, e := IAMCheckConstraints(v.now, proposed)
				checkErr = e
				if len(conflicts) > 0 {
					checkErr = ErrIAMConstraintsViolated
				}
			}
		} else {
			_, checkErr = uc.read(ctx, tx, v, point, req, registered)
		}
		decision := authorization.Decision{Allowed: checkErr == nil, Reason: "ALLOWED", Context: req.Context, Operation: registered, Versions: out.Versions}
		if checkErr != nil {
			decision.Reason = "BUSINESS_RULE_VIOLATION"
		}
		out.Decision = &decision
	case "GetUserEffectivePermissions", "ExplainAuthorization", "ListUserSessions":
		if req.UserID <= 0 {
			return out, ErrIAMInvalidRelation
		}
		if req.UserID != v.actor.UserID {
			// A partial manager cannot probe another user's hidden assignments through
			// explanations. Require visibility for every effective/future source.
			for _, a := range v.state.Assignments {
				if a.UserID == req.UserID && !a.Revoked && (a.Validity.ExpiresAt == nil || a.Validity.ExpiresAt.After(v.now)) && !visibleUser(a, "identity.user_role.read") {
					return out, ErrIAMProtected
				}
			}
		}
		if method == "ListUserSessions" {
			targetRevision, err := uc.repo.UserRevision(ctx, tx, req.UserID)
			if err != nil {
				return out, err
			}
			out.TargetRevision = targetRevision
			for _, s := range v.state.Sessions {
				if s.UserID == req.UserID {
					out.Sessions = append(out.Sessions, s)
				}
			}
			break
		}
		roles := map[int64]IAMRole{}
		for _, r := range v.state.Roles {
			roles[r.ID] = r
		}
		assignments := []IAMAssignment{}
		for _, a := range v.state.Assignments {
			if a.UserID == req.UserID {
				assignments = append(assignments, a)
			}
		}
		active, err := iamAuthorizedRoles(v.now, v.state, req.UserID)
		if err != nil {
			return out, err
		}
		if req.UserID == v.actor.UserID {
			active = v.active
		}
		out.Sources, err = IAMSources(req.Context, roles, assignments, active)
		if err != nil {
			return out, err
		}
		if req.UserID != v.actor.UserID {
			for _, source := range out.Sources {
				if !v.visibleGrantPath(v.state, source) {
					return m.Response{}, ErrIAMProtected
				}
			}
		}
		if method == "ExplainAuthorization" {
			operation, ok := authorization.Lookup(req.Operation)
			if !ok {
				return out, ErrIAMInvalidRelation
			}
			// This API is a simulation, never a capability issued from browser facts.
			// Business execution CheckAuthorization will be owned by B0 fixed callers.
			if IAMExecutionBound(operation.Code) {
				operation.Binding = "bound"
			}
			enabled := slices.ContainsFunc(v.permissions, func(p m.Permission) bool { return p.Code == operation.Code && p.Status == "enabled" })
			target, err := uc.repo.User(ctx, tx, req.UserID)
			if err != nil {
				return out, err
			}
			actor := v.actor
			actor.UserID = req.UserID
			d := authorization.Decide(authorization.Input{Actor: actor, Context: req.Context, Operation: operation, Object: req.Object, Sources: out.Sources, Versions: out.Versions, Now: v.now, IdentityValid: target.Status == UserStatusEnabled, SessionValid: true, ConstraintsPass: true, BusinessRulesPass: true, OperationEnabled: enabled})
			out.Decision = &d
		}
	default:
		return out, ErrIAMInvalidRelation
	}
	return iamPageResponse(out, req, method+"\x00"+iamID(v.actor.UserID)+"\x00"+v.actor.SessionID+"\x00"+strconv.FormatUint(v.session.Revision, 10))
}
func iamCollectionPage[T any](items []T, req m.Request, key string, fields []string, describe func(T) map[string]string) ([]T, int64, string, error) {
	if req.PageSize < 0 || req.PageSize > 200 {
		return nil, 0, "", ErrIAMInvalidRelation
	}
	limit := int(req.PageSize)
	if limit == 0 {
		limit = 50
	}
	filters, err := filtering.Equalities(req.Filter, fields...)
	if err != nil {
		return nil, 0, "", ErrIAMInvalidRelation
	}
	orders, err := ordering.Parse(req.OrderBy, append([]string{"id"}, fields...)...)
	if err != nil {
		return nil, 0, "", ErrIAMInvalidRelation
	}
	items = slices.DeleteFunc(slices.Clone(items), func(item T) bool {
		values := describe(item)
		for field, expected := range filters {
			if values[field] != expected {
				return true
			}
		}
		return false
	})
	if len(orders) > 0 {
		slices.SortStableFunc(items, func(a, b T) int {
			x, y := describe(a), describe(b)
			for _, o := range orders {
				var cmp int
				if strings.HasSuffix(o.Name, "id") || o.Name == "sort" {
					i, ei := strconv.ParseInt(x[o.Name], 10, 64)
					j, ej := strconv.ParseInt(y[o.Name], 10, 64)
					if ei != nil || ej != nil {
						cmp = strings.Compare(x[o.Name], y[o.Name])
					} else if i < j {
						cmp = -1
					} else if i > j {
						cmp = 1
					}
				} else {
					cmp = strings.Compare(x[o.Name], y[o.Name])
				}
				if cmp != 0 {
					if o.Desc {
						cmp = -cmp
					}
					return cmp
				}
			}
			return 0
		})
	}
	query := key + "\x00" + req.Filter + "\x00" + req.OrderBy + "\x00" + strconv.Itoa(limit)
	offset, err := pagination.Offset(req.PageToken, query)
	if err != nil {
		return nil, 0, "", ErrIAMInvalidRelation
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	next := ""
	if end < total {
		next = pagination.Token(end, query)
	}
	return items[offset:end], int64(total), next, nil
}
func iamPageResponse(out m.Response, req m.Request, methodKey string) (m.Response, error) {
	method := strings.Split(methodKey, "\x00")[0]
	key := req.Context.Key + "\x00" + methodKey + "\x00" + iamID(req.ID) + "\x00" + iamID(req.UserID) + "\x00" + strconv.FormatUint(out.BasePolicyRevision, 10)
	var err error
	switch method {
	case "ListRoles":
		out.Roles, out.Total, out.NextPageToken, err = iamCollectionPage(out.Roles, req, key, []string{"code", "status", "name"}, func(r IAMRole) map[string]string {
			return map[string]string{"id": iamID(r.ID), "code": r.Code, "status": r.Status, "name": r.Name}
		})
	case "ListPermissions":
		if req.OrderBy == "" {
			req.OrderBy = "sort,id"
		}
		out.Permissions, out.Total, out.NextPageToken, err = iamCollectionPage(out.Permissions, req, key, []string{"code", "status", "name", "category", "risk_level", "sort"}, func(p m.Permission) map[string]string {
			return map[string]string{"id": iamID(p.ID), "code": p.Code, "status": p.Status, "name": p.Name, "category": p.Category, "risk_level": p.RiskLevel, "sort": strconv.FormatInt(int64(p.Sort), 10)}
		})
	case "ListResources":
		out.Resources, out.Total, out.NextPageToken, err = iamCollectionPage(out.Resources, req, key, []string{"code", "name"}, func(r m.Resource) map[string]string {
			return map[string]string{"id": iamID(r.ID), "code": r.Code, "name": r.Name}
		})
	case "ListDelegations":
		out.Delegations, out.Total, out.NextPageToken, err = iamCollectionPage(out.Delegations, req, key, []string{"target_kind", "manager_role_id"}, func(d m.Delegation) map[string]string {
			return map[string]string{"id": iamID(d.ID), "target_kind": d.TargetKind, "manager_role_id": iamID(d.ManagerRoleID)}
		})
	case "ListRoleConstraints":
		out.Constraints, out.Total, out.NextPageToken, err = iamCollectionPage(out.Constraints, req, key, []string{"kind", "name"}, func(c m.Constraint) map[string]string {
			return map[string]string{"id": iamID(c.ID), "kind": c.Kind, "name": c.Name}
		})
	case "ListMenuItems":
		out.Menus, out.Total, out.NextPageToken, err = iamCollectionPage(out.Menus, req, key, []string{"name", "route_key"}, func(menu m.Menu) map[string]string {
			return map[string]string{"id": iamID(menu.ID), "name": menu.Name, "route_key": menu.RouteKey}
		})
	case "GetUserRoles", "ListRoleMembers":
		out.Assignments, out.Total, out.NextPageToken, err = iamCollectionPage(out.Assignments, req, key, []string{"user_id", "role_id"}, func(a m.Assignment) map[string]string {
			return map[string]string{"id": iamID(a.ID), "user_id": iamID(a.UserID), "role_id": iamID(a.RoleID)}
		})
	case "ListUserSessions":
		out.Sessions, out.Total, out.NextPageToken, err = iamCollectionPage(out.Sessions, req, key, []string{"session_id", "user_id"}, func(s m.Session) map[string]string {
			return map[string]string{"id": s.SessionID, "session_id": s.SessionID, "user_id": iamID(s.UserID)}
		})
	case "ListAuthorizationAuditEvents", "ExportAuthorizationAuditEvents":
		out.Audits, out.Total, out.NextPageToken, err = iamCollectionPage(out.Audits, req, key, []string{"action", "result", "actor_user_id"}, func(a m.Audit) map[string]string {
			return map[string]string{"id": a.EventID, "action": a.Action, "result": a.Result, "actor_user_id": iamID(a.Actor.UserID)}
		})
	}
	if err != nil {
		return m.Response{}, err
	}
	return out, nil
}
