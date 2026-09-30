package biz

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"micro-one-api/domain/authorization"
)

// IAMLimits counts distinct effective roles, including inheritance, in one
// context. Nil means unlimited; zero is invalid. Disabled nodes cut propagation.
type IAMLimits struct {
	MaxRolesPerUser, MaxRolesPerSession *int64
}
type IAMRoleConstraint struct {
	ID         int64
	Context    authorization.Context
	Kind, Name string
	RoleIDs    []int64
	MaxCount   int64
	Enabled    bool
	Revision   uint64
}
type IAMSessionContext struct {
	SessionID                   string
	UserID                      int64
	Context                     authorization.Context
	ExpiresAt                   time.Time
	RevokedAt, ContextRevokedAt *time.Time
	ActivationState             string
	ActiveRoleIDs               []int64
	SessionRevision, Revision   uint64
}
type IAMConstraintState struct {
	Context     authorization.Context
	Roles       []IAMRole
	Assignments []IAMAssignment
	Constraints []IAMRoleConstraint
	Sessions    []IAMSessionContext
	Limits      IAMLimits
}
type IAMConstraintConflict struct {
	ConstraintName       string
	ProposedConstraint   bool
	Kind                 string
	ConstraintID, RoleID int64
	UserIDs, RoleIDs     []int64
	SessionIDs           []string
	Actual, Limit        int64
	Validity             authorization.Interval
}

// Conflict details remain a DO for authorized governance callers; ordinary
// authorization errors must not disclose other users or hidden session IDs.
type IAMConstraintViolation struct{ Conflicts []IAMConstraintConflict }

func (e *IAMConstraintViolation) Error() string { return ErrIAMConstraintsViolated.Error() }
func (e *IAMConstraintViolation) Unwrap() error { return ErrIAMConstraintsViolated }

// IAMRoleClosures validates the whole DAG, even inactive nodes. Every closure
// is deduplicated; a disabled/draft/archived node contributes no roles or edges.
func IAMRoleClosures(c authorization.Context, roles []IAMRole) (map[int64][]int64, error) {
	if c.RequirePlatform() != nil {
		return nil, ErrIAMContextInvalid
	}
	index := map[int64]IAMRole{}
	for _, r := range roles {
		if r.ID <= 0 || r.Context != c || !slices.Contains([]string{"draft", "enabled", "disabled", "archived"}, r.Status) || (r.MaxMembers != nil && *r.MaxMembers < 1) {
			return nil, ErrIAMInvalidRelation
		}
		if _, ok := index[r.ID]; ok {
			return nil, ErrIAMInvalidRelation
		}
		index[r.ID] = r
	}
	color := map[int64]int{}
	closure := map[int64][]int64{}
	var visit func(int64) error
	visit = func(id int64) error {
		r, ok := index[id]
		if !ok {
			return ErrIAMInvalidRelation
		}
		if color[id] == 1 {
			return ErrIAMInvalidRelation
		}
		if color[id] == 2 {
			return nil
		}
		color[id] = 1
		seen := map[int64]bool{}
		effective := map[int64]bool{}
		if r.Status == "enabled" {
			effective[id] = true
		}
		for _, child := range r.Inherits {
			if child == id || seen[child] {
				return ErrIAMInvalidRelation
			}
			seen[child] = true
			if err := visit(child); err != nil {
				return err
			}
			if r.Status == "enabled" {
				for _, j := range closure[child] {
					effective[j] = true
				}
			}
		}
		closure[id] = iamSortedIDs(effective)
		color[id] = 2
		return nil
	}
	for _, id := range iamSortedRoleIDs(index) {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return closure, nil
}
func iamSortedRoleIDs(roles map[int64]IAMRole) []int64 {
	out := make([]int64, 0, len(roles))
	for id := range roles {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
func iamSortedIDs(ids map[int64]bool) []int64 {
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
func iamPositiveLimit(n *int64) bool { return n == nil || *n > 0 }
func iamUniquePositive(ids []int64) bool {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// IAMCheckConstraints evaluates the proposed complete state from now onward.
// Assignment intervals reserve membership even while an account is disabled;
// disabling identity must not silently free a reservation for later restoration.
// No activation or permission/deny scope can hide authorized roles from SSD.
func IAMCheckConstraints(now time.Time, state IAMConstraintState) ([]IAMConstraintConflict, error) {
	if now.IsZero() || !iamPositiveLimit(state.Limits.MaxRolesPerUser) || !iamPositiveLimit(state.Limits.MaxRolesPerSession) {
		return nil, ErrIAMInvalidRelation
	}
	closures, err := IAMRoleClosures(state.Context, state.Roles)
	if err != nil {
		return nil, err
	}
	boundaries := []time.Time{now.UTC()}
	addBoundary := func(t time.Time) {
		if t.After(now) {
			boundaries = append(boundaries, t.UTC())
		}
	}
	assignments := map[int64]bool{}
	relations := map[[2]int64]bool{}
	for _, a := range state.Assignments {
		_, exists := closures[a.RoleID]
		key := [2]int64{a.UserID, a.RoleID}
		if a.ID <= 0 || a.UserID <= 0 || a.Context != state.Context || !exists || a.Validity.Validate() != nil || assignments[a.ID] || relations[key] {
			return nil, ErrIAMInvalidRelation
		}
		assignments[a.ID], relations[key] = true, true
		if !a.Revoked {
			addBoundary(a.Validity.StartsAt)
			if a.Validity.ExpiresAt != nil {
				addBoundary(*a.Validity.ExpiresAt)
			}
		}
	}
	constraints := map[int64]bool{}
	for _, c := range state.Constraints {
		if c.ID <= 0 || c.Context != state.Context || constraints[c.ID] || !slices.Contains([]string{"SSD", "DSD"}, c.Kind) || c.MaxCount < 1 || len(c.RoleIDs) == 0 || !iamUniquePositive(c.RoleIDs) {
			return nil, ErrIAMInvalidRelation
		}
		constraints[c.ID] = true
		for _, id := range c.RoleIDs {
			if _, ok := closures[id]; !ok {
				return nil, ErrIAMInvalidRelation
			}
		}
	}
	sessions := map[string]bool{}
	for _, s := range state.Sessions {
		if strings.TrimSpace(s.SessionID) == "" || sessions[s.SessionID] || s.UserID <= 0 || s.Context != state.Context || s.ExpiresAt.IsZero() || !iamUniquePositive(s.ActiveRoleIDs) || !slices.Contains([]string{"active", "selection_required"}, s.ActivationState) || (s.ActivationState == "selection_required" && len(s.ActiveRoleIDs) > 0) {
			return nil, ErrIAMInvalidRelation
		}
		sessions[s.SessionID] = true
		for _, id := range s.ActiveRoleIDs {
			if _, ok := closures[id]; !ok {
				return nil, ErrIAMInvalidRelation
			}
		}
		if s.RevokedAt == nil && s.ContextRevokedAt == nil {
			addBoundary(s.ExpiresAt)
		}
	}
	slices.SortFunc(boundaries, func(a, b time.Time) int { return a.Compare(b) })
	boundaries = slices.CompactFunc(boundaries, func(a, b time.Time) bool { return a.Equal(b) })
	roles := slices.Clone(state.Roles)
	slices.SortFunc(roles, func(a, b IAMRole) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	cs := slices.Clone(state.Constraints)
	slices.SortFunc(cs, func(a, b IAMRoleConstraint) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	ss := slices.Clone(state.Sessions)
	slices.SortFunc(ss, func(a, b IAMSessionContext) int { return strings.Compare(a.SessionID, b.SessionID) })
	conflicts := []IAMConstraintConflict{}
	previous := map[string]int{}
	appendConflict := func(c IAMConstraintConflict) {
		key := fmt.Sprintf("%s/%d/%d/%v/%v/%v/%d/%d", c.Kind, c.ConstraintID, c.RoleID, c.UserIDs, c.RoleIDs, c.SessionIDs, c.Actual, c.Limit)
		if i, ok := previous[key]; ok && conflicts[i].Validity.ExpiresAt != nil && conflicts[i].Validity.ExpiresAt.Equal(c.Validity.StartsAt) {
			conflicts[i].Validity.ExpiresAt = c.Validity.ExpiresAt
			return
		}
		previous[key] = len(conflicts)
		conflicts = append(conflicts, c)
	}
	// ponytail: full-context boundary scan favors auditable management semantics;
	// introduce an incremental sweep only when measured batch sizes justify it.
	for i, at := range boundaries {
		window := authorization.Interval{StartsAt: at}
		if i+1 < len(boundaries) {
			end := boundaries[i+1]
			window.ExpiresAt = &end
		}
		authorized := map[int64]map[int64]bool{}
		for _, a := range state.Assignments {
			if a.Revoked || !a.Validity.Contains(at) {
				continue
			}
			if authorized[a.UserID] == nil {
				authorized[a.UserID] = map[int64]bool{}
			}
			for _, id := range closures[a.RoleID] {
				authorized[a.UserID][id] = true
			}
		}
		users := make([]int64, 0, len(authorized))
		for id := range authorized {
			users = append(users, id)
		}
		slices.Sort(users)
		for _, r := range roles {
			if r.MaxMembers == nil {
				continue
			}
			members := []int64{}
			for _, uid := range users {
				if authorized[uid][r.ID] {
					members = append(members, uid)
				}
			}
			if int64(len(members)) > *r.MaxMembers {
				appendConflict(IAMConstraintConflict{Kind: "MAX_MEMBERS", RoleID: r.ID, UserIDs: members, RoleIDs: []int64{r.ID}, Actual: int64(len(members)), Limit: *r.MaxMembers, Validity: window})
			}
		}
		for _, uid := range users {
			ids := iamSortedIDs(authorized[uid])
			if n := state.Limits.MaxRolesPerUser; n != nil && int64(len(ids)) > *n {
				appendConflict(IAMConstraintConflict{Kind: "MAX_ROLES_PER_USER", UserIDs: []int64{uid}, RoleIDs: ids, Actual: int64(len(ids)), Limit: *n, Validity: window})
			}
			for _, c := range cs {
				if !c.Enabled || c.Kind != "SSD" {
					continue
				}
				matches := iamConstraintMatches(c.RoleIDs, authorized[uid])
				if int64(len(matches)) > c.MaxCount {
					appendConflict(IAMConstraintConflict{Kind: "SSD", ConstraintID: c.ID, ConstraintName: c.Name, UserIDs: []int64{uid}, RoleIDs: matches, Actual: int64(len(matches)), Limit: c.MaxCount, Validity: window})
				}
			}
		}
		for _, s := range ss {
			if s.RevokedAt != nil || s.ContextRevokedAt != nil || !at.Before(s.ExpiresAt) || s.ActivationState != "active" {
				continue
			}
			active := map[int64]bool{}
			// Stale activations lose authority when their source assignment expires or
			// is revoked. A4 will validate activation requests against current sources.
			for _, id := range s.ActiveRoleIDs {
				if authorized[s.UserID][id] {
					for _, j := range closures[id] {
						active[j] = true
					}
				}
			}
			ids := iamSortedIDs(active)
			if n := state.Limits.MaxRolesPerSession; n != nil && int64(len(ids)) > *n {
				appendConflict(IAMConstraintConflict{Kind: "MAX_ROLES_PER_SESSION", UserIDs: []int64{s.UserID}, SessionIDs: []string{s.SessionID}, RoleIDs: ids, Actual: int64(len(ids)), Limit: *n, Validity: window})
			}
			for _, c := range cs {
				if !c.Enabled || c.Kind != "DSD" {
					continue
				}
				matches := iamConstraintMatches(c.RoleIDs, active)
				if int64(len(matches)) > c.MaxCount {
					appendConflict(IAMConstraintConflict{Kind: "DSD", ConstraintID: c.ID, ConstraintName: c.Name, UserIDs: []int64{s.UserID}, SessionIDs: []string{s.SessionID}, RoleIDs: matches, Actual: int64(len(matches)), Limit: c.MaxCount, Validity: window})
				}
			}
		}
	}
	return conflicts, nil
}
func iamConstraintMatches(members []int64, effective map[int64]bool) []int64 {
	out := []int64{}
	for _, id := range members {
		if effective[id] {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
