package service

import (
	"context"
	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
	"testing"
)

type routingGroupFake struct {
	calls   int
	options biz.RoutingGroupListOptions
	err     error
}

func (f *routingGroupFake) List(_ context.Context, o biz.RoutingGroupListOptions) ([]*biz.RoutingGroup, error) {
	f.calls++
	f.options = o
	if f.err != nil {
		return nil, f.err
	}
	return []*biz.RoutingGroup{{ID: 1, Key: "VIP"}, {ID: 2, Key: "vip"}}, nil
}
func (f *routingGroupFake) Get(_ context.Context, id int64) (*biz.RoutingGroupDetail, error) {
	f.calls++
	return &biz.RoutingGroupDetail{Group: &routing.Group{ID: id, Key: "vip"}, ModelGrants: []routing.GroupModelGrant{{AccountID: 1, Model: "managed", ExtraAuthorization: true}}}, f.err
}
func TestRoutingGroupServicePaginationAndValidation(t *testing.T) {
	f := &routingGroupFake{}
	s := &ChannelService{}
	s.SetRoutingGroupUsecase(f)
	ctx := context.Background()
	req := &channelv1.ListRoutingGroupsRequest{PageSize: 1, Filter: `key = "VIP"`, OrderBy: "key desc"}
	result, err := s.ListRoutingGroups(ctx, req)
	require.NoError(t, err)
	require.Len(t, result.Groups, 1)
	require.NotEmpty(t, result.NextPageToken)
	require.Equal(t, "VIP", f.options.Filter["key"])
	require.True(t, f.options.OrderBy[0].Desc)
	require.Equal(t, 2, f.options.Limit)
	req.PageToken = result.NextPageToken
	_, err = s.ListRoutingGroups(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, f.options.Offset)
	req.Filter = `key = "vip"`
	_, err = s.ListRoutingGroups(ctx, req)
	require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
	for _, bad := range []*channelv1.ListRoutingGroupsRequest{nil, {PageSize: -1}, {PageSize: 201}, {Filter: `status = "enabled" OR key = "vip"`}, {OrderBy: "id;DROP TABLE users"}, {PageToken: "invalid"}} {
		calls := f.calls
		_, err = s.ListRoutingGroups(ctx, bad)
		require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
		require.Equal(t, calls, f.calls)
	}
	_, err = s.GetRoutingGroup(ctx, &channelv1.GetRoutingGroupRequest{Id: -1})
	require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
	detail, err := s.GetRoutingGroup(ctx, &channelv1.GetRoutingGroupRequest{Id: 2})
	require.NoError(t, err)
	require.True(t, detail.ModelGrants[0].ExtraAuthorization)
	f.err = biz.ErrRoutingGroupStorage
	_, err = s.ListRoutingGroups(ctx, &channelv1.ListRoutingGroupsRequest{})
	require.ErrorIs(t, err, biz.ErrRoutingGroupStorage)
}
