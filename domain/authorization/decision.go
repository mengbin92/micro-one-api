package authorization

import (
	"slices"
	"time"
)

// Input contains only verified server facts. Constraints and business rules
// remain owning-domain checks, and must explicitly pass (zero value denies).
type Input struct {
	Actor             Actor
	Context           Context
	Operation         Operation
	Object            ObjectFacts
	Sources           []GrantSource
	Versions          Versions
	Now               time.Time
	IdentityValid     bool
	SessionValid      bool
	ConstraintsPass   bool
	BusinessRulesPass bool
	OperationEnabled  bool
}

func Decide(in Input) Decision {
	d := Decision{Reason: "IDENTITY_INVALID", Context: in.Context, Operation: in.Operation.Code, Versions: in.Versions}
	if !in.IdentityValid || in.Actor.UserID <= 0 || in.Actor.SessionID == "" || in.Now.IsZero() || !in.Now.Before(in.Actor.ExpiresAt) {
		return d
	}
	d.Reason = "OPERATION_UNBOUND"
	op, registered := Lookup(in.Operation.Code)
	if !registered || in.Operation.Binding != "bound" {
		return d
	}
	// Scope/context/whole-object rules always come from the fixed catalog.
	// Binding is supplied only by a code-owned, verified execution point.
	in.Operation = op
	d.Reason = "OPERATION_DISABLED"
	if !in.OperationEnabled {
		return d
	}
	d.Reason = "CONTEXT_INVALID"
	if err := in.Context.RequirePlatform(); err != nil {
		if err == ErrOrganizationDisabled {
			d.Reason = "ORGANIZATION_DISABLED"
		}
		return d
	}
	if !slices.Contains(in.Operation.ContextTypes, in.Context.Type) {
		return d
	}
	if in.Object.Context != in.Context {
		return d
	}
	d.Reason = "SCOPE_INVALID"
	if in.Object.ResourceID < 0 || in.Object.OwnerUserID < 0 {
		return d
	}
	for _, id := range in.Object.RoutingGroupIDs {
		if id <= 0 {
			return d
		}
	}
	d.Reason = "SESSION_INVALID"
	if !in.SessionValid {
		return d
	}
	d.Reason = "CONSTRAINT_VIOLATION"
	if !in.ConstraintsPass {
		return d
	}
	allows, denies := []Scope{}, []Scope{}
	until := in.Actor.ExpiresAt
	for _, s := range in.Sources {
		if s.Context != in.Context || s.Operation != in.Operation.Code {
			continue
		}
		d.Reason = "SCOPE_INVALID"
		if s.Validity.Validate() != nil || s.RoleScope.Validate(in.Operation.Scopes) != nil || s.AssignmentBoundary.Validate(in.Operation.Scopes) != nil || (s.Effect != Allow && s.Effect != Deny) {
			return d
		}
		// A future deny/allow start also invalidates a reused snapshot.
		if in.Now.Before(s.Validity.StartsAt) && s.Validity.StartsAt.Before(until) {
			until = s.Validity.StartsAt
		}
		if !s.Validity.Contains(in.Now) {
			continue
		}
		if s.Validity.ExpiresAt != nil && s.Validity.ExpiresAt.Before(until) {
			until = *s.Validity.ExpiresAt
		}
		if s.Effect == Deny {
			denies = append(denies, s.RoleScope)
			d.Sources = append(d.Sources, s)
		} else if s.Active {
			allows = append(allows, Intersect(s.RoleScope, s.AssignmentBoundary))
			d.Sources = append(d.Sources, s)
		}
	}
	d.ValidUntil = &until
	d.Reason = "ALLOW_MISSING"
	if !AllowCovers(allows, in.Actor.UserID, in.Object, in.Operation.WholeObject) {
		return d
	}
	d.Reason = "DENY_MATCHED"
	if DenyMatches(denies, in.Actor.UserID, in.Object) {
		return d
	}
	d.Reason = "BUSINESS_RULE_VIOLATION"
	if !in.BusinessRulesPass {
		return d
	}
	d.Allowed, d.Reason = true, "ALLOWED"
	return d
}
