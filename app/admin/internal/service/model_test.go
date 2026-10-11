package service

import (
	"context"
	"testing"

	adminv1 "micro-one-api/api/admin/v1"
	channelv1 "micro-one-api/api/channel/v1"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type modelRoutingRevisionClient struct {
	channelv1.ChannelServiceClient
}

func (c *modelRoutingRevisionClient) ListModelRoutings(context.Context, *channelv1.ListModelRoutingsRequest, ...grpc.CallOption) (*channelv1.ListModelRoutingsResponse, error) {
	return &channelv1.ListModelRoutingsResponse{
		Routings: []*channelv1.ModelRouting{{Id: 5, UpdatedAt: 1000, Revision: 7}},
	}, nil
}

func TestListModelRoutingsPreservesOwnerRevision(t *testing.T) {
	svc := NewAdminService(nil, nil, &modelRoutingRevisionClient{}, nil)
	resp, err := svc.ListModelRoutings(context.Background(), &adminv1.ListModelRoutingsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Routings, 1)
	require.Equal(t, int64(7), resp.Routings[0].GetRevision())
	require.Equal(t, int64(1000), resp.Routings[0].GetUpdatedAt())
}
