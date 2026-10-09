// Package authz lets a data owner authorize one operator against the fixed
// execution-point registry. The owner sends its own dedicated service
// credential; identity verifies owner, operation and user session. Owners
// never trust caller-supplied scopes, object facts or permission strings.
package authz

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/iamdto"
)

// ErrUnavailable keeps authorization outages fail-closed without depending on
// a single transport implementation.
var ErrUnavailable = status.Error(codes.Unavailable, "authorization dependency unavailable")

var (
	errForbidden       = status.Error(codes.PermissionDenied, "operation not permitted")
	errUnauthenticated = status.Error(codes.Unauthenticated, "user session required")
)

// Client authorizes user operators for exactly one data owner. The gRPC
// connection must carry the owner's dedicated credential as per-RPC auth;
// identity independently verifies the owner against the fixed registry.
type Client struct {
	conn   *grpc.ClientConn
	owner  string
	iam    v.IAMServiceClient
	legacy bool
}

// NewClient builds an owner client. A nil IAM dependency fails closed;
// absence of a connection never establishes legacy mode.
func NewClient(owner string, iam v.IAMServiceClient) *Client {
	return &Client{owner: owner, iam: iam}
}

// NewLegacyClient is an isolated test fixture. Production owners must use
// FromEnvironment and obtain the current mode from identity.
func NewLegacyClient(owner string) *Client { return &Client{owner: owner, legacy: true} }

// Owner reports the fixed data-owner name this client may act for.
func (c *Client) Owner() string { return c.owner }

// Owns reports whether the execution point belongs to this owner. Wiring a
// point owned by another service is a build error caught by tests.
func (c *Client) Owns(point string) bool {
	e, ok := authorization.Execution(point)
	return ok && e.Owner == c.owner
}

// Query resolves the verified list scope for one operation at one fixed
// execution point. legacy mode returns an empty query so the caller keeps its
// legacy guard; IAM mode requires a verified operator and rejects an empty
// allow union. The owner applies Allow/Deny before counting or paginating.
func (c *Client) Query(ctx context.Context, point, operation, credential string) (authorization.ResourceAuthorization, error) {
	out, err := c.fetch(ctx, point, operation, credential, nil)
	if err != nil || out.Mode != "iam" {
		return out, err
	}
	if len(out.Query.Allow) == 0 {
		return out, errForbidden
	}
	return out, nil
}

// RequireDecision resolves one object authorization. The owner must supply
// authoritative object facts it read itself; a deny or any error never maps
// to an allow.
func (c *Client) RequireDecision(ctx context.Context, point, operation, credential string, object authorization.ObjectFacts) (authorization.ResourceAuthorization, error) {
	out, err := c.fetch(ctx, point, operation, credential, &object)
	if err != nil || out.Mode != "iam" {
		return out, err
	}
	if out.Decision == nil || !out.Decision.Allowed {
		return out, errForbidden
	}
	return out, nil
}

// ActorID verifies only the operator session in IAM mode and returns the
// authenticated user ID. Owners use it for self-service paths where the
// object owner must equal the actor.
func (c *Client) ActorID(ctx context.Context, point, operation, credential string) (int64, string, error) {
	out, err := c.fetch(ctx, point, operation, credential, nil)
	if err != nil || out.Mode != "iam" {
		return 0, out.Mode, err
	}
	if out.Query.ActorID <= 0 {
		return 0, out.Mode, errUnauthenticated
	}
	return out.Query.ActorID, out.Mode, nil
}

func (c *Client) fetch(ctx context.Context, point, operation, credential string, object *authorization.ObjectFacts) (authorization.ResourceAuthorization, error) {
	if c == nil || c.legacy {
		return authorization.ResourceAuthorization{Mode: "legacy"}, nil
	}
	if c.iam == nil {
		return authorization.ResourceAuthorization{Mode: "iam"}, ErrUnavailable
	}
	credential = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credential), "Bearer "))
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	if credential != "" {
		md.Set("x-operator-authorization", "Bearer "+credential)
	}
	p := &v.ResourceAuthorizationRequest{ExecutionPoint: point, Operation: operation}
	if object != nil {
		p.Object = iamdto.AuthorizationObjectFactsTo(*object)
	}
	reply, err := c.iam.GetResourceAuthorization(metadata.NewOutgoingContext(ctx, md), p)
	if err != nil {
		return authorization.ResourceAuthorization{Mode: "iam"}, err
	}
	if reply == nil {
		return authorization.ResourceAuthorization{Mode: "iam"}, ErrUnavailable
	}
	if reply.AuthorizationMode == "iam" && credential == "" {
		return authorization.ResourceAuthorization{Mode: "iam"}, errUnauthenticated
	}
	out := authorization.ResourceAuthorization{Mode: reply.AuthorizationMode, Query: authorization.QueryScope{ActorID: reply.ActorUserId, Versions: iamdto.AuthorizationVersionsFrom(reply.Versions)}}
	for _, scope := range reply.Allow {
		out.Query.Allow = append(out.Query.Allow, iamdto.AuthorizationScopeFrom(scope))
	}
	for _, scope := range reply.Deny {
		out.Query.Deny = append(out.Query.Deny, iamdto.AuthorizationScopeFrom(scope))
	}
	if reply.ValidUntil != nil {
		out.Query.ValidUntil = reply.ValidUntil.AsTime()
	}
	if reply.Decision != nil {
		d := iamdto.AuthorizationDecisionFrom(reply.Decision)
		out.Decision = &d
	}
	if out.Mode == "" {
		out.Mode = "iam"
	}
	return out, nil
}

// OptionalQuery allows a denied field to be redacted while dependency errors
// still abort the enclosing read. It never turns an outage into permission.
func (c *Client) OptionalQuery(ctx context.Context, point, operation, credential string) (authorization.ResourceAuthorization, error) {
	out, err := c.fetch(ctx, point, operation, credential, nil)
	if status.Code(err) == codes.PermissionDenied {
		// A denied optional permission still needs a live authenticated actor.
		// Upserts may start with optional create/update decisions, so there is
		// no earlier required permission from which to borrow the actor ID.
		actor, mode, actorErr := c.ResolveActor(ctx, point, credential)
		if actorErr != nil {
			return authorization.ResourceAuthorization{Mode: mode}, actorErr
		}
		if mode != "iam" || actor.UserID <= 0 {
			return authorization.ResourceAuthorization{Mode: mode}, errForbidden
		}
		return authorization.ResourceAuthorization{Mode: mode, Query: authorization.QueryScope{ActorID: actor.UserID, ValidUntil: actor.ExpiresAt}}, nil
	}
	return out, err
}

// ResolveActor uses the owner-only mode probe followed by identity's existing
// self-session API. Neither request accepts a client-provided user ID.
func (c *Client) ResolveActor(ctx context.Context, point, credential string) (authorization.Actor, string, error) {
	if c == nil || c.legacy {
		return authorization.Actor{}, "legacy", nil
	}
	if c.iam == nil {
		return authorization.Actor{}, "iam", ErrUnavailable
	}
	e, ok := authorization.Execution(point)
	if !ok || e.Owner != c.owner {
		return authorization.Actor{}, "iam", errForbidden
	}
	mode, err := c.iam.GetResourceAuthorization(ctx, &v.ResourceAuthorizationRequest{ExecutionPoint: point, ModeOnly: true})
	if err != nil {
		return authorization.Actor{}, "iam", err
	}
	if mode == nil {
		return authorization.Actor{}, "iam", ErrUnavailable
	}
	if mode.AuthorizationMode == "legacy" {
		return authorization.Actor{}, "legacy", nil
	}
	if mode.AuthorizationMode != "iam" {
		return authorization.Actor{}, "iam", ErrUnavailable
	}
	credential = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credential), "Bearer "))
	if credential == "" {
		return authorization.Actor{}, "iam", errUnauthenticated
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-operator-authorization", "Bearer "+credential)
	reply, err := c.iam.GetSessionAuthorization(metadata.NewOutgoingContext(ctx, md), &v.IAMRequest{Context: iamdto.AuthorizationContextTo(authorization.Platform())})
	if err != nil {
		return authorization.Actor{}, "iam", err
	}
	if reply == nil || reply.Session == nil || reply.Session.UserId <= 0 || reply.Session.ExpiresAt == nil || reply.Session.SessionId == "" {
		return authorization.Actor{}, "iam", errUnauthenticated
	}
	return authorization.Actor{UserID: reply.Session.UserId, SessionID: reply.Session.SessionId, ExpiresAt: reply.Session.ExpiresAt.AsTime()}, "iam", nil
}

func (c *Client) Close() error {
	if c != nil && c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) Mode(ctx context.Context, point string) (string, error) {
	if c == nil {
		return "", ErrUnavailable
	}
	if c.legacy {
		return "legacy", nil
	}
	if c.iam == nil {
		return "", ErrUnavailable
	}
	e, ok := authorization.Execution(point)
	if !ok || e.Owner != c.owner {
		return "", errForbidden
	}
	reply, err := c.iam.GetResourceAuthorization(ctx, &v.ResourceAuthorizationRequest{ExecutionPoint: point, ModeOnly: true})
	if err != nil {
		return "", err
	}
	if reply == nil || (reply.AuthorizationMode != "legacy" && reply.AuthorizationMode != "iam") {
		return "", ErrUnavailable
	}
	return reply.AuthorizationMode, nil
}
