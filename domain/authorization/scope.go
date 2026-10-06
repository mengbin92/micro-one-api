package authorization

import (
	"bytes"
	"io"
	"slices"

	"micro-one-api/pkg/jsonx"
)

// ParseScope rejects unknown descriptors and extra JSON values, never all.
func ParseScope(data []byte, supported []ScopeKind) (Scope, error) {
	var s Scope
	d := jsonx.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return Scope{}, ErrScope
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return Scope{}, ErrScope
	}
	if s.Clauses == nil {
		return Scope{}, ErrScope
	}
	if err := s.Validate(supported); err != nil {
		return Scope{}, err
	}
	return s, nil
}

func (s Scope) Validate(supported []ScopeKind) error {
	for _, c := range s.Clauses {
		kinds := []ScopeKind{}
		if c.All {
			kinds = append(kinds, All)
		}
		if c.Self {
			kinds = append(kinds, Self)
		}
		for _, dim := range []struct {
			kind ScopeKind
			ids  []int64
		}{{Users, c.UserIDs}, {Resources, c.ResourceIDs}, {Groups, c.RoutingGroupIDs}} {
			if len(dim.ids) > 0 {
				kinds = append(kinds, dim.kind)
			}
			for _, id := range dim.ids {
				if id <= 0 {
					return ErrScope
				}
			}
		}
		if len(kinds) == 0 || (c.All && len(kinds) != 1) {
			return ErrScope
		}
		for _, k := range kinds {
			if !slices.Contains(supported, k) {
				return ErrScope
			}
		}
	}
	return nil
}

// Intersect preserves each source's conjunction, including group boundaries.
// The caller validates both inputs against the operation before using them.
func Intersect(a, b Scope) Scope {
	result := Scope{}
	for _, x := range a.Clauses {
		for _, y := range b.Clauses {
			if x.All {
				result.Clauses = append(result.Clauses, y)
				continue
			}
			if y.All {
				result.Clauses = append(result.Clauses, x)
				continue
			}
			users, ok1 := intersectIDs(x.UserIDs, y.UserIDs)
			resources, ok2 := intersectIDs(x.ResourceIDs, y.ResourceIDs)
			groups, ok3 := intersectIDs(x.RoutingGroupIDs, y.RoutingGroupIDs)
			if ok1 && ok2 && ok3 {
				result.Clauses = append(result.Clauses, Clause{Self: x.Self || y.Self, UserIDs: users, ResourceIDs: resources, RoutingGroupIDs: groups})
			}
		}
	}
	return result
}

func intersectIDs(a, b []int64) ([]int64, bool) {
	if len(a) == 0 {
		return slices.Clone(b), true
	}
	if len(b) == 0 {
		return slices.Clone(a), true
	}
	out := []int64{}
	for _, id := range a {
		if slices.Contains(b, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, len(out) > 0
}

func nonGroupMatches(c Clause, userID int64, o ObjectFacts) bool {
	return (!c.Self || (userID > 0 && o.OwnerUserID == userID)) &&
		(len(c.UserIDs) == 0 || slices.Contains(c.UserIDs, o.OwnerUserID)) &&
		(len(c.ResourceIDs) == 0 || (o.ResourceID > 0 && slices.Contains(c.ResourceIDs, o.ResourceID)))
}

// AllowCovers lets eligible paths jointly cover all affected groups for writes.
// Reads require one group; ID/all paths remain narrowed by their own boundary.
func AllowCovers(scopes []Scope, userID int64, o ObjectFacts, wholeObject bool) bool {
	for _, s := range scopes {
		if s.Validate([]ScopeKind{All, Self, Users, Resources, Groups}) != nil {
			return false
		}
	}
	covered := []int64{}
	for _, s := range scopes {
		for _, c := range s.Clauses {
			if !nonGroupMatches(c, userID, o) {
				continue
			}
			if len(c.RoutingGroupIDs) == 0 {
				return true
			}
			for _, id := range o.RoutingGroupIDs {
				if slices.Contains(c.RoutingGroupIDs, id) {
					if !wholeObject {
						return true
					}
					if !slices.Contains(covered, id) {
						covered = append(covered, id)
					}
				}
			}
		}
	}
	return len(o.RoutingGroupIDs) > 0 && len(covered) == len(slices.Compact(slices.Sorted(slices.Values(o.RoutingGroupIDs))))
}

// DenyMatches uses any affected group, for both reads and writes.
func DenyMatches(scopes []Scope, userID int64, o ObjectFacts) bool {
	for _, s := range scopes {
		if s.Validate([]ScopeKind{All, Self, Users, Resources, Groups}) != nil {
			return true // An unparseable mandatory deny cannot become permission.
		}
	}
	return AllowCovers(scopes, userID, o, false)
}
