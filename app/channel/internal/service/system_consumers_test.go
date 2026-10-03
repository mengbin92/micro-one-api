package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
)

type consumerModeResolver struct{}

func (consumerModeResolver) Query(context.Context, string, string, string) (authorization.ResourceAuthorization, error) {
	panic("system consumer must not use user query")
}
func (consumerModeResolver) Mode(context.Context, string) (string, error) { return "iam", nil }
func TestModelSystemConsumersEnforceExactCapabilityBeforeUsecase(t *testing.T) {
	s := &ChannelService{authz: consumerModeResolver{}}
	ctx := authorization.WithExternal(context.Background())
	ctx = serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "legacy-shared"})
	_, err := s.RecordModelUsage(ctx, &channelv1.RecordModelUsageRequest{ModelId: "m"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = s.RecordModelHealth(ctx, &channelv1.RecordModelHealthRequest{ModelId: "m"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	relay := serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "relay", Dedicated: true})
	reply, err := s.RecordModelUsage(serviceidentity.WithRPCMethod(relay, channelv1.ChannelService_RecordModelUsage_FullMethodName), &channelv1.RecordModelUsageRequest{})
	require.NoError(t, err)
	require.False(t, reply.Success) // nil usecase reached only after exact proof
	_, err = s.RecordModelHealth(serviceidentity.WithRPCMethod(relay, channelv1.ChannelService_RecordModelUsage_FullMethodName), &channelv1.RecordModelHealthRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
