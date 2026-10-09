package channelclient

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"testing"
)

type channelFake struct {
	channelv1.ChannelServiceClient
	list            *channelv1.ListRoutingGroupsReply
	detail          *channelv1.RoutingGroupDetail
	query           *channelv1.ListRoutingGroupsRequest
	listCtx, getCtx context.Context
}

func (f *channelFake) ListRoutingGroups(ctx context.Context, q *channelv1.ListRoutingGroupsRequest, _ ...grpc.CallOption) (*channelv1.ListRoutingGroupsReply, error) {
	f.query = q
	f.listCtx = ctx
	return f.list, nil
}
func (f *channelFake) GetRoutingGroup(ctx context.Context, _ *channelv1.GetRoutingGroupRequest, _ ...grpc.CallOption) (*channelv1.RoutingGroupDetail, error) {
	f.getCtx = ctx
	return f.detail, nil
}

type referenceFactsRepo struct{ biz.RoutingAccessRepo }

func (referenceFactsRepo) Facts(context.Context, int64) (*routing.SubjectFacts, error) {
	return &routing.SubjectFacts{DefaultGroupID: 2, AccessRevision: 1}, nil
}

func TestRoutingReferenceReadsStripOperatorMetadata(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	f := &channelFake{list: &channelv1.ListRoutingGroupsReply{}, detail: &channelv1.RoutingGroupDetail{Group: &channelv1.RoutingGroup{Id: 2}}}
	reader := NewRoutingGroupReader(f)
	md := metadata.Pairs("x-operator-authorization", "Bearer user", "x-authorization-reason", "reviewed", "x-authorization-reason-bin", "上游缺货", "trace-id", "trace")
	ctx := metadata.NewOutgoingContext(authorization.WithCredential(context.Background(), "user"), md)
	_, err := reader.List(ctx, routing.GroupListRequest{})
	require.NoError(t, err)
	require.Same(t, ctx, f.listCtx, "ordinary reads retain their operator context")
	uc := biz.NewRoutingAccessUsecase(reader, referenceFactsRepo{})
	_, err = uc.Available(ctx, 42, routing.GroupListRequest{})
	require.NoError(t, err)
	for _, callCtx := range []context.Context{f.listCtx, f.getCtx} {
		forwarded, _ := metadata.FromOutgoingContext(callCtx)
		require.Equal(t, metadata.Pairs("trace-id", "trace"), forwarded, "reference reads must remove both reason formats and the operator")
		require.Empty(t, authorization.Credential(callCtx))
	}
	original, _ := metadata.FromOutgoingContext(ctx)
	require.Equal(t, md, original, "the caller context must remain intact")
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
