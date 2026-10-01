package authorization

import (
	"context"
	"time"
)

// QueryScope is verified by the policy owner. Repositories compile its allow
// union and mandatory deny union before count, aggregation and pagination.
type QueryScope struct {
	ActorID     int64
	Allow, Deny []Scope
	Versions    Versions
	ValidUntil  time.Time
}

func QueryFromSources(actor Actor, operation Operation, sources []GrantSource, now time.Time, versions Versions) (QueryScope, error) {
	q := QueryScope{ActorID: actor.UserID, Versions: versions, ValidUntil: actor.ExpiresAt}
	if actor.UserID <= 0 || now.IsZero() || !now.Before(actor.ExpiresAt) {
		return q, ErrScope
	}
	for _, source := range sources {
		if source.Context != Platform() || source.Operation != operation.Code {
			continue
		}
		if source.Validity.Validate() != nil || source.RoleScope.Validate(operation.Scopes) != nil || source.AssignmentBoundary.Validate(operation.Scopes) != nil || (source.Effect != Allow && source.Effect != Deny) {
			return QueryScope{}, ErrScope
		}
		if now.Before(source.Validity.StartsAt) && source.Validity.StartsAt.Before(q.ValidUntil) {
			q.ValidUntil = source.Validity.StartsAt
		}
		if !source.Validity.Contains(now) {
			continue
		}
		if source.Validity.ExpiresAt != nil && source.Validity.ExpiresAt.Before(q.ValidUntil) {
			q.ValidUntil = *source.Validity.ExpiresAt
		}
		if source.Effect == Deny {
			q.Deny = append(q.Deny, source.RoleScope)
		} else if source.Active {
			q.Allow = append(q.Allow, Intersect(source.RoleScope, source.AssignmentBoundary))
		}
	}
	return q, nil
}

func (q QueryScope) Matches(object ObjectFacts, whole bool) bool {
	if object.ResourceID < 0 || object.OwnerUserID < 0 {
		return false
	}
	for _, id := range object.RoutingGroupIDs {
		if id <= 0 {
			return false
		}
	}
	return object.Context == Platform() && q.ActorID > 0 && AllowCovers(q.Allow, q.ActorID, object, whole) && !DenyMatches(q.Deny, q.ActorID, object)
}

func (q QueryScope) Global() bool {
	return len(q.Deny) == 0 && AllowCovers(q.Allow, q.ActorID, ObjectFacts{Context: Platform()}, false)
}

type credentialKey struct{}

func WithCredential(ctx context.Context, raw string) context.Context {
	return context.WithValue(ctx, credentialKey{}, raw)
}
func Credential(ctx context.Context) string {
	raw, _ := ctx.Value(credentialKey{}).(string)
	return raw
}

type queryKey struct{ operation string }

func WithQueryScope(ctx context.Context, operation string, q QueryScope) context.Context {
	return context.WithValue(ctx, queryKey{operation}, q)
}
func QueryScopeFromContext(ctx context.Context, operation string) (QueryScope, bool) {
	q, ok := ctx.Value(queryKey{operation}).(QueryScope)
	return q, ok
}
