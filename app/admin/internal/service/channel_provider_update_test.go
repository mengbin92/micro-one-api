package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	adminv1 "micro-one-api/api/admin/v1"
	channelv1 "micro-one-api/api/channel/v1"
)

type providerUpdateClient struct {
	channelv1.ChannelServiceClient
	request *channelv1.UpdateChannelRequest
}

func (c *providerUpdateClient) UpdateChannel(_ context.Context, r *channelv1.UpdateChannelRequest, _ ...grpc.CallOption) (*channelv1.UpdateChannelResponse, error) {
	c.request = r
	return &channelv1.UpdateChannelResponse{Success: true}, nil
}

func TestAdminProviderUpdateForwardsPresenceAndCAS(t *testing.T) {
	client := &providerUpdateClient{}
	svc := NewAdminService(nil, nil, client, nil)
	value := int32(6)
	for _, providerType := range []*int32{nil, &value} {
		_, err := svc.UpdateChannel(context.Background(), &adminv1.AdminUpdateChannelRequest{ChannelId: 11, Type: providerType, ExpectedRevision: 2, Reason: "repair provider"})
		require.NoError(t, err)
		require.Equal(t, providerType, client.request.Type)
		require.EqualValues(t, 2, client.request.ExpectedRevision)
		require.Empty(t, client.request.Key)
	}
}
