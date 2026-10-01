package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/pkg/jsonx"
)

type IAMManagementRepo interface {
	IAMRuntimeRepo
	Delegations(context.Context, IAMTx, authorization.Context) ([]m.Delegation, error)
	Permissions(context.Context, IAMTx) ([]m.Permission, error)
	Resources(context.Context, IAMTx) ([]m.Resource, error)
	Menus(context.Context, IAMTx) ([]m.Menu, error)
	SaveManagedRole(context.Context, IAMTx, IAMRole, uint64) (IAMRole, error)
	SaveDelegation(context.Context, IAMTx, m.Delegation, uint64) (m.Delegation, error)
	SavePermission(context.Context, IAMTx, m.Permission, uint64) (m.Permission, error)
	SaveMenu(context.Context, IAMTx, m.Menu, uint64) (m.Menu, error)
	ManagedAuditEvents(context.Context, IAMTx, []int64, bool) ([]IAMAuditEvent, error)
}
type IAMGovernanceUsecase struct {
	repo     IAMManagementRepo
	runner   IAMTxRunner
	identity *IdentityUsecase
	now      func() time.Time
}

func NewIAMGovernanceUsecase(repo IAMManagementRepo, runner IAMTxRunner, identity *IdentityUsecase) *IAMGovernanceUsecase {
	return &IAMGovernanceUsecase{repo: repo, runner: runner, identity: identity, now: time.Now}
}

type iamManagementView struct {
	userRevision   uint64
	state          IAMConstraintState
	delegations    []m.Delegation
	permissions    []m.Permission
	menus          []m.Menu
	actor          authorization.Actor
	session        IAMSessionContext
	sources        []authorization.GrantSource
	active, future []int64
	policy         authorization.PolicyState
	root           bool
	now            time.Time
}

func (uc *IAMGovernanceUsecase) view(ctx context.Context, tx IAMTx, actor authorization.Actor, c authorization.Context) (iamManagementView, error) {
	v := iamManagementView{actor: actor, now: uc.now().UTC().Truncate(time.Millisecond)}
	if c.RequirePlatform() != nil {
		return v, ErrIAMContextInvalid
	}
	var err error
	v.policy, err = uc.repo.Policy(ctx, tx)
	if err != nil {
		return v, err
	}
	if v.policy.CheckWrite(authorization.IAMManagementWrite, false) != nil {
		return v, ErrIAMCutoverBlocked
	}
	v.userRevision, err = uc.repo.UserRevision(ctx, tx, actor.UserID)
	if err != nil {
		return v, err
	}
	user, err := uc.repo.User(ctx, tx, actor.UserID)
	if err != nil {
		return v, err
	}
	if err = checkIAMIdentity(actor, user, v.now); err != nil {
		return v, err
	}
	v.session, err = uc.repo.Session(ctx, tx, actor.SessionID, c)
	if err != nil {
		return v, err
	}
	if err = checkIAMSession(actor, v.session, v.now); err != nil {
		return v, err
	}
	v.state, err = uc.repo.ConstraintState(ctx, tx, c)
	if err != nil {
		return v, err
	}
	if err = iamSessionConflicts(v.now, v.state, v.session); err != nil {
		return v, err
	}
	v.active, err = iamCurrentActive(v.now, v.state, v.session)
	if err != nil {
		return v, err
	}
	if v.session.ActivationState != "active" {
		v.active = nil
	}
	v.future, err = iamFutureRoles(v.state, actor.UserID, v.now)
	if err != nil {
		return v, err
	}
	roles := map[int64]IAMRole{}
	assignments := []IAMAssignment{}
	for _, r := range v.state.Roles {
		roles[r.ID] = r
		if r.Builtin && r.Code == "root" && slices.Contains(v.active, r.ID) {
			v.root = true
		}
	}
	for _, a := range v.state.Assignments {
		if a.UserID == actor.UserID {
			assignments = append(assignments, a)
		}
	}
	v.sources, err = IAMSources(c, roles, assignments, v.active)
	if err != nil {
		return v, err
	}
	v.delegations, err = uc.repo.Delegations(ctx, tx, c)
	if err != nil {
		return v, err
	}
	v.permissions, err = uc.repo.Permissions(ctx, tx)
	if err != nil {
		return v, err
	}
	v.menus, err = uc.repo.Menus(ctx, tx)
	return v, err
}
func iamFutureRoles(state IAMConstraintState, uid int64, now time.Time) ([]int64, error) {
	// Structural closure includes disabled nodes: a credential takeover cannot
	// hide authority behind an inactive role that can later be enabled.
	roles := slices.Clone(state.Roles)
	for i := range roles {
		roles[i].Status = "enabled"
	}
	closure, err := IAMRoleClosures(state.Context, roles)
	if err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	for _, a := range state.Assignments {
		if a.UserID == uid && !a.Revoked && (a.Validity.ExpiresAt == nil || a.Validity.ExpiresAt.After(now)) {
			for _, id := range closure[a.RoleID] {
				ids[id] = true
			}
		}
	}
	return iamSortedIDs(ids), nil
}
func (v iamManagementView) permit(op string, id, uid int64) error {
	code, ok := authorization.Lookup(op)
	if !ok || !IAMExecutionBound(op) {
		return IAMDecisionError(authorization.Decision{Reason: "OPERATION_UNBOUND"})
	}
	// Binding is code-owned; persisted binding metadata can never publish a handler.
	code.Binding = "bound"
	enabled := false
	for _, p := range v.permissions {
		if p.Code == op {
			enabled = p.Status == "enabled"
		}
	}
	// Even root requires an explicitly enabled operation. D0's trusted setup
	// must enable the recovery catalog before IAM cutover; ordinary lifecycle
	// APIs cannot disable protected entries or alter their code-owned binding.
	if v.root && enabled {
		return nil
	}
	d := authorization.Decide(authorization.Input{Actor: v.actor, Context: v.state.Context, Operation: code, Object: authorization.ObjectFacts{Context: v.state.Context, ResourceID: id, OwnerUserID: uid}, Sources: v.sources, Now: v.now, IdentityValid: true, SessionValid: v.session.ActivationState == "active", ConstraintsPass: true, BusinessRulesPass: true, OperationEnabled: enabled})
	return IAMDecisionError(d)
}
func IAMExecutionBound(op string) bool {
	for _, bound := range IAMMethods {
		if op == bound {
			return true
		}
	}
	return slices.Contains([]string{"iam.role.disable", "iam.permission.disable", "iam.authorization.simulate", "iam.authorization.self.read", "identity.session.roles.read", "identity.session.roles.activate", "identity.session.self.revoke"}, op)
}
func iamStructuralRoles(state IAMConstraintState, id int64) ([]IAMRole, error) {
	roles := slices.Clone(state.Roles)
	for i := range roles {
		roles[i].Status = "enabled"
	}
	closure, err := IAMRoleClosures(state.Context, roles)
	if err != nil {
		return nil, err
	}
	ids, ok := closure[id]
	if !ok {
		return nil, ErrIAMNotFound
	}
	out := []IAMRole{}
	for _, r := range state.Roles {
		if slices.Contains(ids, r.ID) {
			out = append(out, r)
		}
	}
	return out, nil
}
func iamCeilingCovers(d m.Delegation, state IAMConstraintState, roleID, uid, actorID int64, boundary authorization.Scope) bool {
	roles, err := iamStructuralRoles(state, roleID)
	if err != nil {
		return false
	}
	for _, r := range roles {
		for _, g := range r.Grants {
			if g.Effect != authorization.Allow {
				continue
			}
			candidate := authorization.Intersect(g.Scope, boundary)
			if candidate.Clauses == nil {
				candidate.Clauses = []authorization.Clause{}
			}
			covered := false
			for _, c := range d.GrantCeiling {
				if c.Context == state.Context && c.Operation == g.Operation && authorization.ScopeContained(candidate, c.Scope, uid, actorID) {
					covered = true
					break
				}
			}
			if !covered {
				return false
			}
		}
	}
	return true
}
func iamDelegationTarget(d m.Delegation, r IAMRole) bool {
	return (d.TargetKind == "role" && d.TargetRoleID == r.ID) || (d.TargetKind == "role_creation" && r.CreationDelegationID == d.ID)
}
func (v iamManagementView) manageRole(state IAMConstraintState, roleID int64, action string, userID int64, interval *authorization.Interval, boundary authorization.Scope) error {
	i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == roleID })
	if i < 0 {
		return ErrIAMNotFound
	}
	r := state.Roles[i]
	if r.Code == "root" || (r.Builtin && !v.root) {
		return ErrIAMProtected
	}
	if userID == v.actor.UserID || slices.Contains(v.future, roleID) {
		return ErrIAMProtected
	}
	if v.root {
		return nil
	}
	for _, d := range v.delegations {
		if d.Context != state.Context || !d.Validity.Contains(v.now) || !slices.Contains(v.active, d.ManagerRoleID) || !slices.Contains(d.Actions, action) || !iamDelegationTarget(d, r) {
			continue
		}
		if userID > 0 && !authorization.AllowCovers([]authorization.Scope{d.TargetUserScope}, v.actor.UserID, authorization.ObjectFacts{Context: state.Context, OwnerUserID: userID}, true) {
			continue
		}
		if interval != nil && !iamIntervalContained(*interval, d.Validity) {
			continue
		}
		if !iamCeilingCovers(d, state, roleID, userID, v.actor.UserID, boundary) {
			continue
		}
		return nil
	}
	return ErrIAMProtected
}
func iamIntervalContained(a, b authorization.Interval) bool {
	return a.Validate() == nil && b.Validate() == nil && !a.StartsAt.Before(b.StartsAt) && (b.ExpiresAt == nil || (a.ExpiresAt != nil && !a.ExpiresAt.After(*b.ExpiresAt)))
}

var iamAllScope = authorization.Scope{Clauses: []authorization.Clause{{All: true}}}

func (v iamManagementView) roleChange(before, after IAMConstraintState, id int64, action string) ([]int64, error) {
	if err := v.permit(action, id, 0); err != nil {
		return nil, err
	}
	if err := v.manageRole(after, id, action, 0, nil, iamAllScope); err != nil {
		return nil, err
	}
	// Every ancestor and its existing/future members must be manageable. Checks
	// use structural closure so draft/disabled roles cannot stage indirect power.
	affected := map[int64]bool{}
	for _, r := range after.Roles {
		prior, _ := iamStructuralRoles(before, r.ID)
		next, err := iamStructuralRoles(after, r.ID)
		if err != nil {
			return nil, err
		}
		linked := r.ID == id || slices.ContainsFunc(prior, func(x IAMRole) bool { return x.ID == id }) || slices.ContainsFunc(next, func(x IAMRole) bool { return x.ID == id })
		if !linked {
			continue
		}
		if err = v.manageRole(after, r.ID, action, 0, nil, iamAllScope); err != nil {
			return nil, err
		}
		for _, a := range after.Assignments {
			if a.RoleID != r.ID || a.Revoked || (a.Validity.ExpiresAt != nil && !a.Validity.ExpiresAt.After(v.now)) {
				continue
			}
			if err = v.manageRole(after, r.ID, action, a.UserID, nil, a.Boundary); err != nil {
				return nil, err
			}
			if iamRoleHasDeny(before, id) {
				if err = v.expandedUserCeiling(after, r.ID, a.UserID, action); err != nil {
					return nil, err
				}
			}
			affected[a.UserID] = true
		}
	}
	return iamSortedIDs(affected), nil
}
func iamValidateDelegation(d m.Delegation, state IAMConstraintState) error {
	if d.Context != state.Context || d.Context.RequirePlatform() != nil || d.Validity.Validate() != nil || !iamUniquePositive([]int64{d.ManagerRoleID}) || len(d.Actions) == 0 {
		return ErrIAMInvalidRelation
	}
	manager := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == d.ManagerRoleID })
	if manager < 0 || state.Roles[manager].Code == "root" {
		return ErrIAMProtected
	}
	allowed := []string{}
	switch d.TargetKind {
	case "role":
		i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == d.TargetRoleID })
		if i < 0 || state.Roles[i].Builtin {
			return ErrIAMProtected
		}
		allowed = []string{"iam.role.read", "iam.role.copy", "iam.role.update", "iam.role.enable", "iam.role.disable", "iam.role.archive", "iam.role.permissions.update", "iam.role.permissions.read", "iam.role.hierarchy.update", "iam.role.members.read", "identity.user_role.read", "identity.user_role.assign", "identity.user_role.revoke", "identity.user_role.batch_assign"}
	case "role_creation":
		if d.TargetRoleID != 0 {
			return ErrIAMInvalidRelation
		}
		allowed = []string{"iam.role.create", "iam.role.copy", "iam.role.read", "iam.role.update", "iam.role.enable", "iam.role.disable", "iam.role.archive", "iam.role.permissions.update", "iam.role.permissions.read", "iam.role.hierarchy.update", "iam.role.members.read", "identity.user_role.read", "identity.user_role.assign", "identity.user_role.revoke", "identity.user_role.batch_assign"}
	case "user_credentials":
		if d.TargetRoleID != 0 || d.Context != authorization.Platform() {
			return ErrIAMInvalidRelation
		}
		allowed = []string{"identity.user.email_binding.update", "identity.user.credential.update", "identity.user.sessions.revoke"}
	default:
		return ErrIAMInvalidRelation
	}
	for i, a := range d.Actions {
		if !slices.Contains(allowed, a) || slices.Contains(d.Actions[:i], a) {
			return ErrIAMInvalidRelation
		}
	}
	if d.TargetUserScope.Clauses == nil || d.TargetUserScope.Validate([]authorization.ScopeKind{authorization.All, authorization.Users}) != nil {
		return ErrIAMScopeInvalid
	}
	for _, c := range d.GrantCeiling {
		op, ok := authorization.Lookup(c.Operation)
		if !ok || c.Context != state.Context || c.Scope.Clauses == nil || c.Scope.Validate(op.Scopes) != nil {
			return ErrIAMScopeInvalid
		}
	}
	return nil
}
func (v iamManagementView) governDelegation(d m.Delegation, action string, old *m.Delegation) error {
	if err := v.permit(action, d.ID, 0); err != nil {
		return err
	}
	if err := iamValidateDelegation(d, v.state); err != nil {
		return err
	}
	if slices.Contains(v.future, d.ManagerRoleID) {
		return ErrIAMProtected
	}
	if old != nil && (old.Context != d.Context || old.ManagerRoleID != d.ManagerRoleID || old.TargetKind != d.TargetKind || old.TargetRoleID != d.TargetRoleID) {
		return ErrIAMInvalidRelation
	}
	if v.root {
		return nil
	}
	for _, parent := range v.delegations {
		if parent.ID == d.ID || !parent.CanRedelegate || !parent.Validity.Contains(v.now) || !slices.Contains(v.active, parent.ManagerRoleID) || parent.TargetKind != d.TargetKind || parent.TargetRoleID != d.TargetRoleID || !iamIntervalContained(d.Validity, parent.Validity) || !authorization.ScopeContained(d.TargetUserScope, parent.TargetUserScope, v.actor.UserID, v.actor.UserID) {
			continue
		}
		if old != nil {
			oldCopy := *old
			oldCopy.Actions = d.Actions
			// Reassignment/revocation requires authority over the existing ceiling too.
			if !iamDelegationContained(oldCopy, parent, v.actor.UserID) {
				continue
			}
		}
		if iamDelegationContained(d, parent, v.actor.UserID) {
			return nil
		}
	}
	return ErrIAMProtected
}
func iamDelegationContained(d, parent m.Delegation, uid int64) bool {
	for _, a := range d.Actions {
		if !slices.Contains(parent.Actions, a) {
			return false
		}
	}
	for _, c := range d.GrantCeiling {
		if !slices.ContainsFunc(parent.GrantCeiling, func(p m.Ceiling) bool {
			return p.Context == c.Context && p.Operation == c.Operation && authorization.ScopeContained(c.Scope, p.Scope, uid, uid)
		}) {
			return false
		}
	}
	return true
}

// CheckCredentialTakeover compares every future assignment's maximum allow and
// every overlapping delegation authority, irrespective of session activation,
// deny grants or DSD. Caller holds the identity policy lock.
func IAMCheckCredentialTakeover(now time.Time, state IAMConstraintState, delegations []m.Delegation, actorID, target int64, d m.Delegation) error {
	if actorID == target || d.TargetKind != "user_credentials" || !d.Validity.Contains(now) || !authorization.AllowCovers([]authorization.Scope{d.TargetUserScope}, actorID, authorization.ObjectFacts{Context: state.Context, OwnerUserID: target}, true) {
		return ErrIAMProtected
	}
	for _, a := range state.Assignments {
		if a.UserID != target || a.Revoked || (a.Validity.ExpiresAt != nil && !a.Validity.ExpiresAt.After(now)) {
			continue
		}
		roles, err := iamStructuralRoles(state, a.RoleID)
		if err != nil {
			return err
		}
		for _, r := range roles {
			if r.Builtin && r.Code == "root" {
				return ErrIAMProtected
			}
			for _, authority := range delegations {
				if authority.ManagerRoleID == r.ID && len(authority.Actions) > 0 && iamIntervalsOverlapAfter(now, a.Validity, authority.Validity) {
					return ErrIAMProtected
				}
			}
		}
		if !iamCeilingCovers(d, state, a.RoleID, target, actorID, a.Boundary) {
			return ErrIAMProtected
		}
	}
	return nil
}
func iamIntervalsOverlapAfter(now time.Time, a, b authorization.Interval) bool {
	start := now
	if a.StartsAt.After(start) {
		start = a.StartsAt
	}
	if b.StartsAt.After(start) {
		start = b.StartsAt
	}
	return (a.ExpiresAt == nil || a.ExpiresAt.After(start)) && (b.ExpiresAt == nil || b.ExpiresAt.After(start))
}
func IAMContentDigest(req m.Request) string {
	req.ContentDigest = ""
	req.BasePolicyRevision = 0
	req.EventID = ""
	req.RequestID = ""
	b, _ := jsonx.Marshal(req)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// Losing a deny can expose allows from a different assignment. Check that
// user's entire future maximum against one matching delegation, never against
// a ceiling assembled from unrelated delegation paths.
func (v iamManagementView) expandedUserCeiling(state IAMConstraintState, roleID, uid int64, action string) error {
	if v.root {
		return nil
	}
	i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == roleID })
	if i < 0 {
		return ErrIAMNotFound
	}
	for _, d := range v.delegations {
		if !d.Validity.Contains(v.now) || !slices.Contains(v.active, d.ManagerRoleID) || !slices.Contains(d.Actions, action) || !iamDelegationTarget(d, state.Roles[i]) || !authorization.AllowCovers([]authorization.Scope{d.TargetUserScope}, v.actor.UserID, authorization.ObjectFacts{Context: state.Context, OwnerUserID: uid}, true) {
			continue
		}
		covered := true
		for _, a := range state.Assignments {
			if a.UserID == uid && !a.Revoked && (a.Validity.ExpiresAt == nil || a.Validity.ExpiresAt.After(v.now)) && !iamCeilingCovers(d, state, a.RoleID, uid, v.actor.UserID, a.Boundary) {
				covered = false
				break
			}
		}
		if covered {
			return nil
		}
	}
	return ErrIAMProtected
}
func iamRoleHasDeny(state IAMConstraintState, id int64) bool {
	roles, err := iamStructuralRoles(state, id)
	if err != nil {
		return true
	}
	for _, r := range roles {
		if slices.ContainsFunc(r.Grants, func(g IAMGrant) bool { return g.Effect == authorization.Deny }) {
			return true
		}
	}
	return false
}

// Scoped list admission uses the same object check as every returned record.
// There is no presence-only allow check, and root still needs a bound, enabled
// operation. An empty authorized result remains possible after domain filters.
func (v iamManagementView) readAdmission(method, op string, req m.Request) error {
	if req.ID > 0 || req.UserID > 0 && method != "GetUserRoles" {
		id := req.ID
		if req.UserID > 0 && id == 0 {
			id = req.UserID
		}
		return v.permit(op, id, req.UserID)
	}
	switch method {
	case "ListRoles":
		for _, r := range v.state.Roles {
			if v.permit(op, r.ID, 0) == nil {
				return nil
			}
		}
	case "GetUserRoles", "ListRoleMembers":
		for _, a := range v.state.Assignments {
			if (req.UserID == 0 || a.UserID == req.UserID) && v.permit(op, a.RoleID, a.UserID) == nil {
				return nil
			}
		}
	case "ListResources", "ListPermissions":
		for _, p := range v.permissions {
			if v.permit(op, p.ID, 0) == nil {
				return nil
			}
		}
	case "ListDelegations":
		for _, d := range v.delegations {
			if v.permit(op, d.ID, 0) == nil {
				return nil
			}
		}
	case "ListMenuItems":
		for _, menu := range v.menus {
			if v.permit(op, menu.ID, 0) == nil {
				return nil
			}
		}
	case "ListRoleConstraints":
		for _, c := range v.state.Constraints {
			if v.permit(op, c.ID, 0) == nil {
				return nil
			}
		}
	case "ListAuthorizationAuditEvents", "ExportAuthorizationAuditEvents":
		for _, r := range v.state.Roles {
			if v.permit(op, r.ID, 0) == nil {
				return nil
			}
		}
	}
	return v.permit(op, 0, req.UserID)
}

// AuthorizeIAMChanges closes the A3 trusted-authorizer seam with authoritative
// identity, session, operation, delegation and whole-state impact checks.
func (uc *IAMGovernanceUsecase) AuthorizeIAMChanges(ctx context.Context, tx IAMTx, req IAMConstraintRequest, state IAMConstraintState) error {
	v, err := uc.view(ctx, tx, req.Actor, req.Context)
	if err != nil {
		return err
	}
	proposed, _, err := iamProposeChanges(state, req.Changes, v.now)
	if err != nil {
		return err
	}
	for _, change := range req.Changes {
		switch {
		case change.Role != nil:
			id := change.Role.ID
			i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == id })
			if i < 0 {
				return ErrIAMNotFound
			}
			old := state.Roles[i]
			actions := []string{}
			if !slices.Equal(old.Inherits, change.Role.Inherits) {
				actions = append(actions, "iam.role.hierarchy.update")
			}
			if old.Status != change.Role.Status {
				action := "iam.role.enable"
				if change.Role.Status == "disabled" {
					action = "iam.role.disable"
				}
				if change.Role.Status == "archived" {
					action = "iam.role.archive"
				}
				actions = append(actions, action)
			}
			if len(actions) == 0 {
				actions = append(actions, "iam.role.update")
			}
			for _, action := range actions {
				if _, err = v.roleChange(state, proposed, id, action); err != nil {
					return err
				}
			}
		case change.Assignment != nil:
			a := change.Assignment
			action := "identity.user_role.assign"
			if a.Revoked {
				action = "identity.user_role.revoke"
			}
			if err = v.permit(action, a.RoleID, a.UserID); err != nil {
				return err
			}
			interval := &a.Validity
			if a.Revoked {
				interval = nil
			}
			if err = v.manageRole(proposed, a.RoleID, action, a.UserID, interval, a.Boundary); err != nil {
				return err
			}
			if a.Revoked && iamRoleHasDeny(state, a.RoleID) {
				if err = v.expandedUserCeiling(proposed, a.RoleID, a.UserID, action); err != nil {
					return err
				}
			}
		case change.Activation != nil:
			if change.Activation.SessionID != v.actor.SessionID {
				return ErrIAMProtected
			}
		default:
			if !v.root {
				return ErrIAMProtected
			}
		}
	}
	return nil
}

func iamPermissionImpacts(before, after IAMConstraintState, users []int64, now time.Time, preview bool) ([]m.Impact, error) {
	sources := func(state IAMConstraintState, uid int64) ([]authorization.GrantSource, error) {
		roles := map[int64]IAMRole{}
		for _, r := range state.Roles {
			roles[r.ID] = r
		}
		closure, err := IAMRoleClosures(state.Context, state.Roles)
		if err != nil {
			return nil, err
		}
		assignments := []IAMAssignment{}
		active := map[int64]bool{}
		for _, a := range state.Assignments {
			if a.UserID == uid {
				assignments = append(assignments, a)
				if !a.Revoked && (a.Validity.ExpiresAt == nil || a.Validity.ExpiresAt.After(now)) {
					for _, id := range closure[a.RoleID] {
						active[id] = true
					}
				}
			}
		}
		return IAMSources(state.Context, roles, assignments, iamSortedIDs(active))
	}
	out := []m.Impact{}
	seen := map[int64]bool{}
	for _, uid := range users {
		if seen[uid] {
			continue
		}
		seen[uid] = true
		a, err := sources(before, uid)
		if err != nil {
			return nil, err
		}
		b, err := sources(after, uid)
		if err != nil {
			return nil, err
		}
		existing := map[int64]bool{}
		for _, assignment := range before.Assignments {
			existing[assignment.ID] = true
		}
		for i := range b {
			if preview && !existing[b[i].AssignmentID] {
				b[i].AssignmentID = 0
			}
		}
		out = append(out, m.Impact{UserID: uid, Before: a, After: b})
	}
	return out, nil
}

func (v iamManagementView) visibleRole(r IAMRole, action string) bool {
	if v.permit(action, r.ID, 0) != nil {
		return false
	}
	if v.root {
		return true
	}
	if action == "iam.role.list" {
		action = "iam.role.read"
	}
	return slices.ContainsFunc(v.delegations, func(d m.Delegation) bool {
		return d.Validity.Contains(v.now) && slices.Contains(v.active, d.ManagerRoleID) && iamDelegationTarget(d, r) && slices.Contains(d.Actions, action)
	})
}
func (v iamManagementView) visibleAssignment(state IAMConstraintState, a IAMAssignment, action string) bool {
	if v.permit(action, a.RoleID, a.UserID) != nil {
		return false
	}
	if v.root {
		return true
	}
	i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == a.RoleID })
	if i < 0 {
		return false
	}
	return slices.ContainsFunc(v.delegations, func(d m.Delegation) bool {
		return d.Validity.Contains(v.now) && slices.Contains(v.active, d.ManagerRoleID) && iamDelegationTarget(d, state.Roles[i]) && slices.Contains(d.Actions, action) && authorization.AllowCovers([]authorization.Scope{d.TargetUserScope}, v.actor.UserID, authorization.ObjectFacts{Context: state.Context, OwnerUserID: a.UserID}, true)
	})
}

// Impacts are an informational response, not additional read authority. A
// manager's valid change to one role must not reveal another assignment or
// an inherited role's hidden grants in the same target user's source list.
func (v iamManagementView) permissionImpacts(before, after IAMConstraintState, users []int64, preview bool) ([]m.Impact, error) {
	impacts, err := iamPermissionImpacts(before, after, users, v.now, false)
	if err != nil {
		return nil, err
	}
	visible := func(state IAMConstraintState, s authorization.GrantSource) bool {
		if v.root {
			return true
		}
		i := slices.IndexFunc(state.Assignments, func(a IAMAssignment) bool { return a.ID == s.AssignmentID })
		if i < 0 || !v.visibleAssignment(state, state.Assignments[i], "identity.user_role.read") {
			return false
		}
		for _, id := range s.InheritancePath {
			i := slices.IndexFunc(state.Roles, func(r IAMRole) bool { return r.ID == id })
			if i < 0 || !v.visibleRole(state.Roles[i], "iam.role.permissions.read") {
				return false
			}
		}
		return true
	}
	for i := range impacts {
		impacts[i].Before = slices.DeleteFunc(impacts[i].Before, func(s authorization.GrantSource) bool { return !visible(before, s) })
		impacts[i].After = slices.DeleteFunc(impacts[i].After, func(s authorization.GrantSource) bool { return !visible(after, s) })
		if preview {
			for j := range impacts[i].After {
				if !slices.ContainsFunc(before.Assignments, func(a IAMAssignment) bool { return a.ID == impacts[i].After[j].AssignmentID }) {
					impacts[i].After[j].AssignmentID = 0
				}
			}
		}
	}
	return impacts, nil
}
