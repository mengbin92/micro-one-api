package channelclient

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/routing"
	"testing"
)

type channelFake struct {
	channelv1.ChannelServiceClient
	list   *channelv1.ListRoutingGroupsReply
	detail *channelv1.RoutingGroupDetail
	query  *channelv1.ListRoutingGroupsRequest
}

func (f *channelFake) ListRoutingGroups(_ context.Context, q *channelv1.ListRoutingGroupsRequest, _ ...grpc.CallOption) (*channelv1.ListRoutingGroupsReply, error) {
	f.query = q
	return f.list, nil
}
func (f *channelFake) GetRoutingGroup(context.Context, *channelv1.GetRoutingGroupRequest, ...grpc.CallOption) (*channelv1.RoutingGroupDetail, error) {
	return f.detail, nil
}
func TestRoutingGroupOwnerAdapter(t *testing.T) {
	f := &channelFake{list: &channelv1.ListRoutingGroupsReply{Groups: []*channelv1.RoutingGroup{{Id: 2, Key: "vip"}}, NextPageToken: "next"}, detail: &channelv1.RoutingGroupDetail{Group: &channelv1.RoutingGroup{Id: 2, Key: "vip"}, ModelGrants: []*channelv1.RoutingGroupModelGrant{{MappingId: 3, AccountId: 1, Model: "managed", ExtraAuthorization: true}}}}
	reader := NewRoutingGroupReader(f)
	query := routing.GroupListRequest{PageSize: 5, PageToken: "token", Filter: `key = "vip"`, OrderBy: "id desc"}
	reply, err := reader.List(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, "next", reply.NextPageToken)
	require.Equal(t, query.Filter, f.query.Filter)
	require.Equal(t, query.PageToken, f.query.PageToken)
	detail, err := reader.Get(context.Background(), 2)
	require.NoError(t, err)
	require.True(t, detail.ModelGrants[0].ExtraAuthorization)
	require.Empty(t, detail.Resources)
	f.detail.Group = nil
	_, err = reader.Get(context.Background(), 2)
	require.ErrorIs(t, err, biz.ErrRoutingGroupUnavailable)
	f.list = nil
	_, err = reader.List(context.Background(), query)
	require.ErrorIs(t, err, biz.ErrRoutingGroupUnavailable)
}
