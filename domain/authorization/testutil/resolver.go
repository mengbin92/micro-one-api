// Package testutil supplies an isolated policy-owner seam for owner tests.
package testutil

import (
	"context"
	"micro-one-api/domain/authorization"
	"slices"
	"time"
)

type Resolver struct {
	ActorID int64
	Scopes  map[string]authorization.QueryScope
	Err     error
}

func (r *Resolver) Query(_ context.Context, point, operation, credential string) (authorization.ResourceAuthorization, error) {
	entry, ok := authorization.Execution(point)
	if !ok || !slices.Contains(entry.Operations, operation) || !authorization.ResourceBound(operation) {
		return authorization.ResourceAuthorization{}, authorization.ErrDenied
	}
	if r.Err != nil {
		return authorization.ResourceAuthorization{}, r.Err
	}
	if credential != "verified-session" {
		return authorization.ResourceAuthorization{}, authorization.ErrDenied
	}
	q, ok := r.Scopes[operation]
	if !ok {
		return authorization.ResourceAuthorization{}, authorization.ErrDenied
	}
	if q.ActorID == 0 {
		q.ActorID = r.ActorID
	}
	return authorization.ResourceAuthorization{Mode: "iam", Query: q}, nil
}
func (r *Resolver) OptionalQuery(ctx context.Context, point, operation, credential string) (authorization.ResourceAuthorization, error) {
	entry, valid := authorization.Execution(point)
	if !valid || !slices.Contains(entry.Operations, operation) || !authorization.ResourceBound(operation) {
		return authorization.ResourceAuthorization{}, authorization.ErrDenied
	}
	if _, ok := r.Scopes[operation]; !ok && r.Err == nil && credential == "verified-session" {
		return authorization.ResourceAuthorization{Mode: "iam", Query: authorization.QueryScope{ActorID: r.ActorID}}, nil
	}
	return r.Query(ctx, point, operation, credential)
}
func (r *Resolver) ResolveActor(_ context.Context, _, credential string) (authorization.Actor, string, error) {
	if r.Err != nil {
		return authorization.Actor{}, "iam", r.Err
	}
	if credential != "verified-session" {
		return authorization.Actor{}, "iam", authorization.ErrDenied
	}
	return authorization.Actor{UserID: r.ActorID, SessionID: "real-jti", ExpiresAt: time.Now().Add(time.Hour)}, "iam", nil
}
func Context() context.Context {
	return authorization.WithWriteReason(authorization.WithCredential(authorization.WithExternal(context.Background()), "verified-session"), "isolated owner acceptance")
}
func All() authorization.QueryScope {
	return authorization.QueryScope{ActorID: 1, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{All: true}}}}}
}
func Users(ids ...int64) authorization.QueryScope {
	return authorization.QueryScope{ActorID: 1, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{UserIDs: ids}}}}}
}
func Resources(ids ...int64) authorization.QueryScope {
	return authorization.QueryScope{ActorID: 1, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{ResourceIDs: ids}}}}}
}
func Groups(ids ...int64) authorization.QueryScope {
	return authorization.QueryScope{ActorID: 1, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{RoutingGroupIDs: ids}}}}}
}
