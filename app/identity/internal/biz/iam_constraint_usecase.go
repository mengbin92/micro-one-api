package biz

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

type IAMRoleTopology struct {
	ID         int64
	Status     string
	MaxMembers *int64
	Inherits   []int64
}
type IAMActivationChange struct {
	SessionID string
	RoleIDs   []int64
}

// Exactly one payload per change. ExpectedRevision is the target revision;
// zero is only legal for creating a constraint/assignment or changing limits.
type IAMConstraintChange struct {
	ExpectedRevision uint64
	Role             *IAMRoleTopology
	Assignment       *IAMAssignment
	Constraint       *IAMRoleConstraint
	DeleteConstraint *int64
	Limits           *IAMLimits
	Activation       *IAMActivationChange
}
type IAMConstraintRequest struct {
	Context                    authorization.Context
	Actor                      authorization.Actor
	ExpectedPolicyRevision     uint64
	Changes                    []IAMConstraintChange
	EventID, RequestID, Reason string
}
type IAMConstraintResult struct {
	PolicyRevision uint64
	Conflicts      []IAMConstraintConflict
}
type IAMConstraintRepo interface {
	IAMRepo
	ConstraintState(context.Context, IAMTx, authorization.Context) (IAMConstraintState, error)
	SaveRoleTopology(context.Context, IAMTx, authorization.Context, IAMRoleTopology, uint64) error
	SaveConstraint(context.Context, IAMTx, IAMRoleConstraint, uint64) error
	DeleteConstraint(context.Context, IAMTx, authorization.Context, int64, uint64) error
	SaveLimits(context.Context, IAMTx, IAMLimits) error
	SaveActivation(context.Context, IAMTx, authorization.Context, IAMActivationChange, uint64) error
}

// The trusted caller must verify identity and every changed governance action,
// delegation ceiling/source, target scope and protected-account invariant in
// this same transaction. A3 intentionally supplies no permissive implementation
// or server wiring; A4/A5 implement this seam. Nil always refuses execution.
type IAMChangeAuthorizer interface {
	AuthorizeIAMChanges(context.Context, IAMTx, IAMConstraintRequest, IAMConstraintState) error
}
type IAMConstraintsUsecase struct {
	repo       IAMConstraintRepo
	runner     IAMTxRunner
	authorizer IAMChangeAuthorizer
	now        func() time.Time
}

func NewIAMConstraintsUsecase(repo IAMConstraintRepo, runner IAMTxRunner, authorizer IAMChangeAuthorizer) *IAMConstraintsUsecase {
	return &IAMConstraintsUsecase{repo: repo, runner: runner, authorizer: authorizer, now: time.Now}
}
func (uc *IAMConstraintsUsecase) prepare(ctx context.Context, tx IAMTx, req IAMConstraintRequest) (IAMConstraintState, authorization.PolicyState, error) {
	if req.Context.RequirePlatform() != nil {
		return IAMConstraintState{}, authorization.PolicyState{}, ErrIAMContextInvalid
	}
	if req.ExpectedPolicyRevision == 0 || len(req.Changes) == 0 || strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.EventID) == "" || strings.TrimSpace(req.RequestID) == "" {
		return IAMConstraintState{}, authorization.PolicyState{}, ErrIAMInvalidRelation
	}
	policy, err := uc.repo.Policy(ctx, tx)
	if err != nil {
		return IAMConstraintState{}, policy, err
	}
	if policy.CheckWrite(authorization.IAMManagementWrite, false) != nil {
		return IAMConstraintState{}, policy, ErrIAMCutoverBlocked
	}
	if policy.PolicyRevision != req.ExpectedPolicyRevision {
		return IAMConstraintState{}, policy, ErrIAMRevisionConflict
	}
	state, err := uc.repo.ConstraintState(ctx, tx, req.Context)
	if err != nil {
		return state, policy, err
	}
	if uc.authorizer == nil {
		return state, policy, ErrIAMProtected
	}
	if err := uc.authorizer.AuthorizeIAMChanges(ctx, tx, req, state); err != nil {
		return state, policy, err
	}
	return state, policy, nil
}
func (uc *IAMConstraintsUsecase) Preview(ctx context.Context, req IAMConstraintRequest) (IAMConstraintResult, error) {
	result := IAMConstraintResult{}
	if uc.repo == nil || uc.runner == nil {
		return result, ErrIAMDependencyUnavailable
	}
	err := uc.runner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		state, policy, err := uc.prepare(ctx, tx, req)
		if err != nil {
			return err
		}
		now := uc.now().UTC().Truncate(time.Millisecond)
		proposed, _, err := iamProposeChanges(state, req.Changes, now)
		if err != nil {
			return err
		}
		conflicts, err := IAMCheckConstraints(now, proposed)
		iamNormalizeProposedConflicts(state, conflicts)
		if err != nil {
			return err
		}
		result = IAMConstraintResult{PolicyRevision: policy.PolicyRevision, Conflicts: conflicts}
		return nil
	})
	if err != nil {
		return IAMConstraintResult{}, err
	}
	return result, nil
}
func (uc *IAMConstraintsUsecase) Apply(ctx context.Context, req IAMConstraintRequest) (IAMConstraintResult, error) {
	result := IAMConstraintResult{}
	if uc.repo == nil || uc.runner == nil {
		return result, ErrIAMDependencyUnavailable
	}
	authorized := false
	var attemptPolicy authorization.PolicyState
	err := uc.runner.RunIAMWrite(ctx, func(ctx context.Context, tx IAMTx) error {
		authorized = false
		result = IAMConstraintResult{} // failed/replayed attempts must not leak a result
		state, policy, err := uc.prepare(ctx, tx, req)
		if err != nil {
			return err
		}
		authorized = true
		attemptPolicy = policy
		now := uc.now().UTC().Truncate(time.Millisecond)
		proposed, changes, err := iamProposeChanges(state, req.Changes, now)
		if err != nil {
			return err
		}
		conflicts, err := IAMCheckConstraints(now, proposed)
		iamNormalizeProposedConflicts(state, conflicts)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return &IAMConstraintViolation{Conflicts: conflicts}
		}
		for _, change := range changes {
			switch {
			case change.Role != nil:
				err = uc.repo.SaveRoleTopology(ctx, tx, req.Context, *change.Role, change.ExpectedRevision)
			case change.Assignment != nil:
				_, err = uc.repo.SaveAssignment(ctx, tx, *change.Assignment, change.ExpectedRevision)
			case change.Constraint != nil:
				err = uc.repo.SaveConstraint(ctx, tx, *change.Constraint, change.ExpectedRevision)
			case change.DeleteConstraint != nil:
				err = uc.repo.DeleteConstraint(ctx, tx, req.Context, *change.DeleteConstraint, change.ExpectedRevision)
			case change.Limits != nil:
				err = uc.repo.SaveLimits(ctx, tx, *change.Limits)
			case change.Activation != nil:
				err = uc.repo.SaveActivation(ctx, tx, req.Context, *change.Activation, change.ExpectedRevision)
			}
			if err != nil {
				return err
			}
		}
		// Every successful batch changes the policy revision, including assignments
		// and activations. This binds a later preview to exactly the full state read.
		// Target user/session revisions are also advanced by their own repositories.
		latest, err := uc.repo.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if err := uc.repo.AdvancePolicy(ctx, tx, latest.PolicyRevision, false); err != nil {
			return err
		}
		before, err := jsonx.Marshal(state)
		if err != nil {
			return ErrIAMInvalidRelation
		}
		persisted, err := uc.repo.ConstraintState(ctx, tx, req.Context)
		if err != nil {
			return err
		}
		after, err := jsonx.Marshal(persisted)
		if err != nil {
			return ErrIAMInvalidRelation
		}
		diff, err := jsonx.Marshal(changes)
		if err != nil {
			return ErrIAMInvalidRelation
		}
		event := IAMAuditEvent{EventID: req.EventID, Actor: req.Actor, Context: req.Context, TargetContext: req.Context, Action: "iam.constraint.batch", Target: req.Context.Key, Before: string(before), After: string(after), Diff: string(diff), Result: "success", Versions: authorization.Versions{Policy: policy.PolicyRevision, Catalog: policy.CatalogRevision}, RequestID: req.RequestID, Reason: req.Reason, OccurredAt: now}
		if err := uc.repo.AppendAudit(ctx, tx, event); err != nil {
			return err
		}
		latest, err = uc.repo.Policy(ctx, tx)
		if err != nil {
			return err
		}
		result = IAMConstraintResult{PolicyRevision: latest.PolicyRevision}
		return nil
	})
	if err != nil {
		if authorized {
			detail := struct {
				Reason    string
				Conflicts []IAMConstraintConflict
			}{Reason: err.Error()}
			var violation *IAMConstraintViolation
			if errors.As(err, &violation) {
				detail.Conflicts = violation.Conflicts
			}
			diff, marshalErr := jsonx.Marshal(detail)
			if marshalErr != nil {
				return IAMConstraintResult{}, errors.Join(err, ErrIAMInvalidRelation)
			}
			event := IAMAuditEvent{EventID: req.EventID, Actor: req.Actor, Context: req.Context, TargetContext: req.Context, Action: "iam.constraint.batch", Target: req.Context.Key, Before: "{}", After: "{}", Diff: string(diff), Result: "failure", Versions: authorization.Versions{Policy: attemptPolicy.PolicyRevision, Catalog: attemptPolicy.CatalogRevision}, RequestID: req.RequestID, Reason: req.Reason, OccurredAt: uc.now().UTC().Truncate(time.Millisecond)}
			if auditErr := uc.repo.AppendFailureAudit(ctx, event); auditErr != nil {
				return IAMConstraintResult{}, errors.Join(err, auditErr)
			}
		}
		return IAMConstraintResult{}, err
	}
	return result, nil
}

func iamProposeChanges(state IAMConstraintState, changes []IAMConstraintChange, now time.Time) (IAMConstraintState, []IAMConstraintChange, error) {
	// Clone every mutable container: simulation and retries never modify request
	// payloads, the authoritative snapshot, or caller-owned scope/role slices.
	proposed := state
	proposed.Roles = slices.Clone(state.Roles)
	proposed.Assignments = slices.Clone(state.Assignments)
	proposed.Constraints = slices.Clone(state.Constraints)
	proposed.Sessions = slices.Clone(state.Sessions)
	normalized := make([]IAMConstraintChange, 0, len(changes))
	touched := map[string]bool{}
	nextAssignment, nextConstraint := int64(1), int64(1)
	for _, a := range state.Assignments {
		if a.ID >= nextAssignment {
			nextAssignment = a.ID + 1
		}
	}
	for _, c := range state.Constraints {
		if c.ID >= nextConstraint {
			nextConstraint = c.ID + 1
		}
	}
	for _, ch := range changes {
		count := 0
		for _, present := range []bool{ch.Role != nil, ch.Assignment != nil, ch.Constraint != nil, ch.DeleteConstraint != nil, ch.Limits != nil, ch.Activation != nil} {
			if present {
				count++
			}
		}
		if count != 1 {
			return state, nil, ErrIAMInvalidRelation
		}
		var key string
		switch {
		case ch.Role != nil:
			r := *ch.Role
			r.Inherits = slices.Clone(r.Inherits)
			ch.Role = &r
			key = "role:" + iamID(r.ID)
			i := slices.IndexFunc(proposed.Roles, func(role IAMRole) bool { return role.ID == r.ID })
			if i < 0 {
				return state, nil, ErrIAMNotFound
			}
			current := proposed.Roles[i]
			if current.Code == "root" {
				return state, nil, ErrIAMProtected
			}
			if ch.ExpectedRevision == 0 || current.Revision != ch.ExpectedRevision {
				return state, nil, ErrIAMRevisionConflict
			}
			for _, id := range r.Inherits {
				for _, child := range state.Roles {
					if child.ID == id && child.Code == "root" {
						return state, nil, ErrIAMProtected
					}
				}
			}
			current.Status, current.MaxMembers, current.Inherits = r.Status, r.MaxMembers, r.Inherits
			current.Revision++
			proposed.Roles[i] = current
		case ch.Assignment != nil:
			a := *ch.Assignment
			if a.Context != state.Context {
				return state, nil, ErrIAMContextInvalid
			}
			if a.Validity.StartsAt.IsZero() {
				a.Validity.StartsAt = now
			} else {
				a.Validity.StartsAt = a.Validity.StartsAt.UTC().Truncate(time.Millisecond)
			}
			if a.Validity.ExpiresAt != nil {
				end := a.Validity.ExpiresAt.UTC().Truncate(time.Millisecond)
				a.Validity.ExpiresAt = &end
			}
			if a.Validity.Validate() != nil || a.Validity.StartsAt.UnixMilli() <= 0 || a.Boundary.Clauses == nil || a.Boundary.Validate([]authorization.ScopeKind{authorization.All, authorization.Self, authorization.Users, authorization.Resources, authorization.Groups}) != nil || authorization.ValidateOrigin(a.Origin, a.MigrationBatchID) != nil {
				return state, nil, ErrIAMInvalidRelation
			}
			key = "assignment:" + iamID(a.UserID) + ":" + iamID(a.RoleID)
			for _, role := range state.Roles {
				if role.ID == a.RoleID && role.Code == "root" {
					return state, nil, ErrIAMProtected
				}
			}
			if a.ID == 0 {
				if ch.ExpectedRevision != 0 || a.Origin != "explicit" {
					return state, nil, ErrIAMInvalidRelation
				}
				candidate := a
				candidate.ID = nextAssignment
				candidate.Revision = 1
				nextAssignment++
				proposed.Assignments = append(proposed.Assignments, candidate)
			} else {
				i := slices.IndexFunc(proposed.Assignments, func(other IAMAssignment) bool { return other.ID == a.ID })
				if i < 0 {
					return state, nil, ErrIAMNotFound
				}
				current := proposed.Assignments[i]
				if current.UserID != a.UserID || current.RoleID != a.RoleID || current.Origin != a.Origin || current.MigrationBatchID != a.MigrationBatchID {
					return state, nil, ErrIAMInvalidRelation
				}
				if ch.ExpectedRevision == 0 || current.Revision != ch.ExpectedRevision {
					return state, nil, ErrIAMRevisionConflict
				}
				a.Revision = current.Revision + 1
				proposed.Assignments[i] = a
			}
			ch.Assignment = &a
		case ch.Constraint != nil:
			c := *ch.Constraint
			c.RoleIDs = slices.Clone(c.RoleIDs)
			ch.Constraint = &c
			if c.Context != state.Context {
				return state, nil, ErrIAMContextInvalid
			}
			for _, id := range c.RoleIDs {
				for _, role := range state.Roles {
					if role.ID == id && role.Code == "root" {
						return state, nil, ErrIAMProtected
					}
				}
			}
			if c.ID == 0 {
				if ch.ExpectedRevision != 0 {
					return state, nil, ErrIAMRevisionConflict
				}
				key = "new_constraint:" + iamID(nextConstraint)
				candidate := c
				candidate.ID = nextConstraint
				candidate.Revision = 1
				nextConstraint++
				proposed.Constraints = append(proposed.Constraints, candidate)
			} else {
				key = "constraint:" + iamID(c.ID)
				i := slices.IndexFunc(proposed.Constraints, func(other IAMRoleConstraint) bool { return other.ID == c.ID })
				if i < 0 {
					return state, nil, ErrIAMNotFound
				}
				if ch.ExpectedRevision == 0 || proposed.Constraints[i].Revision != ch.ExpectedRevision {
					return state, nil, ErrIAMRevisionConflict
				}
				c.Revision = ch.ExpectedRevision + 1
				proposed.Constraints[i] = c
			}
		case ch.DeleteConstraint != nil:
			key = "constraint:" + iamID(*ch.DeleteConstraint)
			i := slices.IndexFunc(proposed.Constraints, func(c IAMRoleConstraint) bool { return c.ID == *ch.DeleteConstraint })
			if i < 0 {
				return state, nil, ErrIAMNotFound
			}
			if ch.ExpectedRevision == 0 || proposed.Constraints[i].Revision != ch.ExpectedRevision {
				return state, nil, ErrIAMRevisionConflict
			}
			proposed.Constraints = append(proposed.Constraints[:i:i], proposed.Constraints[i+1:]...)
		case ch.Limits != nil:
			key = "limits"
			if ch.ExpectedRevision != 0 {
				return state, nil, ErrIAMInvalidRelation
			}
			limits := *ch.Limits
			ch.Limits = &limits
			proposed.Limits = limits
		case ch.Activation != nil:
			a := *ch.Activation
			a.RoleIDs = slices.Clone(a.RoleIDs)
			ch.Activation = &a
			key = "activation:" + a.SessionID
			i := slices.IndexFunc(proposed.Sessions, func(s IAMSessionContext) bool { return s.SessionID == a.SessionID })
			if i < 0 {
				return state, nil, ErrIAMNotFound
			}
			s := proposed.Sessions[i]
			if s.RevokedAt != nil || s.ContextRevokedAt != nil || !now.Before(s.ExpiresAt) {
				return state, nil, IAMDecisionError(authorization.Decision{Reason: "SESSION_INVALID"})
			}
			if ch.ExpectedRevision == 0 || s.Revision != ch.ExpectedRevision {
				return state, nil, ErrIAMRevisionConflict
			}
			s.ActiveRoleIDs = a.RoleIDs
			s.ActivationState = "active"
			s.Revision++
			proposed.Sessions[i] = s
		}
		if touched[key] {
			return state, nil, ErrIAMInvalidRelation
		}
		touched[key] = true
		normalized = append(normalized, ch)
	}
	// Validate activation ownership against the final batch, so ordering cannot
	// hide a source revoke or a disabled node. Only currently reachable roles
	// may be activated; future assignments do not authorize activation today.
	closure, err := IAMRoleClosures(state.Context, proposed.Roles)
	if err != nil {
		return state, nil, err
	}
	for _, ch := range normalized {
		if ch.Activation == nil {
			continue
		}
		s := proposed.Sessions[slices.IndexFunc(proposed.Sessions, func(s IAMSessionContext) bool { return s.SessionID == ch.Activation.SessionID })]
		authorized := map[int64]bool{}
		for _, a := range proposed.Assignments {
			if a.UserID == s.UserID && !a.Revoked && a.Validity.Contains(now) {
				for _, id := range closure[a.RoleID] {
					authorized[id] = true
				}
			}
		}
		for _, id := range s.ActiveRoleIDs {
			if !authorized[id] {
				return state, nil, ErrIAMProtected
			}
		}
	}
	return proposed, normalized, nil
}

func iamID(id int64) string { return strconv.FormatInt(id, 10) }

// Prospective IDs exist only to validate uniqueness inside a candidate graph.
// They are never returned as if a new constraint had already been persisted.
func iamNormalizeProposedConflicts(state IAMConstraintState, conflicts []IAMConstraintConflict) {
	existing := map[int64]bool{}
	for _, c := range state.Constraints {
		existing[c.ID] = true
	}
	for i := range conflicts {
		if conflicts[i].ConstraintID != 0 && !existing[conflicts[i].ConstraintID] {
			conflicts[i].ConstraintID = 0
			conflicts[i].ProposedConstraint = true
		}
	}
}
