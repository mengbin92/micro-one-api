package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/authorization/management"
)

type optionalActorRepo struct {
	mode       string
	actorErr   error
	actorCalls int
}

func (r *optionalActorRepo) ResourceMode(context.Context, string) (string, error) { return r.mode, nil }
func (r *optionalActorRepo) ResourceAuthorization(context.Context, string, authorization.ResourceRequest) (authorization.ResourceAuthorization, error) {
	return authorization.ResourceAuthorization{Mode: r.mode}, status.Error(codes.PermissionDenied, "optional field denied")
}
func (r *optionalActorRepo) Execute(_ context.Context, credential, method string, _ management.Request) (management.Response, error) {
	r.actorCalls++
	if credential != "live-session" || method != "GetSessionAuthorization" {
		return management.Response{}, status.Error(codes.Unauthenticated, "session required")
	}
	if r.actorErr != nil {
		return management.Response{}, r.actorErr
	}
	return management.Response{Session: &management.Session{UserID: 7, SessionID: "session", ExpiresAt: time.Now().Add(time.Hour)}}, nil
}

func TestAdminOptionalFieldDenialRequiresLiveActor(t *testing.T) {
	repo := &optionalActorRepo{mode: "iam"}
	svc := NewAdminService(nil, nil, nil, nil)
	svc.SetIAMService(NewIAMAdminService(adminbiz.NewIAMUsecase(repo)))
	resolver := svc.ResourceAuthorizer().(authorization.OptionalResolver)
	ctx := authorization.WithExternal(context.Background())
	out, err := resolver.OptionalQuery(ctx, "admin.system_options", "billing.pricing.read", "live-session")
	require.NoError(t, err)
	require.EqualValues(t, 7, out.Query.ActorID)
	require.Empty(t, out.Query.Allow)
	require.Equal(t, 1, repo.actorCalls)
	for _, failure := range []codes.Code{codes.Unauthenticated, codes.Unavailable} {
		repo.actorErr = status.Error(failure, "revoked or dependency unavailable")
		_, err = resolver.OptionalQuery(ctx, "admin.system_options", "billing.pricing.read", "live-session")
		require.Equal(t, failure, status.Code(err), "field redaction must not hide an invalid session or outage")
	}
}

func TestExternalAdminResourceDependencyCannotBecomeLegacy(t *testing.T) {
	svc := NewAdminService(nil, nil, nil, nil)
	ctx := authorization.WithExternal(context.Background())
	_, err := svc.ResourceAuthorizer().Query(ctx, "admin.console", "admin.console.access", "token")
	require.Equal(t, codes.Unavailable, status.Code(err))
	_, _, err = svc.ResourceAuthorizer().(authorization.ActorResolver).ResolveActor(ctx, "admin.subscription.self", "token")
	require.Equal(t, codes.Unavailable, status.Code(err))
	repo := &optionalActorRepo{mode: "unknown"}
	svc.SetIAMService(NewIAMAdminService(adminbiz.NewIAMUsecase(repo)))
	_, _, err = svc.ResourceAuthorizer().(authorization.ActorResolver).ResolveActor(ctx, "admin.subscription.self", "token")
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Zero(t, repo.actorCalls)
}
