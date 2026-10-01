package authz_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	c "micro-one-api/api/common/v1"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/security/serviceidentity"
)

type fakeIAM struct {
	v.IAMServiceClient
	reply   *v.ResourceAuthorizationReply
	err     error
	gotMeta metadata.MD
	gotReq  *v.ResourceAuthorizationRequest
}

func (f *fakeIAM) GetResourceAuthorization(ctx context.Context, p *v.ResourceAuthorizationRequest, _ ...grpc.CallOption) (*v.ResourceAuthorizationReply, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	f.gotMeta = md
	f.gotReq = p
	if f.err != nil {
		return nil, f.err
	}
	return f.reply, nil
}

func TestClientLegacyShortCircuit(t *testing.T) {
	client := authz.NewLegacyClient("channel")
	out, err := client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "raw")
	require.NoError(t, err)
	require.Equal(t, "legacy", out.Mode)
	out, err = client.RequireDecision(context.Background(), "channel.channels.update", "channel.channel.update", "raw", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: 1})
	require.NoError(t, err)
	require.Equal(t, "legacy", out.Mode)
}

func TestClientUnavailableFailsClosed(t *testing.T) {
	client := authz.NewClient("channel", nil)
	_, err := client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "raw")
	require.Equal(t, codes.Unavailable, status.Code(err))
	_, err = client.RequireDecision(context.Background(), "channel.channels.update", "channel.channel.update", "raw", authorization.ObjectFacts{})
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestClientRequiresCredential(t *testing.T) {
	client := authz.NewClient("channel", &fakeIAM{reply: &v.ResourceAuthorizationReply{AuthorizationMode: "iam"}})
	_, err := client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "")
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = client.RequireDecision(context.Background(), "channel.channels.update", "channel.channel.update", "  ", authorization.ObjectFacts{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestClientForwardsCredentialAndScopes(t *testing.T) {
	fake := &fakeIAM{reply: &v.ResourceAuthorizationReply{
		AuthorizationMode: "iam",
		ActorUserId:       42,
		Allow: []*c.AuthorizationScope{{
			Clauses: []*c.AuthorizationScopeClause{{Self: true}},
		}},
		Deny:       []*c.AuthorizationScope{{Clauses: []*c.AuthorizationScopeClause{{UserIds: []int64{9}}}}},
		ValidUntil: timestamppb.New(time.Now().Add(time.Hour)),
	}}
	client := authz.NewClient("channel", fake)
	out, err := client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "Bearer raw-jwt")
	require.NoError(t, err)
	require.Equal(t, "iam", out.Mode)
	require.EqualValues(t, 42, out.Query.ActorID)
	require.Len(t, out.Query.Allow, 1)
	require.True(t, out.Query.Allow[0].Clauses[0].Self)
	require.Len(t, out.Query.Deny, 1)
	require.Equal(t, "Bearer raw-jwt", fake.gotMeta.Get("x-operator-authorization")[0])
	require.Equal(t, "channel.channels.list", fake.gotReq.ExecutionPoint)
	require.Equal(t, "channel.channel.list", fake.gotReq.Operation)
}

func TestClientQueryRejectsEmptyAllow(t *testing.T) {
	fake := &fakeIAM{reply: &v.ResourceAuthorizationReply{AuthorizationMode: "iam", ActorUserId: 42}}
	client := authz.NewClient("channel", fake)
	_, err := client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "raw")
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestClientDecision(t *testing.T) {
	fake := &fakeIAM{reply: &v.ResourceAuthorizationReply{
		AuthorizationMode: "iam",
		ActorUserId:       42,
		Decision:          &c.AuthorizationDecision{Allowed: true, Reason: "ALLOWED"},
	}}
	client := authz.NewClient("channel", fake)
	out, err := client.RequireDecision(context.Background(), "channel.channels.update", "channel.channel.update", "raw", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: 7})
	require.NoError(t, err)
	require.True(t, out.Decision.Allowed)
	require.NotNil(t, fake.gotReq.Object)

	fake.reply.Decision.Allowed = false
	_, err = client.RequireDecision(context.Background(), "channel.channels.update", "channel.channel.update", "raw", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: 7})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestClientPropagatesTransportError(t *testing.T) {
	fake := &fakeIAM{err: status.Error(codes.Aborted, "revision conflict")}
	client := authz.NewClient("channel", fake)
	_, err := client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "raw")
	require.Equal(t, codes.Aborted, status.Code(err))

	fake.err = errors.New("boom")
	_, err = client.Query(context.Background(), "channel.channels.list", "channel.channel.list", "raw")
	require.Error(t, err)
}

func TestOwnsMatchesRegistry(t *testing.T) {
	client := authz.NewLegacyClient("channel")
	require.True(t, client.Owns("channel.channels.list"))
	require.True(t, client.Owns("channel.accounts.update"))
	require.False(t, client.Owns("identity.users.list"))
	require.False(t, client.Owns("does.not.exist"))
}

func TestIAMCoverageRequiresDedicatedCallerAndCompletedMethod(t *testing.T) {
	fake := &fakeIAM{reply: &v.ResourceAuthorizationReply{AuthorizationMode: "iam"}}
	client := authz.NewClient("channel", fake)
	method := "/api.channel.v1.ChannelService/ListChannels"
	guard := authz.CoverageUnaryInterceptor(client, "channel.channels.list", []string{method})
	called := false
	next := func(context.Context, any) (any, error) { called = true; return nil, nil }
	ctx := serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "legacy-shared"})
	_, err := guard(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, next)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, called)
	ctx = serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "admin", Dedicated: true})
	_, err = guard(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, next)
	require.NoError(t, err)
	require.True(t, called)
	called = false
	_, err = guard(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/api.channel.v1.ChannelService/ImportModels"}, next)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, called)
	fake.err = status.Error(codes.Unavailable, "identity down")
	_, err = guard(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, next)
	require.Equal(t, codes.Unavailable, status.Code(err))
	// An exact verified relay capability remains usable during an IAM outage.
	method = "/api.channel.v1.ChannelService/GetChannel"
	ctx = serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "relay", Dedicated: true}), method)
	_, err = guard(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, next)
	require.NoError(t, err)
	_, err = guard(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/api.channel.v1.ChannelService/DeleteChannel"}, next)
	require.Error(t, err)
}
func TestDeniedOptionalFieldDoesNotHideDependencyFailure(t *testing.T) {
	fake := &fakeIAM{err: status.Error(codes.PermissionDenied, "field denied")}
	client := authz.NewClient("log", fake)
	out, err := client.OptionalQuery(context.Background(), "log.requests.read", "log.request.content.read", "user")
	require.NoError(t, err)
	require.Empty(t, out.Query.Allow)
	fake.err = status.Error(codes.Unavailable, "identity down")
	_, err = client.OptionalQuery(context.Background(), "log.requests.read", "log.request.content.read", "user")
	require.Equal(t, codes.Unavailable, status.Code(err))
}
