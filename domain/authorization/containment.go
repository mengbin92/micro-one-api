package authorization

import "slices"

// ScopeContained proves inclusion clause by clause. It deliberately refuses
// cases requiring splitting a conjunction across unrelated ceiling clauses.
// Self is resolved to the actor whose authority is being compared.
func ScopeContained(candidate, ceiling Scope, candidateUser, ceilingUser int64) bool {
	kinds := []ScopeKind{All, Self, Users, Resources, Groups}
	if candidate.Clauses == nil || ceiling.Clauses == nil || candidate.Validate(kinds) != nil || ceiling.Validate(kinds) != nil {
		return false
	}
	if slices.ContainsFunc(ceiling.Clauses, func(c Clause) bool { return c.All }) {
		return true
	}
	resolve := func(c Clause, user int64) (Clause, bool) {
		if !c.Self {
			return c, true
		}
		if user <= 0 {
			return c, false
		}
		c.Self = false
		if len(c.UserIDs) > 0 && !slices.Contains(c.UserIDs, user) {
			return c, false
		}
		c.UserIDs = []int64{user}
		return c, true
	}
	subset := func(a, b []int64) bool {
		if len(b) == 0 {
			return true
		}
		if len(a) == 0 {
			return false
		}
		for _, id := range a {
			if !slices.Contains(b, id) {
				return false
			}
		}
		return true
	}
	for _, c := range candidate.Clauses {
		x, possible := resolve(c, candidateUser)
		if !possible {
			if c.Self && candidateUser > 0 {
				continue
			}
			return false
		}
		covered := false
		for _, limit := range ceiling.Clauses {
			y, ok := resolve(limit, ceilingUser)
			if !ok {
				continue
			}
			if y.All || (!x.All && subset(x.UserIDs, y.UserIDs) && subset(x.ResourceIDs, y.ResourceIDs) && subset(x.RoutingGroupIDs, y.RoutingGroupIDs)) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}
