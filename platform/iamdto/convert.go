// Package iamdto converts IAM transport messages to pure management objects.
package iamdto

import (
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	c "micro-one-api/api/common/v1"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"time"
)

func fromTime(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime().UTC().Truncate(time.Millisecond)
}
func toTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
func fromTimePtr(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	v := t.AsTime().UTC().Truncate(time.Millisecond)
	return &v
}
func toTimePtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return toTime(*t)
}

func AuthorizationContextFrom(p *c.AuthorizationContext) authorization.Context {
	var d authorization.Context
	if p == nil {
		return d
	}
	d.Type = p.ContextType
	d.OrganizationID = p.OrganizationId
	d.Key = p.ContextKey
	return d
}
func AuthorizationContextTo(d authorization.Context) *c.AuthorizationContext {
	if d.Type == "" && d.Key == "" && d.OrganizationID == 0 {
		return nil
	}
	p := &c.AuthorizationContext{}
	p.ContextType = d.Type
	p.OrganizationId = d.OrganizationID
	p.ContextKey = d.Key
	return p
}

func AuthorizationScopeFrom(p *c.AuthorizationScope) authorization.Scope {
	var d authorization.Scope
	if p == nil {
		return d
	}
	d.Clauses = make([]authorization.Clause, 0, len(p.Clauses))
	for _, item := range p.Clauses {
		d.Clauses = append(d.Clauses, AuthorizationScopeClauseFrom(item))
	}
	return d
}
func AuthorizationScopeTo(d authorization.Scope) *c.AuthorizationScope {
	p := &c.AuthorizationScope{}
	for _, item := range d.Clauses {
		p.Clauses = append(p.Clauses, AuthorizationScopeClauseTo(item))
	}
	return p
}

func AuthorizationScopeClauseFrom(p *c.AuthorizationScopeClause) authorization.Clause {
	var d authorization.Clause
	if p == nil {
		return d
	}
	d.All = p.All
	d.Self = p.Self
	d.UserIDs = p.UserIds
	d.ResourceIDs = p.ResourceIds
	d.RoutingGroupIDs = p.RoutingGroupIds
	return d
}
func AuthorizationScopeClauseTo(d authorization.Clause) *c.AuthorizationScopeClause {
	p := &c.AuthorizationScopeClause{}
	p.All = d.All
	p.Self = d.Self
	p.UserIds = d.UserIDs
	p.ResourceIds = d.ResourceIDs
	p.RoutingGroupIds = d.RoutingGroupIDs
	return p
}

func AuthorizationVersionsFrom(p *c.AuthorizationVersions) authorization.Versions {
	var d authorization.Versions
	if p == nil {
		return d
	}
	d.User = p.UserRevision
	d.Policy = p.PolicyRevision
	d.Catalog = p.CatalogRevision
	d.Session = p.SessionRevision
	d.SessionContext = p.SessionContextRevision
	d.Organization = p.OrganizationRevision
	d.Membership = p.MembershipRevision
	d.Unit = p.UnitRevision
	return d
}
func AuthorizationVersionsTo(d authorization.Versions) *c.AuthorizationVersions {
	p := &c.AuthorizationVersions{}
	p.UserRevision = d.User
	p.PolicyRevision = d.Policy
	p.CatalogRevision = d.Catalog
	p.SessionRevision = d.Session
	p.SessionContextRevision = d.SessionContext
	p.OrganizationRevision = d.Organization
	p.MembershipRevision = d.Membership
	p.UnitRevision = d.Unit
	return p
}

func AuthorizationActorFrom(p *c.AuthorizationActor) authorization.Actor {
	var d authorization.Actor
	if p == nil {
		return d
	}
	d.UserID = p.UserId
	d.ServiceID = p.ServiceId
	d.SessionID = p.SessionId
	d.PasswordEpoch = p.PasswordEpoch
	d.ExpiresAt = fromTime(p.ExpiresAt)
	return d
}
func AuthorizationActorTo(d authorization.Actor) *c.AuthorizationActor {
	p := &c.AuthorizationActor{}
	p.UserId = d.UserID
	p.ServiceId = d.ServiceID
	p.SessionId = d.SessionID
	p.PasswordEpoch = d.PasswordEpoch
	p.ExpiresAt = toTime(d.ExpiresAt)
	return p
}

func AuthorizationObjectFactsFrom(p *c.AuthorizationObjectFacts) authorization.ObjectFacts {
	var d authorization.ObjectFacts
	if p == nil {
		return d
	}
	d.Context = AuthorizationContextFrom(p.Context)
	d.ResourceID = p.ResourceId
	d.OwnerUserID = p.OwnerUserId
	d.RoutingGroupIDs = p.RoutingGroupIds
	return d
}
func AuthorizationObjectFactsTo(d authorization.ObjectFacts) *c.AuthorizationObjectFacts {
	p := &c.AuthorizationObjectFacts{}
	p.Context = AuthorizationContextTo(d.Context)
	p.ResourceId = d.ResourceID
	p.OwnerUserId = d.OwnerUserID
	p.RoutingGroupIds = d.RoutingGroupIDs
	return p
}

func AuthorizationIntervalFrom(p *c.AuthorizationInterval) authorization.Interval {
	var d authorization.Interval
	if p == nil {
		return d
	}
	d.StartsAt = fromTime(p.StartsAt)
	d.ExpiresAt = fromTimePtr(p.ExpiresAt)
	return d
}
func AuthorizationIntervalTo(d authorization.Interval) *c.AuthorizationInterval {
	p := &c.AuthorizationInterval{}
	p.StartsAt = toTime(d.StartsAt)
	p.ExpiresAt = toTimePtr(d.ExpiresAt)
	return p
}

func AuthorizationGrantSourceFrom(p *c.AuthorizationGrantSource) authorization.GrantSource {
	var d authorization.GrantSource
	if p == nil {
		return d
	}
	d.Context = AuthorizationContextFrom(p.Context)
	d.Operation = p.Operation
	d.Effect = effectFrom(p.Effect)
	d.AssignmentID = p.AssignmentId
	d.RoleID = p.RoleId
	d.InheritancePath = p.InheritancePath
	d.RoleScope = AuthorizationScopeFrom(p.RoleScope)
	d.AssignmentBoundary = AuthorizationScopeFrom(p.AssignmentBoundary)
	d.Validity = AuthorizationIntervalFrom(p.Validity)
	d.Active = p.Active
	return d
}
func AuthorizationGrantSourceTo(d authorization.GrantSource) *c.AuthorizationGrantSource {
	p := &c.AuthorizationGrantSource{}
	p.Context = AuthorizationContextTo(d.Context)
	p.Operation = d.Operation
	p.Effect = effectTo(d.Effect)
	p.AssignmentId = d.AssignmentID
	p.RoleId = d.RoleID
	p.InheritancePath = d.InheritancePath
	p.RoleScope = AuthorizationScopeTo(d.RoleScope)
	p.AssignmentBoundary = AuthorizationScopeTo(d.AssignmentBoundary)
	p.Validity = AuthorizationIntervalTo(d.Validity)
	p.Active = d.Active
	return p
}

func AuthorizationDecisionFrom(p *c.AuthorizationDecision) authorization.Decision {
	var d authorization.Decision
	if p == nil {
		return d
	}
	d.Allowed = p.Allowed
	d.Reason = p.Reason
	d.Context = AuthorizationContextFrom(p.Context)
	d.Operation = p.Operation
	d.Versions = AuthorizationVersionsFrom(p.Versions)
	d.ValidUntil = fromTimePtr(p.ValidUntil)
	d.Sources = make([]authorization.GrantSource, 0, len(p.Sources))
	for _, item := range p.Sources {
		d.Sources = append(d.Sources, AuthorizationGrantSourceFrom(item))
	}
	return d
}
func AuthorizationDecisionTo(d authorization.Decision) *c.AuthorizationDecision {
	p := &c.AuthorizationDecision{}
	p.Allowed = d.Allowed
	p.Reason = d.Reason
	p.Context = AuthorizationContextTo(d.Context)
	p.Operation = d.Operation
	p.Versions = AuthorizationVersionsTo(d.Versions)
	p.ValidUntil = toTimePtr(d.ValidUntil)
	for _, item := range d.Sources {
		p.Sources = append(p.Sources, AuthorizationGrantSourceTo(item))
	}
	return p
}

func IAMGrantFrom(p *v.IAMGrant) m.Grant {
	var d m.Grant
	if p == nil {
		return d
	}
	d.Operation = p.Operation
	d.Effect = authorization.Effect(p.Effect)
	d.Scope = AuthorizationScopeFrom(p.Scope)
	return d
}
func IAMGrantTo(d m.Grant) *v.IAMGrant {
	p := &v.IAMGrant{}
	p.Operation = d.Operation
	p.Effect = string(d.Effect)
	p.Scope = AuthorizationScopeTo(d.Scope)
	return p
}

func IAMRoleFrom(p *v.IAMRole) m.Role {
	var d m.Role
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.Context = AuthorizationContextFrom(p.Context)
	d.Code = p.Code
	d.Name = p.Name
	d.Description = p.Description
	d.Builtin = p.Builtin
	d.MaxMembers = p.MaxMembers
	d.Status = p.Status
	d.Revision = p.Revision
	d.CreationDelegationID = p.CreationDelegationId
	d.Inherits = p.Inherits
	d.Grants = make([]m.Grant, 0, len(p.Grants))
	for _, item := range p.Grants {
		d.Grants = append(d.Grants, IAMGrantFrom(item))
	}
	return d
}
func IAMRoleTo(d m.Role) *v.IAMRole {
	p := &v.IAMRole{}
	p.Id = d.ID
	p.Context = AuthorizationContextTo(d.Context)
	p.Code = d.Code
	p.Name = d.Name
	p.Description = d.Description
	p.Builtin = d.Builtin
	p.MaxMembers = d.MaxMembers
	p.Status = d.Status
	p.Revision = d.Revision
	p.CreationDelegationId = d.CreationDelegationID
	p.Inherits = d.Inherits
	for _, item := range d.Grants {
		p.Grants = append(p.Grants, IAMGrantTo(item))
	}
	return p
}

func IAMAssignmentFrom(p *v.IAMAssignment) m.Assignment {
	var d m.Assignment
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.UserID = p.UserId
	d.RoleID = p.RoleId
	d.Context = AuthorizationContextFrom(p.Context)
	d.Boundary = AuthorizationScopeFrom(p.Boundary)
	d.Validity = AuthorizationIntervalFrom(p.Validity)
	d.Revoked = p.Revoked
	d.Origin = p.Origin
	d.MigrationBatchID = p.MigrationBatchId
	d.Revision = p.Revision
	d.AssignedBy = p.AssignedBy
	return d
}
func IAMAssignmentTo(d m.Assignment) *v.IAMAssignment {
	p := &v.IAMAssignment{}
	p.Id = d.ID
	p.UserId = d.UserID
	p.RoleId = d.RoleID
	p.Context = AuthorizationContextTo(d.Context)
	p.Boundary = AuthorizationScopeTo(d.Boundary)
	p.Validity = AuthorizationIntervalTo(d.Validity)
	p.Revoked = d.Revoked
	p.Origin = d.Origin
	p.MigrationBatchId = d.MigrationBatchID
	p.Revision = d.Revision
	p.AssignedBy = d.AssignedBy
	return p
}

func IAMCeilingFrom(p *v.IAMCeiling) m.Ceiling {
	var d m.Ceiling
	if p == nil {
		return d
	}
	d.Context = AuthorizationContextFrom(p.Context)
	d.Operation = p.Operation
	d.Scope = AuthorizationScopeFrom(p.Scope)
	return d
}
func IAMCeilingTo(d m.Ceiling) *v.IAMCeiling {
	p := &v.IAMCeiling{}
	p.Context = AuthorizationContextTo(d.Context)
	p.Operation = d.Operation
	p.Scope = AuthorizationScopeTo(d.Scope)
	return p
}

func IAMDelegationFrom(p *v.IAMDelegation) m.Delegation {
	var d m.Delegation
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.ManagerRoleID = p.ManagerRoleId
	d.TargetRoleID = p.TargetRoleId
	d.Context = AuthorizationContextFrom(p.Context)
	d.TargetKind = p.TargetKind
	d.Actions = p.Actions
	d.TargetUserScope = AuthorizationScopeFrom(p.TargetUserScope)
	d.GrantCeiling = make([]m.Ceiling, 0, len(p.GrantCeiling))
	for _, item := range p.GrantCeiling {
		d.GrantCeiling = append(d.GrantCeiling, IAMCeilingFrom(item))
	}
	d.CanRedelegate = p.CanRedelegate
	d.Validity = AuthorizationIntervalFrom(p.Validity)
	d.Revision = p.Revision
	return d
}
func IAMDelegationTo(d m.Delegation) *v.IAMDelegation {
	p := &v.IAMDelegation{}
	p.Id = d.ID
	p.ManagerRoleId = d.ManagerRoleID
	p.TargetRoleId = d.TargetRoleID
	p.Context = AuthorizationContextTo(d.Context)
	p.TargetKind = d.TargetKind
	p.Actions = d.Actions
	p.TargetUserScope = AuthorizationScopeTo(d.TargetUserScope)
	for _, item := range d.GrantCeiling {
		p.GrantCeiling = append(p.GrantCeiling, IAMCeilingTo(item))
	}
	p.CanRedelegate = d.CanRedelegate
	p.Validity = AuthorizationIntervalTo(d.Validity)
	p.Revision = d.Revision
	return p
}

func IAMPermissionFrom(p *v.IAMPermission) m.Permission {
	var d m.Permission
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.ResourceID = p.ResourceId
	d.Code = p.Code
	d.Name = p.Name
	d.Category = p.Category
	d.RiskLevel = p.RiskLevel
	d.Status = p.Status
	d.Binding = p.Binding
	for _, scope := range p.SupportedScopes {
		d.SupportedScopes = append(d.SupportedScopes, authorization.ScopeKind(scope))
	}
	d.ContextTypes = p.ContextTypes
	d.Protected = p.Protected
	d.Revision = p.Revision
	return d
}
func IAMPermissionTo(d m.Permission) *v.IAMPermission {
	p := &v.IAMPermission{}
	p.Id = d.ID
	p.ResourceId = d.ResourceID
	p.Code = d.Code
	p.Name = d.Name
	p.Category = d.Category
	p.RiskLevel = d.RiskLevel
	p.Status = d.Status
	p.Binding = d.Binding
	for _, scope := range d.SupportedScopes {
		p.SupportedScopes = append(p.SupportedScopes, string(scope))
	}
	p.ContextTypes = d.ContextTypes
	p.Protected = d.Protected
	p.Revision = d.Revision
	return p
}

func IAMResourceFrom(p *v.IAMResource) m.Resource {
	var d m.Resource
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.Code = p.Code
	d.Name = p.Name
	d.Owner = p.Owner
	d.Enabled = p.Enabled
	return d
}
func IAMResourceTo(d m.Resource) *v.IAMResource {
	p := &v.IAMResource{}
	p.Id = d.ID
	p.Code = d.Code
	p.Name = d.Name
	p.Owner = d.Owner
	p.Enabled = d.Enabled
	return p
}

func IAMMenuFrom(p *v.IAMMenu) m.Menu {
	var d m.Menu
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.ParentID = p.ParentId
	d.RouteKey = p.RouteKey
	d.Name = p.Name
	d.IconKey = p.IconKey
	d.Sort = p.Sort
	d.Enabled = p.Enabled
	d.RequiredAll = p.RequiredAll
	d.RequiredAny = p.RequiredAny
	d.Revision = p.Revision
	return d
}
func IAMMenuTo(d m.Menu) *v.IAMMenu {
	p := &v.IAMMenu{}
	p.Id = d.ID
	p.ParentId = d.ParentID
	p.RouteKey = d.RouteKey
	p.Name = d.Name
	p.IconKey = d.IconKey
	p.Sort = d.Sort
	p.Enabled = d.Enabled
	p.RequiredAll = d.RequiredAll
	p.RequiredAny = d.RequiredAny
	p.Revision = d.Revision
	return p
}

func IAMConstraintFrom(p *v.IAMConstraint) m.Constraint {
	var d m.Constraint
	if p == nil {
		return d
	}
	d.ID = p.Id
	d.Context = AuthorizationContextFrom(p.Context)
	d.Kind = p.Kind
	d.Name = p.Name
	d.RoleIDs = p.RoleIds
	d.MaxCount = p.MaxCount
	d.Enabled = p.Enabled
	d.Revision = p.Revision
	return d
}
func IAMConstraintTo(d m.Constraint) *v.IAMConstraint {
	p := &v.IAMConstraint{}
	p.Id = d.ID
	p.Context = AuthorizationContextTo(d.Context)
	p.Kind = d.Kind
	p.Name = d.Name
	p.RoleIds = d.RoleIDs
	p.MaxCount = d.MaxCount
	p.Enabled = d.Enabled
	p.Revision = d.Revision
	return p
}

func IAMSessionFrom(p *v.IAMSession) m.Session {
	var d m.Session
	if p == nil {
		return d
	}
	d.SessionID = p.SessionId
	d.UserID = p.UserId
	d.Context = AuthorizationContextFrom(p.Context)
	d.ExpiresAt = fromTime(p.ExpiresAt)
	d.RevokedAt = fromTimePtr(p.RevokedAt)
	d.ContextRevokedAt = fromTimePtr(p.ContextRevokedAt)
	d.ActivationState = p.ActivationState
	d.ActiveRoleIDs = p.ActiveRoleIds
	d.SessionRevision = p.SessionRevision
	d.Revision = p.Revision
	return d
}
func IAMSessionTo(d m.Session) *v.IAMSession {
	p := &v.IAMSession{}
	p.SessionId = d.SessionID
	p.UserId = d.UserID
	p.Context = AuthorizationContextTo(d.Context)
	p.ExpiresAt = toTime(d.ExpiresAt)
	p.RevokedAt = toTimePtr(d.RevokedAt)
	p.ContextRevokedAt = toTimePtr(d.ContextRevokedAt)
	p.ActivationState = d.ActivationState
	p.ActiveRoleIds = d.ActiveRoleIDs
	p.SessionRevision = d.SessionRevision
	p.Revision = d.Revision
	return p
}

func IAMReferenceFrom(p *v.IAMReference) m.Reference {
	var d m.Reference
	if p == nil {
		return d
	}
	d.Kind = p.Kind
	d.ID = p.Id
	d.UserID = p.UserId
	return d
}
func IAMReferenceTo(d m.Reference) *v.IAMReference {
	p := &v.IAMReference{}
	p.Kind = d.Kind
	p.Id = d.ID
	p.UserId = d.UserID
	return p
}

func IAMConflictFrom(p *v.IAMConflict) m.Conflict {
	var d m.Conflict
	if p == nil {
		return d
	}
	d.ConstraintName = p.ConstraintName
	d.ProposedConstraint = p.ProposedConstraint
	d.Kind = p.Kind
	d.ConstraintID = p.ConstraintId
	d.RoleID = p.RoleId
	d.UserIDs = p.UserIds
	d.RoleIDs = p.RoleIds
	d.SessionIDs = p.SessionIds
	d.Actual = p.Actual
	d.Limit = p.Limit
	d.Validity = AuthorizationIntervalFrom(p.Validity)
	return d
}
func IAMConflictTo(d m.Conflict) *v.IAMConflict {
	p := &v.IAMConflict{}
	p.ConstraintName = d.ConstraintName
	p.ProposedConstraint = d.ProposedConstraint
	p.Kind = d.Kind
	p.ConstraintId = d.ConstraintID
	p.RoleId = d.RoleID
	p.UserIds = d.UserIDs
	p.RoleIds = d.RoleIDs
	p.SessionIds = d.SessionIDs
	p.Actual = d.Actual
	p.Limit = d.Limit
	p.Validity = AuthorizationIntervalTo(d.Validity)
	return p
}

func IAMAuditFrom(p *v.IAMAudit) m.Audit {
	var d m.Audit
	if p == nil {
		return d
	}
	d.EventID = p.EventId
	d.Actor = AuthorizationActorFrom(p.Actor)
	d.Context = AuthorizationContextFrom(p.Context)
	d.TargetContext = AuthorizationContextFrom(p.TargetContext)
	d.Action = p.Action
	d.Target = p.Target
	d.Before = p.Before
	d.After = p.After
	d.Diff = p.Diff
	d.Result = p.Result
	d.RequestID = p.RequestId
	d.Reason = p.Reason
	d.Versions = AuthorizationVersionsFrom(p.Versions)
	d.OccurredAt = fromTime(p.OccurredAt)
	return d
}
func IAMAuditTo(d m.Audit) *v.IAMAudit {
	p := &v.IAMAudit{}
	p.EventId = d.EventID
	p.Actor = AuthorizationActorTo(d.Actor)
	p.Context = AuthorizationContextTo(d.Context)
	p.TargetContext = AuthorizationContextTo(d.TargetContext)
	p.Action = d.Action
	p.Target = d.Target
	p.Before = d.Before
	p.After = d.After
	p.Diff = d.Diff
	p.Result = d.Result
	p.RequestId = d.RequestID
	p.Reason = d.Reason
	p.Versions = AuthorizationVersionsTo(d.Versions)
	p.OccurredAt = toTime(d.OccurredAt)
	return p
}

func IAMRequestFrom(p *v.IAMRequest) m.Request {
	var d m.Request
	if p == nil {
		return d
	}
	d.Context = AuthorizationContextFrom(p.Context)
	d.ID = p.Id
	d.UserID = p.UserId
	d.SourceID = p.SourceId
	d.EventID = p.EventId
	if p.Role != nil {
		value := IAMRoleFrom(p.Role)
		d.Role = &value
	}
	if p.Permission != nil {
		value := IAMPermissionFrom(p.Permission)
		d.Permission = &value
	}
	if p.Delegation != nil {
		value := IAMDelegationFrom(p.Delegation)
		d.Delegation = &value
	}
	if p.Constraint != nil {
		value := IAMConstraintFrom(p.Constraint)
		d.Constraint = &value
	}
	if p.Menu != nil {
		value := IAMMenuFrom(p.Menu)
		d.Menu = &value
	}
	if p.Assignment != nil {
		value := IAMAssignmentFrom(p.Assignment)
		d.Assignment = &value
	}
	d.Assignments = make([]m.Assignment, 0, len(p.Assignments))
	for _, item := range p.Assignments {
		d.Assignments = append(d.Assignments, IAMAssignmentFrom(item))
	}
	d.Grants = make([]m.Grant, 0, len(p.Grants))
	for _, item := range p.Grants {
		d.Grants = append(d.Grants, IAMGrantFrom(item))
	}
	d.RoleIDs = p.RoleIds
	d.ExpectedRevision = p.ExpectedRevision
	d.ExpectedPolicyRevision = p.ExpectedPolicyRevision
	d.BasePolicyRevision = p.BasePolicyRevision
	d.ContentDigest = p.ContentDigest
	d.Reason = p.Reason
	d.RequestID = p.RequestId
	d.Filter = p.Filter
	d.OrderBy = p.OrderBy
	d.PageToken = p.PageToken
	d.PageSize = p.PageSize
	d.UpdateMask = p.GetUpdateMask().GetPaths()
	d.Operation = p.Operation
	d.Object = AuthorizationObjectFactsFrom(p.Object)
	return d
}
func IAMRequestTo(d m.Request) *v.IAMRequest {
	p := &v.IAMRequest{}
	p.Context = AuthorizationContextTo(d.Context)
	p.Id = d.ID
	p.UserId = d.UserID
	p.SourceId = d.SourceID
	p.EventId = d.EventID
	if d.Role != nil {
		p.Role = IAMRoleTo(*d.Role)
	}
	if d.Permission != nil {
		p.Permission = IAMPermissionTo(*d.Permission)
	}
	if d.Delegation != nil {
		p.Delegation = IAMDelegationTo(*d.Delegation)
	}
	if d.Constraint != nil {
		p.Constraint = IAMConstraintTo(*d.Constraint)
	}
	if d.Menu != nil {
		p.Menu = IAMMenuTo(*d.Menu)
	}
	if d.Assignment != nil {
		p.Assignment = IAMAssignmentTo(*d.Assignment)
	}
	for _, item := range d.Assignments {
		p.Assignments = append(p.Assignments, IAMAssignmentTo(item))
	}
	for _, item := range d.Grants {
		p.Grants = append(p.Grants, IAMGrantTo(item))
	}
	p.RoleIds = d.RoleIDs
	p.ExpectedRevision = d.ExpectedRevision
	p.ExpectedPolicyRevision = d.ExpectedPolicyRevision
	p.BasePolicyRevision = d.BasePolicyRevision
	p.ContentDigest = d.ContentDigest
	p.Reason = d.Reason
	p.RequestId = d.RequestID
	p.Filter = d.Filter
	p.OrderBy = d.OrderBy
	p.PageToken = d.PageToken
	p.PageSize = d.PageSize
	p.UpdateMask = &fieldmaskpb.FieldMask{Paths: d.UpdateMask}
	p.Operation = d.Operation
	if d.Object.Context.Key != "" || d.Object.ResourceID != 0 || d.Object.OwnerUserID != 0 || len(d.Object.RoutingGroupIDs) != 0 {
		p.Object = AuthorizationObjectFactsTo(d.Object)
	}
	return p
}

func IAMReplyFrom(p *v.IAMReply) m.Response {
	var d m.Response
	if p == nil {
		return d
	}
	for _, session := range p.Sessions {
		d.Sessions = append(d.Sessions, IAMSessionFrom(session))
	}
	d.Impacts = make([]m.Impact, 0, len(p.Impacts))
	for _, item := range p.Impacts {
		d.Impacts = append(d.Impacts, IAMImpactFrom(item))
	}
	d.Roles = make([]m.Role, 0, len(p.Roles))
	for _, item := range p.Roles {
		d.Roles = append(d.Roles, IAMRoleFrom(item))
	}
	d.Permissions = make([]m.Permission, 0, len(p.Permissions))
	for _, item := range p.Permissions {
		d.Permissions = append(d.Permissions, IAMPermissionFrom(item))
	}
	d.Resources = make([]m.Resource, 0, len(p.Resources))
	for _, item := range p.Resources {
		d.Resources = append(d.Resources, IAMResourceFrom(item))
	}
	d.Delegations = make([]m.Delegation, 0, len(p.Delegations))
	for _, item := range p.Delegations {
		d.Delegations = append(d.Delegations, IAMDelegationFrom(item))
	}
	d.Constraints = make([]m.Constraint, 0, len(p.Constraints))
	for _, item := range p.Constraints {
		d.Constraints = append(d.Constraints, IAMConstraintFrom(item))
	}
	d.Menus = make([]m.Menu, 0, len(p.Menus))
	for _, item := range p.Menus {
		d.Menus = append(d.Menus, IAMMenuFrom(item))
	}
	d.Assignments = make([]m.Assignment, 0, len(p.Assignments))
	for _, item := range p.Assignments {
		d.Assignments = append(d.Assignments, IAMAssignmentFrom(item))
	}
	d.Audits = make([]m.Audit, 0, len(p.Audits))
	for _, item := range p.Audits {
		d.Audits = append(d.Audits, IAMAuditFrom(item))
	}
	d.References = make([]m.Reference, 0, len(p.References))
	for _, item := range p.References {
		d.References = append(d.References, IAMReferenceFrom(item))
	}
	d.Conflicts = make([]m.Conflict, 0, len(p.Conflicts))
	for _, item := range p.Conflicts {
		d.Conflicts = append(d.Conflicts, IAMConflictFrom(item))
	}
	d.Sources = make([]authorization.GrantSource, 0, len(p.Sources))
	for _, item := range p.Sources {
		d.Sources = append(d.Sources, AuthorizationGrantSourceFrom(item))
	}
	if p.Decision != nil {
		value := AuthorizationDecisionFrom(p.Decision)
		d.Decision = &value
	}
	d.Versions = AuthorizationVersionsFrom(p.Versions)
	if p.Session != nil {
		value := IAMSessionFrom(p.Session)
		d.Session = &value
	}
	d.ValidUntil = fromTimePtr(p.ValidUntil)
	d.AuthorizedRoleIDs = p.AuthorizedRoleIds
	d.ActiveRoleIDs = p.ActiveRoleIds
	d.AffectedUserIDs = p.AffectedUserIds
	d.BasePolicyRevision = p.BasePolicyRevision
	d.ContentDigest = p.ContentDigest
	d.NextPageToken = p.NextPageToken
	d.Total = p.Total
	return d
}
func IAMReplyTo(d m.Response) *v.IAMReply {
	p := &v.IAMReply{}
	for _, session := range d.Sessions {
		p.Sessions = append(p.Sessions, IAMSessionTo(session))
	}
	for _, item := range d.Impacts {
		p.Impacts = append(p.Impacts, IAMImpactTo(item))
	}
	for _, item := range d.Roles {
		p.Roles = append(p.Roles, IAMRoleTo(item))
	}
	for _, item := range d.Permissions {
		p.Permissions = append(p.Permissions, IAMPermissionTo(item))
	}
	for _, item := range d.Resources {
		p.Resources = append(p.Resources, IAMResourceTo(item))
	}
	for _, item := range d.Delegations {
		p.Delegations = append(p.Delegations, IAMDelegationTo(item))
	}
	for _, item := range d.Constraints {
		p.Constraints = append(p.Constraints, IAMConstraintTo(item))
	}
	for _, item := range d.Menus {
		p.Menus = append(p.Menus, IAMMenuTo(item))
	}
	for _, item := range d.Assignments {
		p.Assignments = append(p.Assignments, IAMAssignmentTo(item))
	}
	for _, item := range d.Audits {
		p.Audits = append(p.Audits, IAMAuditTo(item))
	}
	for _, item := range d.References {
		p.References = append(p.References, IAMReferenceTo(item))
	}
	for _, item := range d.Conflicts {
		p.Conflicts = append(p.Conflicts, IAMConflictTo(item))
	}
	for _, item := range d.Sources {
		p.Sources = append(p.Sources, AuthorizationGrantSourceTo(item))
	}
	if d.Decision != nil {
		p.Decision = AuthorizationDecisionTo(*d.Decision)
	}
	p.Versions = AuthorizationVersionsTo(d.Versions)
	if d.Session != nil {
		p.Session = IAMSessionTo(*d.Session)
	}
	p.ValidUntil = toTimePtr(d.ValidUntil)
	p.AuthorizedRoleIds = d.AuthorizedRoleIDs
	p.ActiveRoleIds = d.ActiveRoleIDs
	p.AffectedUserIds = d.AffectedUserIDs
	p.BasePolicyRevision = d.BasePolicyRevision
	p.ContentDigest = d.ContentDigest
	p.NextPageToken = d.NextPageToken
	p.Total = d.Total
	return p
}

func IAMImpactFrom(p *v.IAMImpact) m.Impact {
	var d m.Impact
	if p == nil {
		return d
	}
	d.UserID = p.UserId
	d.Before = make([]authorization.GrantSource, 0, len(p.Before))
	for _, item := range p.Before {
		d.Before = append(d.Before, AuthorizationGrantSourceFrom(item))
	}
	d.After = make([]authorization.GrantSource, 0, len(p.After))
	for _, item := range p.After {
		d.After = append(d.After, AuthorizationGrantSourceFrom(item))
	}
	return d
}
func IAMImpactTo(d m.Impact) *v.IAMImpact {
	p := &v.IAMImpact{}
	p.UserId = d.UserID
	for _, item := range d.Before {
		p.Before = append(p.Before, AuthorizationGrantSourceTo(item))
	}
	for _, item := range d.After {
		p.After = append(p.After, AuthorizationGrantSourceTo(item))
	}
	return p
}

func effectFrom(e c.AuthorizationEffect) authorization.Effect {
	switch e {
	case c.AuthorizationEffect_AUTHORIZATION_EFFECT_ALLOW:
		return authorization.Allow
	case c.AuthorizationEffect_AUTHORIZATION_EFFECT_DENY:
		return authorization.Deny
	default:
		return ""
	}
}
func effectTo(e authorization.Effect) c.AuthorizationEffect {
	switch e {
	case authorization.Allow:
		return c.AuthorizationEffect_AUTHORIZATION_EFFECT_ALLOW
	case authorization.Deny:
		return c.AuthorizationEffect_AUTHORIZATION_EFFECT_DENY
	default:
		return c.AuthorizationEffect_AUTHORIZATION_EFFECT_UNSPECIFIED
	}
}
