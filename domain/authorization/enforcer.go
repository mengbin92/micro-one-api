package authorization

import (
	"context"
	"errors"
	"time"
)

// Resolver is the policy-owner seam. The implementation lives in platform;
// domain usecases exchange only domain authorization shapes.
type Resolver interface {
	Query(context.Context, string, string, string) (ResourceAuthorization, error)
}

var ErrDenied = errors.New("authorization denied")

// Prepare obtains a fresh policy decision. A nil resolver is reserved for
// isolated legacy usecase tests; every production owner wires a resolver.
func Prepare(ctx context.Context, resolver Resolver, point, operation string) (context.Context, error) {
	if resolver == nil {
		return ctx, checkLegacy(ctx)
	}
	out, err := resolver.Query(ctx, point, operation, Credential(ctx))
	if err != nil {
		return ctx, err
	}
	if out.Mode == "legacy" {
		return ctx, checkLegacy(ctx)
	}
	if out.Mode != "iam" || out.Query.ActorID <= 0 || len(out.Query.Allow) == 0 {
		return ctx, ErrDenied
	}
	return withRefresh(WithQueryScope(ctx, operation, out.Query), resolver, point, operation, false), nil
}

// Require evaluates authoritative owner facts with the operation's fixed
// whole-object semantics. All affected groups must be covered for a write.
func Require(ctx context.Context, operation string, object ObjectFacts) error {
	q, ok := QueryScopeFromContext(ctx, operation)
	if !ok {
		return nil
	}
	if !q.ValidUntil.IsZero() && !time.Now().Before(q.ValidUntil) {
		return ErrDenied
	}
	op, ok := Lookup(operation)
	if !ok || !q.Matches(object, op.WholeObject) {
		return ErrDenied
	}
	return nil
}

type externalKey struct{}

// WithExternal marks requests entering an authenticated or guarded transport.
func WithExternal(ctx context.Context) context.Context {
	return context.WithValue(ctx, externalKey{}, true)
}
func External(ctx context.Context) bool { v, _ := ctx.Value(externalKey{}).(bool); return v }

type OptionalResolver interface {
	OptionalQuery(context.Context, string, string, string) (ResourceAuthorization, error)
}

// PrepareOptional scopes an independently protected response field.
func PrepareOptional(ctx context.Context, resolver Resolver, point, operation string) (context.Context, error) {
	if resolver == nil {
		return ctx, nil
	}
	optional, ok := resolver.(OptionalResolver)
	if !ok {
		return ctx, ErrDenied
	}
	out, err := optional.OptionalQuery(ctx, point, operation, Credential(ctx))
	if err != nil {
		return ctx, err
	}
	if out.Mode == "legacy" {
		return ctx, nil
	}
	if out.Mode != "iam" {
		return ctx, ErrDenied
	}
	if out.Query.ActorID == 0 {
		out.Query.ActorID, _ = ctx.Value(verifiedActorKey{}).(int64)
	}
	return withRefresh(WithQueryScope(ctx, operation, out.Query), resolver, point, operation, true), nil
}

type legacyAllowedKey struct{}

// WithLegacyAuthorization records a transport's legacy guard result. IAM
// always ignores numerical roles and obtains its decision from the resolver.
func WithLegacyAuthorization(ctx context.Context, allowed bool) context.Context {
	return context.WithValue(ctx, legacyAllowedKey{}, allowed)
}
func checkLegacy(ctx context.Context) error {
	allowed, present := ctx.Value(legacyAllowedKey{}).(bool)
	if present && !allowed {
		return ErrDenied
	}
	return nil
}

type ActorResolver interface {
	ResolveActor(context.Context, string, string) (Actor, string, error)
}

// PrepareSelf proves the actual session identity and supplies only a self
// predicate. Client IDs never establish ownership of an order or account.
func PrepareSelf(ctx context.Context, resolver Resolver, point, operation string) (context.Context, error) {
	if resolver == nil {
		return ctx, checkLegacy(ctx)
	}
	actors, ok := resolver.(ActorResolver)
	if !ok {
		return ctx, ErrDenied
	}
	actor, mode, err := actors.ResolveActor(ctx, point, Credential(ctx))
	if err != nil {
		return ctx, err
	}
	if mode == "legacy" {
		return ctx, checkLegacy(ctx)
	}
	if mode != "iam" || actor.UserID <= 0 {
		return ctx, ErrDenied
	}
	return withSelfRefresh(WithQueryScope(ctx, operation, QueryScope{ActorID: actor.UserID, Allow: []Scope{{Clauses: []Clause{{Self: true}}}}, ValidUntil: actor.ExpiresAt}), resolver, point, operation), nil
}
