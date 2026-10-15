package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/security/serviceidentity"
)

type contractCoverageIAM struct{ identityv1.IAMServiceClient }

func (contractCoverageIAM) GetResourceAuthorization(context.Context, *identityv1.ResourceAuthorizationRequest, ...grpc.CallOption) (*identityv1.ResourceAuthorizationReply, error) {
	return &identityv1.ResourceAuthorizationReply{AuthorizationMode: "iam"}, nil
}

func TestUpstreamContractsCoverageReachesIAMOwner(t *testing.T) {
	for _, method := range []string{channelv1.ChannelService_ListUpstreamContracts_FullMethodName, channelv1.ChannelService_CreateUpstreamContract_FullMethodName, channelv1.ChannelService_TransitionUpstreamContract_FullMethodName} {
		t.Run(method, func(t *testing.T) {
			for _, caller := range []struct {
				name      string
				principal serviceidentity.Principal
				allowed   bool
			}{
				{"dedicated_admin", serviceidentity.Principal{Name: "admin", Dedicated: true}, true},
				{"shared_admin", serviceidentity.Principal{Name: "admin"}, false},
				{"other_dedicated_service", serviceidentity.Principal{Name: "relay", Dedicated: true}, false},
			} {
				t.Run(caller.name, func(t *testing.T) {
					ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-operator-authorization", "Bearer user-session"))
					ctx = serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(ctx, caller.principal), method)
					called := false
					info := &grpc.UnaryServerInfo{FullMethod: method}
					_, err := authz.OperatorUnaryInterceptor()(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
						return authz.CoverageUnaryInterceptor(authz.NewClient("channel", contractCoverageIAM{}), "channel.channels.list", channelReadyMethods)(ctx, req, info, func(ctx context.Context, _ any) (any, error) {
							called = true
							require.Equal(t, "Bearer user-session", authorization.Credential(ctx))
							return nil, status.Error(codes.PermissionDenied, "owner operation not granted")
						})
					})
					require.Equal(t, caller.allowed, called, "completed owner must be reached only by its dedicated admin caller")
					require.Equal(t, codes.PermissionDenied, status.Code(err))
					if caller.allowed {
						require.Equal(t, "owner operation not granted", status.Convert(err).Message())
					} else {
						require.Equal(t, "owner execution point incomplete or caller denied", status.Convert(err).Message())
					}
				})
			}
		})
	}
}

func TestUpstreamContractsCoverageKeepsSystemAndUnreviewedRPCsClosedToUsers(t *testing.T) {
	for _, method := range []string{channelv1.ChannelService_AcquireContractProbe_FullMethodName, channelv1.ChannelService_RecordContractResult_FullMethodName, "/api.channel.v1.ChannelService/UnreviewedContractAction"} {
		t.Run(method, func(t *testing.T) {
			require.NotContains(t, channelReadyMethods, method)
			ctx := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(authorization.WithCredential(context.Background(), "Bearer user-session"), serviceidentity.Principal{Name: "admin", Dedicated: true}), method)
			called := false
			_, err := authz.CoverageUnaryInterceptor(authz.NewClient("channel", contractCoverageIAM{}), "channel.channels.list", channelReadyMethods)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, any) (any, error) {
				called = true
				return nil, nil
			})
			require.Equal(t, codes.PermissionDenied, status.Code(err))
			require.False(t, called)
		})
	}
}
