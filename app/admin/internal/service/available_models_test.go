package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
)

type modelProofIdentity struct {
	identityv1.IdentityServiceClient
	snapshot *identityv1.GetAuthSnapshotReply
	err      error
	ip       string
}

func (c *modelProofIdentity) GetAuthSnapshot(_ context.Context, p *identityv1.GetAuthSnapshotRequest, _ ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	c.ip = p.ClientIp
	return c.snapshot, c.err
}

type modelProofChannel struct {
	channelv1.ChannelServiceClient
	groups    map[int64]*channelv1.RoutingGroup
	requested []int64
	models    []string
	err       error
	listed    bool
}

func (c *modelProofChannel) GetRoutingGroup(_ context.Context, p *channelv1.GetRoutingGroupRequest, _ ...grpc.CallOption) (*channelv1.RoutingGroupDetail, error) {
	if c.err != nil {
		return nil, c.err
	}
	g := c.groups[p.Id]
	if g == nil {
		return nil, status.Error(codes.NotFound, "group missing")
	}
	return &channelv1.RoutingGroupDetail{Group: g}, nil
}
func (c *modelProofChannel) ListAvailableModels(_ context.Context, p *channelv1.ListAvailableModelsRequest, _ ...grpc.CallOption) (*channelv1.ListAvailableModelsReply, error) {
	c.listed = true
	c.requested = append([]int64(nil), p.RoutingGroupIds...)
	return &channelv1.ListAvailableModelsReply{Models: c.models}, nil
}
func TestAvailableModelsUsesVerifiedTokenRoutesAndWhitelist(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	facts := &commonv1.RoutingSubjectFacts{DefaultGroupId: 1, TokenMode: "fixed", TokenGroupId: 2, TokenRevision: 3, AccessRevision: 4, Grants: []*commonv1.UserRoutingGroupGrant{{GroupId: 2, Status: "active", SourceType: "admin", StartsAt: time.Now().Unix() - 1}}}
	identity := &modelProofIdentity{snapshot: &identityv1.GetAuthSnapshotReply{UserId: 7, TokenId: 9, UserEnabled: true, TokenEnabled: true, AllowedModels: []string{"MODEL-A"}, RoutingFacts: facts}}
	channel := &modelProofChannel{groups: map[int64]*channelv1.RoutingGroup{2: {Id: 2, Key: "vip", Status: "enabled", AccessMode: "restricted", Revision: 5}}, models: []string{"model-a", "hidden"}}
	svc := NewAdminService(nil, identity, channel, nil)
	out, err := svc.AvailableModels(context.Background(), "real-key", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, []string{"model-a"}, out)
	require.Equal(t, []int64{2}, channel.requested)
	require.Equal(t, "127.0.0.1", identity.ip)
	facts.Grants[0].ExpiresAt = time.Now().Unix() - 1
	channel.listed = false
	_, err = svc.AvailableModels(context.Background(), "real-key", "127.0.0.1")
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, channel.listed)
	channel.err = status.Error(codes.Unavailable, "owner down")
	facts.Grants[0].ExpiresAt = 0
	_, err = svc.AvailableModels(context.Background(), "real-key", "127.0.0.1")
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.False(t, channel.listed)
}
func TestAvailableModelsOrderedSkipsRevokedAndDisabledCandidates(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	identity := &modelProofIdentity{snapshot: &identityv1.GetAuthSnapshotReply{UserId: 7, TokenId: 9, UserEnabled: true, TokenEnabled: true, RoutingFacts: &commonv1.RoutingSubjectFacts{TokenMode: "ordered", TokenGroupIds: []int64{1, 2, 3}, TokenRevision: 3, AccessRevision: 4, Grants: []*commonv1.UserRoutingGroupGrant{{GroupId: 1, Status: "active", SourceType: "admin"}, {GroupId: 3, Status: "active", SourceType: "admin"}}}}}
	channel := &modelProofChannel{groups: map[int64]*channelv1.RoutingGroup{1: {Id: 1, Key: "one", Status: "disabled", Revision: 5}, 2: {Id: 2, Key: "two", Status: "enabled", Revision: 5}, 3: {Id: 3, Key: "three", Status: "enabled", Revision: 5}}, models: []string{"model-a"}}
	out, err := NewAdminService(nil, identity, channel, nil).AvailableModels(context.Background(), "real-key", "")
	require.NoError(t, err)
	require.Equal(t, []string{"model-a"}, out)
	require.Equal(t, []int64{3}, channel.requested)
}
