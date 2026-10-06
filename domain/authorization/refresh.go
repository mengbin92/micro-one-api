package authorization

import (
	"context"
	"sort"
)

type refreshKey struct{}
type refreshRequest struct {
	resolver         Resolver
	point, operation string
	optional, self   bool
}

// withRefresh records the verified policy-owner seam, never request-supplied
// scopes. Each derived context owns its map so concurrent requests cannot
// change one another's authorization plan.
func withRefresh(ctx context.Context, resolver Resolver, point, operation string, optional bool) context.Context {
	old, _ := ctx.Value(refreshKey{}).(map[string]refreshRequest)
	next := make(map[string]refreshRequest, len(old)+1)
	for key, value := range old {
		next[key] = value
	}
	next[operation] = refreshRequest{resolver: resolver, point: point, operation: operation, optional: optional}
	return context.WithValue(ctx, refreshKey{}, next)
}

// Refresh obtains fresh authority before a resource transaction attempt.
// Callers must run this outside the replayable database callback. A revoked
// primary grant, mode change, actor change or dependency outage aborts the
// attempt. Optional grants remain independent; a denied one cannot authorize
// an actual write when Require checks the locked object.
func Refresh(ctx context.Context) (context.Context, error) {
	requests, _ := ctx.Value(refreshKey{}).(map[string]refreshRequest)
	keys := make([]string, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		request := requests[key]
		previous, ok := QueryScopeFromContext(ctx, key)
		if !ok || previous.ActorID <= 0 {
			return ctx, ErrDenied
		}
		var out ResourceAuthorization
		var err error
		if request.self {
			resolver, ok := request.resolver.(ActorResolver)
			if !ok {
				return ctx, ErrDenied
			}
			var actor Actor
			actor, out.Mode, err = resolver.ResolveActor(ctx, request.point, Credential(ctx))
			out.Query = QueryScope{ActorID: actor.UserID, Allow: []Scope{{Clauses: []Clause{{Self: true}}}}, ValidUntil: actor.ExpiresAt}
		} else if request.optional {
			resolver, ok := request.resolver.(OptionalResolver)
			if !ok {
				return ctx, ErrDenied
			}
			out, err = resolver.OptionalQuery(ctx, request.point, key, Credential(ctx))
		} else {
			out, err = request.resolver.Query(ctx, request.point, key, Credential(ctx))
		}
		if err != nil {
			return ctx, err
		}
		if out.Mode != "iam" {
			return ctx, ErrDenied
		}
		if request.optional && out.Query.ActorID == 0 {
			out.Query.ActorID = previous.ActorID
		}
		if out.Query.ActorID != previous.ActorID || (!request.optional && len(out.Query.Allow) == 0) {
			return ctx, ErrDenied
		}
		ctx = WithQueryScope(ctx, key, out.Query)
	}
	return ctx, nil
}

func withSelfRefresh(ctx context.Context, resolver Resolver, point, operation string) context.Context {
	ctx = withRefresh(ctx, resolver, point, operation, false)
	requests := ctx.Value(refreshKey{}).(map[string]refreshRequest)
	request := requests[operation]
	request.self = true
	requests[operation] = request
	return ctx
}
