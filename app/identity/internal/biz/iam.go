package biz

import (
	"context"
	"slices"
	"time"

	"micro-one-api/domain/authorization"
)

type IAMGrant struct {
	Operation string
	Effect    authorization.Effect
	Scope     authorization.Scope
}
type IAMRole struct {
	ID                   int64
	Context              authorization.Context
	Code                 string
	Name, Description    string
	Builtin              bool
	MaxMembers           *int64
	Status               string
	Revision             uint64
	CreationDelegationID int64
	Inherits             []int64 // senior -> junior
	Grants               []IAMGrant
}
type IAMAssignment struct {
	ID, UserID, RoleID int64
	Context            authorization.Context
	Boundary           authorization.Scope
	Validity           authorization.Interval
	Revoked            bool
	Origin             string // legacy_candidate/default/bootstrap/explicit; no username inference.
	MigrationBatchID   string
	Revision           uint64
	AssignedBy         int64
}

// IAMTx is data-owned and opaque; biz never imports a driver or storage model.
type IAMTx interface{ Handle() any }
type IAMTxRunner interface {
	// RunIAMWrite locks policy first, then invokes fn with authoritative facts
	// in the same transaction. Busy retries re-run the entire callback.
	RunIAMWrite(context.Context, func(context.Context, IAMTx) error) error
	// ReadIAMSnapshot provides a single primary repeatable-read view.
	ReadIAMSnapshot(context.Context, func(context.Context, IAMTx) error) error
}

// IAMSources is the pure A1 inheritance/source contract. It preserves every
// assignment path, including inactive mandatory deny and future intervals.
// It validates the entire graph; disabled nodes stop propagation.
func IAMSources(ctx authorization.Context, roles map[int64]IAMRole, assignments []IAMAssignment, active []int64) ([]authorization.GrantSource, error) {
	if err := ctx.RequirePlatform(); err != nil {
		return nil, err
	}
	visiting, done := map[int64]bool{}, map[int64]bool{}
	var validate func(int64) error
	validate = func(id int64) error {
		r, ok := roles[id]
		if !ok || r.ID != id || id <= 0 || r.Context != ctx || visiting[id] {
			return authorization.ErrGraph
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, child := range r.Inherits {
			if err := validate(child); err != nil {
				return err
			}
		}
		visiting[id], done[id] = false, true
		return nil
	}
	for id := range roles {
		if err := validate(id); err != nil {
			return nil, err
		}
	}
	out := []authorization.GrantSource{}
	reachable := map[int64]bool{}
	var userID int64
	for _, a := range assignments {
		if a.Context != ctx || a.ID <= 0 || a.UserID <= 0 || a.Validity.Validate() != nil {
			return nil, authorization.ErrGraph
		}
		if userID != 0 && a.UserID != userID {
			return nil, authorization.ErrGraph
		}
		userID = a.UserID
		if a.Revoked {
			continue
		}
		if _, ok := roles[a.RoleID]; !ok {
			return nil, authorization.ErrGraph
		}
		var walk func(int64, []int64, bool)
		walk = func(id int64, path []int64, isActive bool) {
			r := roles[id]
			if r.Status != "enabled" {
				return
			}
			reachable[id] = true
			path = append(slices.Clone(path), id)
			isActive = isActive || slices.Contains(active, id)
			for _, g := range r.Grants {
				out = append(out, authorization.GrantSource{Context: ctx, Operation: g.Operation, Effect: g.Effect, AssignmentID: a.ID, RoleID: id, InheritancePath: slices.Clone(path), RoleScope: g.Scope, AssignmentBoundary: a.Boundary, Validity: a.Validity, Active: isActive})
			}
			for _, child := range r.Inherits {
				walk(child, path, isActive)
			}
		}
		walk(a.RoleID, nil, false)
	}
	for _, id := range active {
		if !reachable[id] {
			return nil, authorization.ErrGraph
		}
	}
	return out, nil
}

// IAMMemberLimit is the reference future-window counterexample contract.
// A3 IAMCheckConstraints combines inheritance, SSD/DSD, all capacities and
// affected session windows; this helper remains a narrow reference regression.
func IAMMemberLimit(now time.Time, assignments []IAMAssignment, maxMembers int) error {
	if now.IsZero() || maxMembers < 1 {
		return authorization.ErrInterval
	}
	boundaries := []time.Time{now}
	for _, a := range assignments {
		if a.Validity.Validate() != nil || a.UserID <= 0 {
			return authorization.ErrInterval
		}
		if a.Revoked {
			continue
		}
		if a.Validity.StartsAt.After(now) {
			boundaries = append(boundaries, a.Validity.StartsAt)
		}
		if a.Validity.ExpiresAt != nil && a.Validity.ExpiresAt.After(now) {
			boundaries = append(boundaries, *a.Validity.ExpiresAt)
		}
	}
	// ponytail: quadratic reference scan; use a sweep if management batches grow.
	for _, at := range boundaries {
		members := map[int64]bool{}
		for _, a := range assignments {
			if !a.Revoked && a.Validity.Contains(at) {
				members[a.UserID] = true
			}
		}
		if len(members) > maxMembers {
			return authorization.ErrInterval
		}
	}
	return nil
}
