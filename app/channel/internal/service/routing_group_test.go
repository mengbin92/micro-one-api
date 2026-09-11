package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
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
	return &biz.RoutingGroupDetail{Group: &routing.Group{ID: id, Key: "vip", Status: "enabled"}, ModelGrants: []routing.GroupModelGrant{{AccountID: 1, Model: "managed", ExtraAuthorization: true}}}, f.err
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

func TestRoutingGroupPairRejectsMismatchBeforeSelection(t *testing.T) {
	s := &ChannelService{}
	s.SetRoutingGroupUsecase(&routingGroupFake{})
	ctx := context.Background()
	_, err := s.SelectChannel(ctx, &channelv1.SelectChannelRequest{Group: "VIP", RoutingGroupId: 2, Model: "m"})
	require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
	_, err = s.SelectSubscriptionAccount(ctx, &channelv1.SelectSubscriptionAccountRequest{Group: "VIP", RoutingGroupId: 2, Model: "m"})
	require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
	_, err = s.CheckRoute(ctx, &channelv1.CheckRouteRequest{Group: "VIP", RoutingGroupId: 2, Model: "m", SourceKind: "channel", SourceId: 1})
	require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
}

// unionListRepo shadows ListAvailableModels with a per-group table while
// inheriting every other ChannelRepo behavior from the shared test fake.
type unionListRepo struct {
	*channelServiceRepo
	models map[string][]string
}

func (r *unionListRepo) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	return r.models[group], nil
}

type unionGroupsFake struct {
	groups map[int64]*routing.Group
}

func (f *unionGroupsFake) List(context.Context, biz.RoutingGroupListOptions) ([]*biz.RoutingGroup, error) {
	return nil, nil
}

func (f *unionGroupsFake) Get(_ context.Context, id int64) (*biz.RoutingGroupDetail, error) {
	g, ok := f.groups[id]
	if !ok {
		return nil, biz.ErrRoutingGroupNotFound
	}
	return &biz.RoutingGroupDetail{Group: g}, nil
}

// TestListAvailableModelsUnionsOrderedCandidateGroups covers the ordered
// (auto) token model listing: the reply is the deduplicated union of the
// candidate groups' authorized models, and disabled groups contribute nothing.
func TestListAvailableModelsUnionsOrderedCandidateGroups(t *testing.T) {
	repo := &unionListRepo{
		channelServiceRepo: &channelServiceRepo{channel: &biz.Channel{ID: 101, Status: biz.ChannelStatusEnabled}},
		models: map[string][]string{
			"vip":     {"gpt-4o", "o3"},
			"std":     {"gpt-4o-mini", "gpt-4o"},
			"retired": {"legacy-only"},
		},
	}
	s := NewChannelService(biz.NewChannelUsecase(repo, nil))
	s.SetRoutingGroupUsecase(&unionGroupsFake{groups: map[int64]*routing.Group{
		20: {ID: 20, Key: "vip", Status: "enabled"},
		21: {ID: 21, Key: "std", Status: "enabled"},
		22: {ID: 22, Key: "retired", Status: "disabled"},
	}})
	reply, err := s.ListAvailableModels(context.Background(), &channelv1.ListAvailableModelsRequest{
		Group:           "default",
		RoutingGroupIds: []int64{20, 21, 22},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o", "o3", "gpt-4o-mini"}, reply.Models,
		"union must dedup across groups and skip disabled groups")
}
