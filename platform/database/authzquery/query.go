// Package authzquery compiles verified domain scopes into parameterized SQL.
// Column and relation declarations belong to the data owner, never a request.
package authzquery

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"micro-one-api/domain/authorization"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Columns struct {
	Resource, User string
	// UserText is explicit for owners whose immutable user_id column is TEXT.
	UserText bool
	// Groups is a data-owned SELECT with a single '?' for the candidate group
	// IDs. It returns one row when the current object has a matching group.
	Groups string
}

var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$`)

func Predicate(q authorization.QueryScope, c Columns) (string, []any, error) {
	if q.ActorID <= 0 || (c.Resource != "" && !identifier.MatchString(c.Resource)) || (c.User != "" && !identifier.MatchString(c.User)) {
		return "", nil, authorization.ErrScope
	}
	if !q.ValidUntil.IsZero() && !time.Now().Before(q.ValidUntil) {
		return "", nil, authorization.ErrDenied
	}
	allow, aa, err := compile(q.Allow, q.ActorID, c)
	if err != nil {
		return "", nil, err
	}
	deny, da, err := compile(q.Deny, q.ActorID, c)
	if err != nil {
		return "", nil, err
	}
	return "(" + allow + ") AND NOT (" + deny + ")", append(aa, da...), nil
}
func Apply(db *gorm.DB, q authorization.QueryScope, c Columns) (*gorm.DB, error) {
	predicate, args, err := Predicate(q, c)
	if err != nil {
		return nil, err
	}
	return db.Where(predicate, args...), nil
}

func compile(scopes []authorization.Scope, actorID int64, cols Columns) (string, []any, error) {
	parts, args := []string{}, []any{}
	for _, s := range scopes {
		if err := s.Validate([]authorization.ScopeKind{authorization.All, authorization.Self, authorization.Users, authorization.Resources, authorization.Groups}); err != nil {
			return "", nil, err
		}
		for _, c := range s.Clauses {
			if c.All {
				parts = append(parts, "1 = 1")
				continue
			}
			conditions := []string{}
			if c.Self {
				if cols.User == "" {
					return "", nil, authorization.ErrScope
				}
				conditions = append(conditions, cols.User+" = ?")
				if cols.UserText {
					args = append(args, strconv.FormatInt(actorID, 10))
				} else {
					args = append(args, actorID)
				}
			}
			for _, dim := range []struct {
				column string
				ids    []int64
			}{{cols.User, c.UserIDs}, {cols.Resource, c.ResourceIDs}} {
				if len(dim.ids) == 0 {
					continue
				}
				if dim.column == "" {
					return "", nil, authorization.ErrScope
				}
				conditions = append(conditions, dim.column+" IN ?")
				if dim.column == cols.User && cols.UserText {
					ids := make([]string, len(dim.ids))
					for i, id := range dim.ids {
						ids[i] = strconv.FormatInt(id, 10)
					}
					args = append(args, ids)
				} else {
					args = append(args, dim.ids)
				}
			}
			if len(c.RoutingGroupIDs) > 0 {
				if cols.Groups == "" || strings.Count(cols.Groups, "?") != 1 {
					return "", nil, fmt.Errorf("owner does not support group scope: %w", authorization.ErrScope)
				}
				conditions = append(conditions, "EXISTS ("+cols.Groups+")")
				args = append(args, c.RoutingGroupIDs)
			}
			parts = append(parts, "("+strings.Join(conditions, " AND ")+")")
		}
	}
	if len(parts) == 0 {
		return "1 = 0", nil, nil
	}
	return strings.Join(parts, " OR "), args, nil
}

// ApplyContext intersects every required operation present in the verified
// request context. Owners call it before count, aggregation and pagination.
func ApplyContext(ctx context.Context, db *gorm.DB, c Columns, operations ...string) (*gorm.DB, error) {
	for _, op := range operations {
		if q, ok := authorization.QueryScopeFromContext(ctx, op); ok {
			if !q.ValidUntil.IsZero() && !time.Now().Before(q.ValidUntil) {
				return nil, authorization.ErrDenied
			}
			var err error
			db, err = Apply(db, q, c)
			if err != nil {
				return nil, err
			}
		}
	}
	return db, nil
}
