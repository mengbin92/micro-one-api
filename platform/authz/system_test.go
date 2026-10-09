package authz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

type systemModeResolver struct {
	mode string
	err  error
}

func (r systemModeResolver) Query(context.Context, string, string, string) (authorization.ResourceAuthorization, error) {
	panic("system guard must not request user authority")
}
func (r systemModeResolver) Mode(context.Context, string) (string, error) { return r.mode, r.err }

func TestSystemConsumerRequiresExactCurrentMethod(t *testing.T) {
	const method = "/api.channel.v1.ChannelService/StoreSubscriptionCredentials"
	ctx := authorization.WithExternal(context.Background())
	require.Equal(t, codes.PermissionDenied, status.Code(RequireSystem(ctx, systemModeResolver{mode: "iam"}, "channel.channels.list", method)))
	shared := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "legacy-shared"}), method)
	require.Equal(t, codes.PermissionDenied, status.Code(RequireSystem(shared, systemModeResolver{mode: "iam"}, "channel.channels.list", method)))
	relay := serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "relay", Dedicated: true})
	require.NoError(t, RequireSystem(serviceidentity.WithRPCMethod(relay, method), nil, "channel.channels.list", method))
	require.Equal(t, codes.PermissionDenied, status.Code(RequireSystem(serviceidentity.WithRPCMethod(relay, "/api.channel.v1.ChannelService/RecordChannelHealth"), systemModeResolver{mode: "iam"}, "channel.channels.list", method)))
	require.Equal(t, codes.Unavailable, status.Code(RequireSystem(ctx, nil, "channel.channels.list", method)))
	require.Equal(t, codes.Unavailable, status.Code(RequireSystem(ctx, systemModeResolver{err: ErrUnavailable}, "channel.channels.list", method)))
	require.NoError(t, RequireSystem(shared, systemModeResolver{mode: "legacy"}, "channel.channels.list", method))
}

func TestTypedNilAuthorizationClientFailsClosed(t *testing.T) {
	var client *Client
	_, err := client.Mode(context.Background(), "billing.payments.read")
	require.ErrorIs(t, err, ErrUnavailable)
}
